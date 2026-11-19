package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

type stubCommander struct{}

func (*stubCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

func TestNextAfterAgentRecommendsPost(t *testing.T) {
	t.Parallel()
	got := next(Request{Repo: "acme/app", Pr: "7"}, &flags{agent: "claude"})
	const wantCmd = "verdict drain acme/app 7 --post"
	if !strings.Contains(got, wantCmd) {
		t.Fatalf("next = %q, want %q", got, wantCmd)
	}
	_, after, ok := strings.Cut(got, "verdict drain ")
	if !ok {
		t.Fatalf("next %q did not start command with verdict drain", got)
	}
	fields := strings.Fields(after)
	if len(fields) < 3 || fields[0] != "acme/app" || fields[1] != "7" {
		t.Fatalf("recommended args = %v, want repo pr --post", fields)
	}
	fl, code, done := parseFlags(&cli.Runner{}, fields[2:])
	if done || code != 0 || !fl.post {
		t.Fatalf("recommended flags did not parse: fl=%+v code=%d done=%v", fl, code, done)
	}
}

func TestParseFlagsAcceptsPost(t *testing.T) {
	t.Parallel()
	fl, code, done := parseFlags(&cli.Runner{}, []string{"--agent", "claude", "--post"})
	if done || code != 0 || !fl.post || fl.agent != "claude" {
		t.Fatalf("parse = %+v, %d, %v", fl, code, done)
	}
	if !strings.Contains(wantFlags, "--post") {
		t.Errorf("usage %q does not offer --post", wantFlags)
	}
}

func TestRefreshRequestKeepsInjectedCommander(t *testing.T) {
	t.Parallel()
	cmdr := &stubCommander{}
	req := Request{Runtime: &core.Runtime{Commander: cmdr}}
	r := &cli.Runner{Now: time.Now}
	got := refreshRequest(r, req, &flags{provider: "github"})
	if got.Commander != cmdr {
		t.Errorf("refresh commander = %T, want the injected commander", got.Commander)
	}
}

func TestRunAgentAndPostZeroPendingSkipsLaunchAndPost(t *testing.T) {
	t.Parallel()
	root, _ := setupDrainBundle(t)
	cmdr := &drainHostStub{
		pageJSON: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`,
		self:     "verdict-bot",
	}
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Diag: &diag, Human: &diag,
		Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
		Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
	}
	req := Request{
		Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
		Repo:     "acme/app",
		Pr:       "7",
		RepoRoot: root,
	}
	code := Run(context.Background(), r, []string{"--agent", "nonexistent-agent", "--post"}, req)
	if code != cli.ExitOK {
		t.Fatalf("Run exit = %d, out=%s, diag=%s", code, out.String(), diag.String())
	}
	var env struct {
		Data  *draincore.File `json:"data"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if env.Error != nil || env.Data == nil || len(env.Data.Pending) != 0 || len(env.Data.Posted) != 0 {
		t.Errorf("envelope = %+v, want 0 pending, 0 posted, nil error", env)
	}
	dir := filepath.Join(root, ".verdict", "state", "7")
	got, err := os.ReadFile(filepath.Join(dir, draincore.KeyProcedureMD))
	if err != nil {
		t.Fatalf("read %s: %v", draincore.KeyProcedureMD, err)
	}
	if !bytes.Equal(got, drainProcedureBytes(t)) {
		t.Fatalf("%s is not the embedded drain assembly", draincore.KeyProcedureMD)
	}
}

// TestEnsureDraftProcedureRestoresRefsKeepsExistingProcedure pins N4: an
// older procedure is never rewritten, but its pull refs are restored when the
// bundle lost them.
func TestEnsureDraftProcedureRestoresRefsKeepsExistingProcedure(t *testing.T) {
	dir := t.TempDir()
	procedure := filepath.Join(dir, draincore.KeyProcedureMD)
	want := []byte("operator-edited drain procedure\n")
	if err := os.WriteFile(procedure, want, 0o600); err != nil {
		t.Fatal(err)
	}
	refsDir := filepath.Join(dir, "refs")
	if _, err := os.Stat(refsDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: refs dir exists: %v", err)
	}
	if err := ensureDraftProcedure(dir); err != nil {
		t.Fatalf("ensureDraftProcedure: %v", err)
	}
	assertRefsRestored(t, refsDir)
	assertProcedureBytes(t, procedure, want)
}

func assertRefsRestored(t *testing.T, refsDir string) {
	t.Helper()
	for _, ref := range skillfs.HandoffRefs() {
		wantRef, err := skillfs.ReadRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(refsDir, filepath.FromSlash(strings.TrimPrefix(ref.Path, "refs/"))))
		if err != nil {
			t.Fatalf("read %s: %v", filepath.ToSlash(ref.Path), err)
		}
		if !bytes.Equal(got, wantRef) {
			t.Errorf("%s is not the embedded ref bytes", ref.Path)
		}
	}
}

func assertProcedureBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("procedure = %q, want the unchanged %q", got, want)
	}
}

// drainProcedureBytes builds the expected drain Procedure from the embedded
// payload paths, not from the function under test.
func drainProcedureBytes(t *testing.T) []byte {
	t.Helper()
	var out []byte
	for i, name := range []string{
		"prompts/handoff/role-method.md", "prompts/handoff/replies-contract.md", "prompts/handoff/capability-map.md",
	} {
		if i > 0 {
			out = append(out, '\n')
		}
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		out = append(out, raw...)
	}
	return out
}
