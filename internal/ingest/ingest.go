// Package ingest is the ingest verb's CLI adapter: argv parsing, bounded
// stdin reading when the source is `-`, one call into ingestcore, warning
// routing, envelope rendering, and exit mapping. Domain logic lives in
// internal/core/ingest.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	ingestcore "github.com/stainless-code/verdict/internal/core/ingest"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved ingest invocation.
type Request struct {
	Stdin    io.Reader
	Env      map[string]string
	Repo     string
	Pr       string
	Root     string
	RepoRoot string
}

// Run executes `ingest <owner/repo> <pr> --from sarif <file|-> [--json]`,
// returning the process exit code. Human progress goes to the runner's human
// stream; --json mode prints one envelope to stdout. The legacy `--sarif
// <file|->` form names the same source and stays accepted.
func Run(ctx context.Context, r *cli.Runner, args []string, req *Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	source, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, ingestcore.VerbIngest); done {
		return code
	}
	creq, ref := ingestRequest(r, req, source)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	resp, ref := ingestcore.Run(ctx, creq)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprintln(r.Human, resp.Human)
	r.Emit(ingestcore.VerbIngest, r.Repo, r.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

func ingestRequest(r *cli.Runner, req *Request, source input) (*ingestcore.Request, *core.RefusalError) {
	read := r.Read(core.ReadRequest{
		Runtime:  &core.Runtime{Env: req.Env},
		Repo:     req.Repo,
		Pr:       req.Pr,
		RepoRoot: req.RepoRoot,
	}, nil)
	out := &ingestcore.Request{ReadRequest: read, From: source.Format, SARIF: source.Path}
	if source.Path != "-" {
		return out, nil
	}
	in := req.Stdin
	if in == nil {
		in = os.Stdin
	}
	raw, err := jq.Capped(in, ingestcore.MaxSARIFBytes)
	if err != nil {
		if errors.Is(err, jq.ErrTooLarge) {
			return nil, &core.RefusalError{
				Code:    "sarif-too-large",
				Message: fmt.Sprintf("SARIF stdin exceeds maximum size of %d bytes", ingestcore.MaxSARIFBytes),
			}
		}
		return nil, &core.RefusalError{
			Code:    "ingest-failed",
			Message: fmt.Sprintf("reading SARIF from stdin: %v", err),
		}
	}
	out.SARIF = ""
	out.SARIFData = raw
	out.SARIFInline = true
	return out, nil
}

// input is the resolved SARIF source: the format named by --from and the file
// path, or "-" for stdin.
type input struct {
	Format string
	Path   string
}

// rawFlags is argv as written: which source spellings appeared, and what each
// carried. Resolution happens once, after the whole line is read, so a
// conflicting or repeated spelling can never win by position.
type rawFlags struct {
	fromFormat string
	positional string
	aliasPath  string
	fromSet    bool
	aliasSet   bool
}

// parseFlags reads `--from <format> <file|->`, its `--from=<format>` form, and
// the legacy `--sarif <file|->` spelling, then resolves one source. A bare
// path, a repeated flag, two spellings of the same source, and an unknown
// single-dash flag are all usage errors: the verb takes exactly one source and
// names it once.
func parseFlags(r *cli.Runner, args []string) (in input, code int, done bool) {
	var raw rawFlags
	for i := 0; i < len(args); i++ {
		consumed, u := raw.take(args, i)
		if u != nil {
			return input{}, r.ReportUsage(u), true
		}
		i += consumed
	}
	in, u := raw.resolve()
	if u != nil {
		return input{}, r.ReportUsage(u), true
	}
	return in, 0, false
}

// take consumes one argument, plus the value a spaced flag carries, and reports
// the usage error a malformed spelling earns.
func (f *rawFlags) take(args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	switch {
	case a == "--from":
		v, next, u := cli.FlagValue(args, i, a, "sarif")
		return f.takeFrom(v, next-i, u)
	case strings.HasPrefix(a, "--from="):
		return f.takeFrom(strings.TrimPrefix(a, "--from="), 0, nil)
	case a == "--sarif":
		return f.takeAlias(args, i)
	case isFlag(a):
		return 0, &cli.UsageError{
			Message: fmt.Sprintf("unknown flag '%s' for ingest (want --from, --sarif, --json)", a),
		}
	default:
		return 0, f.takePath(a)
	}
}

// takeFrom records the --from format, refusing a second --from outright.
func (f *rawFlags) takeFrom(value string, consumed int, u *cli.UsageError) (int, *cli.UsageError) {
	if u != nil {
		return 0, u
	}
	if f.fromSet {
		return 0, &cli.UsageError{Message: "--from was given twice; name one source"}
	}
	f.fromSet = true
	f.fromFormat = value
	return consumed, nil
}

// takeAlias reads the older `--sarif <file|->` spelling of the same source.
func (f *rawFlags) takeAlias(args []string, i int) (int, *cli.UsageError) {
	v, next, u := cli.FlagValue(args, i, args[i], "<file|->")
	if u != nil {
		return 0, u
	}
	if f.aliasSet {
		return 0, &cli.UsageError{Message: "--sarif was given twice; name one source"}
	}
	f.aliasSet = true
	f.aliasPath = v
	return next - i, nil
}

// takePath records the one bare argument. It is only a source beside
// `--from sarif`, which resolve enforces.
func (f *rawFlags) takePath(a string) *cli.UsageError {
	if f.positional != "" {
		return &cli.UsageError{
			Message: fmt.Sprintf("unexpected argument %q for ingest (one <file|-> source)", a),
		}
	}
	f.positional = a
	return nil
}

// resolve turns the recorded spellings into one source, or names the conflict.
func (f *rawFlags) resolve() (input, *cli.UsageError) {
	switch {
	case f.fromSet && f.aliasSet:
		return input{}, &cli.UsageError{Message: "--from and --sarif name one source; pass one"}
	case f.aliasSet && f.positional != "":
		return input{}, &cli.UsageError{
			Message: fmt.Sprintf("unexpected argument %q after --sarif; pass one <file|-> value", f.positional),
		}
	case f.aliasSet:
		return input{Path: f.aliasPath}, nil
	case f.fromSet:
		return f.resolveFrom()
	case f.positional != "":
		return input{}, &cli.UsageError{
			Message: fmt.Sprintf("name the source as --from sarif %s (or --sarif %s)", f.positional, f.positional),
		}
	default:
		// No source at all: the core service refuses missing-sarif with its hint.
		return input{}, nil
	}
}

// resolveFrom reads the format and the path beside it, refusing an empty or
// whitespace format and a source that is missing or doubled.
func (f *rawFlags) resolveFrom() (input, *cli.UsageError) {
	if strings.TrimSpace(f.fromFormat) == "" {
		return input{}, &cli.UsageError{Message: "--from needs a format (want sarif)"}
	}
	if f.positional == "" {
		return input{}, &cli.UsageError{
			Message: fmt.Sprintf("--from %s needs <file|->", strings.TrimSpace(f.fromFormat)),
		}
	}
	return input{Format: f.fromFormat, Path: f.positional}, nil
}

// isFlag reports whether an argument is a flag rather than the stdin source:
// "-" is the only dash-prefixed argument that names a source.
func isFlag(a string) bool {
	return a != "-" && strings.HasPrefix(a, "-")
}
