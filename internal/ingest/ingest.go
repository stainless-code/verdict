// Package ingest is the ingest verb's CLI adapter: argv parsing, bounded
// stdin reading when the source is `-`, one call into ingestcore, warning
// routing, envelope rendering, and exit mapping. Domain logic lives in
// internal/core/ingest.
package ingest

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	ingestcore "github.com/stainless-code/verdict/internal/core/ingest"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes `ingest <owner/repo> <pr> --from sarif <file|-> [--json]`,
// returning the process exit code. Human progress goes to the runner's human
// stream; --json mode prints one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	return run(ctx, r, args, &req, os.Stdin)
}

// run is Run with the stdin seam exposed: a `-` source reads from stdin.
func run(ctx context.Context, r *cli.Runner, args []string, req *readcommon.Request, stdin io.Reader) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	source, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, ingestcore.VerbIngest); done {
		return code
	}
	creq, ref := ingestRequest(r, req, stdin, source)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	resp, ref := ingestcore.Run(ctx, creq)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	r.HumanLine(resp.Human)
	r.Emit(ingestcore.VerbIngest, r.Repo, r.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

func ingestRequest(r *cli.Runner, req *readcommon.Request, stdin io.Reader, source input) (*ingestcore.Request, *core.RefusalError) {
	out := &ingestcore.Request{ReadRequest: r.Read(*req, nil), From: source.Format, SARIF: source.Path}
	if source.Path != "-" {
		return out, nil
	}
	raw, err := jq.Capped(stdin, ingestcore.MaxSARIFBytes)
	if err != nil {
		if errors.Is(err, jq.ErrTooLarge) {
			return nil, &core.RefusalError{
				Code:    "sarif-too-large",
				Message: core.Msgf("SARIF stdin exceeds maximum size of %d bytes", ingestcore.MaxSARIFBytes),
			}
		}
		return nil, &core.RefusalError{
			Code:    "ingest-failed",
			Message: core.Msgf("reading SARIF from stdin: %v", err),
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

// rawFlags is argv as written: the --from format and the one bare source.
// Resolution happens once, after the whole line is read, so a repeated flag
// can never win by position.
type rawFlags struct {
	fromFormat string
	positional string
	fromSet    bool
}

// parseFlags reads `--from <format> <file|->` and its `--from=<format>` form,
// then resolves one source. A bare path, a repeated flag, and an unknown flag
// are all usage errors: the verb takes exactly one source and names it once.
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
	case isFlag(a):
		return 0, &cli.UsageError{
			Message: core.Msgf("unknown flag '%s' for ingest (want --from, --json)", a),
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

// takePath records the one bare argument. It is only a source beside
// `--from sarif`, which resolve enforces.
func (f *rawFlags) takePath(a string) *cli.UsageError {
	if f.positional != "" {
		return &cli.UsageError{
			Message: core.Msgf("unexpected argument %q for ingest (one <file|-> source)", a),
		}
	}
	f.positional = a
	return nil
}

// resolve turns the recorded spellings into one source, or names the conflict.
func (f *rawFlags) resolve() (input, *cli.UsageError) {
	switch {
	case f.fromSet:
		return f.resolveFrom()
	case f.positional != "":
		return input{}, &cli.UsageError{
			Message: core.Msgf("name the source as --from sarif %s", f.positional),
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
			Message: core.Msgf("--from %s needs <file|->", strings.TrimSpace(f.fromFormat)),
		}
	}
	return input{Format: f.fromFormat, Path: f.positional}, nil
}

// isFlag reports whether an argument is a flag rather than the stdin source:
// "-" is the only dash-prefixed argument that names a source.
func isFlag(a string) bool {
	return a != "-" && strings.HasPrefix(a, "-")
}
