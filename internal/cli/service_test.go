package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

func TestEmit(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		json bool
	}{
		{name: "human"},
		{name: "json", json: true, want: `{
  "schema_version": "1",
  "verb": "report",
  "repo": "owner/repo",
  "pr": 7,
  "at": "2026-11-02T12:00:00Z",
  "next": "done",
  "warnings": [
    "notice"
  ],
  "data": "value",
  "error": null
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := &cli.Runner{Out: &out, JSONMode: tc.json}
			r.Emit("report", "owner/repo", "7", "2026-11-02T12:00:00Z", "done", []string{"notice"}, "value")
			if got := out.String(); got != tc.want {
				t.Fatalf("Emit = %q; want %q", got, tc.want)
			}
		})
	}
}

type readCommander struct{ id int }

func (*readCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{ExitCode: 17}, nil
}

func TestReadCopiesRuntime(t *testing.T) {
	originalNow := func() time.Time { return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC) }
	runnerNow := func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	originalCmd := &readCommander{id: 1}
	selectedCmd := &readCommander{id: 2}
	var out bytes.Buffer
	originalProgress := cli.Progress{R: &cli.Runner{Out: &out, Human: &out}}
	rt := &core.Runtime{Env: map[string]string{"KEY": "value"}, Now: originalNow, Commander: originalCmd, Progress: originalProgress}
	req := core.ReadRequest{Runtime: rt, Repo: "owner/repo", Pr: "7", RepoRoot: "root"}
	var injected bytes.Buffer
	r := &cli.Runner{Now: runnerNow, Human: &injected}
	got := r.Read(req, selectedCmd)
	if got.Runtime == rt {
		t.Fatal("Read reused caller runtime")
	}
	if rt.Now().Year() != 2001 || rt.Commander != originalCmd || rt.Progress != originalProgress {
		t.Fatal("Read mutated caller runtime")
	}
	checkReadResult(t, got, selectedCmd)
	got.Progress.Human("injected")
	if out.Len() != 0 {
		t.Fatalf("Read retained caller progress: %q", out.String())
	}
	if injected.String() != "injected\n" {
		t.Fatalf("Read progress = %q", injected.String())
	}
}

func checkReadResult(t *testing.T, got core.ReadRequest, selectedCmd *readCommander) {
	t.Helper()
	if got.Now().Year() != 2026 || got.Commander != selectedCmd {
		t.Fatal("Read did not inject runner dependencies")
	}
	if got.Repo != "owner/repo" || got.Pr != "7" || got.RepoRoot != "root" || got.Env["KEY"] != "value" {
		t.Fatalf("Read lost input: %+v", got)
	}
}

func TestReadInitializesRuntime(t *testing.T) {
	r := &cli.Runner{}
	selectedCmd := &readCommander{id: 2}
	empty := r.Read(core.ReadRequest{}, selectedCmd)
	if empty.Runtime == nil || empty.Commander != selectedCmd {
		t.Fatal("Read did not initialize runtime")
	}
}
