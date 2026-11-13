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
		return core.Result{Stdout: `{"headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","status":"IN_PROGRESS"}],"reviews":[]}`}, nil
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

func TestCLIWaitJSONLines(t *testing.T) {
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	runner := &cli.Runner{Out: out, Diag: diag, Human: diag, Now: time.Now}
	code := Run(t.Context(), runner, []string{"--until", "merged", "--timeout", "1s", "--once"}, core.ReadRequest{Repo: "acme/app", Pr: "42", RepoRoot: t.TempDir(), Runtime: &core.Runtime{Commander: pendingHost{}}})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if code != 1 || len(lines) != 2 {
		t.Fatalf("code %d stdout %s", code, out)
	}
	var result struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "condition-unmet" {
		t.Fatalf("result %s", lines[1])
	}
}

func TestCLIWaitForwardsWarningsAndPreCancel(t *testing.T) {
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	runner := &cli.Runner{
		Out: out, Diag: diag, Human: diag, JSONMode: true,
		Now: time.Now, Cmd: "pr-wait", Warnings: []string{"settings-warn"},
	}
	code := Run(t.Context(), runner, []string{"--until", "merged", "--timeout", "1s", "--once"}, core.ReadRequest{
		Repo: "acme/app", Pr: "42", RepoRoot: t.TempDir(), Runtime: &core.Runtime{Commander: pendingHost{}},
	})
	var env struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if code != 1 || len(env.Warnings) != 1 || env.Warnings[0] != "settings-warn" {
		t.Fatalf("code=%d warnings=%v stdout=%s", code, env.Warnings, out)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out.Reset()
	diag.Reset()
	runner.Warnings = nil
	code = Run(ctx, runner, nil, core.ReadRequest{Repo: "acme/app", Pr: "42"})
	var cancelEnv struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &cancelEnv); err != nil {
		t.Fatalf("canceled stdout %q: %v", out.String(), err)
	}
	if code != cli.ExitBroke || cancelEnv.Error == nil || cancelEnv.Error.Code != "canceled" {
		t.Fatalf("pre-canceled exit=%d env=%s", code, out)
	}
}
