package prwait

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func TestParseWaitFlags(t *testing.T) {
	q := &readycore.WaitRequest{}
	ref := Parse([]string{"--until", "merged", "--timeout", "5s", "--interval", "2s", "--once", "--provider", "gitlab"}, q)
	if ref != nil || q.Until != "merged" || q.Timeout != "5s" || q.Interval != "2s" || !q.Once || q.Provider != "gitlab" {
		t.Fatalf("q=%+v ref=%v", q, ref)
	}
}

func TestParseRejectsMissingAndUnknown(t *testing.T) {
	for _, args := range [][]string{{"--timeout"}, {"--bogus", "x"}} {
		ref := Parse(args, &readycore.WaitRequest{})
		if ref == nil || ref.Code != "bad-flags" {
			t.Fatalf("args %v ref %v", args, ref)
		}
	}
}

type pendingHost struct{}

func (pendingHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "--include"):
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\n\r\n" + `{"state":"open","head":{"sha":"abc"}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"statusCheckRollup":[{"__typename":"CheckRun","status":"IN_PROGRESS"}],"reviews":[]}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	}
	return core.Result{ExitCode: 1}, nil
}

func TestCLIWaitTimeoutEnvelope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		runner := &cli.Runner{Out: out, Diag: diag, Human: diag, JSONMode: true, Now: time.Now}
		code := Run(t.Context(), runner, []string{"--until", "merged", "--timeout", "1s"}, core.ReadRequest{Repo: "acme/app", Pr: "42", RepoRoot: t.TempDir(), Runtime: &core.Runtime{Commander: pendingHost{}}})
		var envelope struct {
			Error any `json:"error"`
			Data  struct {
				Outcome string `json:"outcome"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if code != 1 || envelope.Error != nil || envelope.Data.Outcome != "wait-timeout" {
			t.Fatalf("exit=%d stdout=%s", code, out)
		}
	})
}
