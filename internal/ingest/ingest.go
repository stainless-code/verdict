// Package ingest is the ingest verb's CLI adapter: argv parsing, bounded
// stdin reading when --sarif -, one call into ingestcore, warning routing,
// envelope rendering, and exit mapping. Domain logic lives in
// internal/core/ingest.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

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

// Run executes `ingest <owner/repo> <pr> --sarif <file|-> [--json]`, returning
// the process exit code. Human progress goes to the runner's human stream;
// --json mode prints one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req *Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	sarifFlag, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, ingestcore.VerbIngest); done {
		return code
	}
	creq, ref := ingestRequest(r, req, sarifFlag)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	resp, ref := ingestcore.Run(ctx, creq)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprintln(r.Human, resp.Human)
	if r.JSONMode {
		env := core.RenderEnvelope(ingestcore.VerbIngest, r.Repo, r.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

func ingestRequest(r *cli.Runner, req *Request, sarifFlag string) (*ingestcore.Request, *core.RefusalError) {
	read := core.ReadRequest{
		Runtime:  &core.Runtime{Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}},
		Repo:     req.Repo,
		Pr:       req.Pr,
		RepoRoot: req.RepoRoot,
	}
	out := &ingestcore.Request{ReadRequest: read, SARIF: sarifFlag}
	if sarifFlag != "-" {
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

func parseFlags(r *cli.Runner, args []string) (sarif string, code int, done bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--sarif" {
			v, next, u := cli.FlagValue(args, i, a, "<file|->")
			if u != nil {
				return "", r.ReportUsage(u), true
			}
			sarif = v
			i = next
			continue
		}
		msg := fmt.Sprintf("unknown flag '%s' for ingest (want --sarif, --json)", a)
		return "", r.ReportUsage(&cli.UsageError{Message: msg}), true
	}
	return sarif, 0, false
}
