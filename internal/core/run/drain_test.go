package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
)

func drainState(root, pr, digest string) *state {
	return &state{
		req: &core.RunRequest{
			Runtime: &core.Runtime{Now: time.Now, Env: fixtureEnv(root)},
			Repo:    "acme/app", Pr: pr, RepoRoot: root, Agent: "claude",
			TimeoutRaw: "30", RoundsRaw: "1",
			LookPath: func(string) (string, error) { return "/bin/true", nil },
		},
		warnings: []string{}, drainDigest: digest,
	}
}

func writeDrainFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDrainDirectiveAndArtifact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	start := time.Now().Add(-time.Second)
	st := drainState(root, "7", "abc123")
	st.req.Now = func() time.Time { return start }
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDrainFile(t, filepath.Join(dir, reviewFile))
	args, ref := st.argsFor(&agentcatalog.Launch{Name: "claude", Binary: "/bin/true"}, dir, 30)
	if ref != nil {
		t.Fatal(ref)
	}
	if filepath.Base(args.abs) != drainFile || !strings.Contains(args.artifact, drainFile) {
		t.Errorf("artifact = %q, want %s", args.abs, drainFile)
	}
	assertDrainDirective(t, args.prompt)
	assertFreshOnly(t, args.abs, start)
}

func assertDrainDirective(t *testing.T, prompt string) {
	t.Helper()
	for _, want := range []string{"drain.json", "threads.json", "findings.json", "abc123", "defer", "post nothing", "untrusted data"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("directive misses %q:\n%s", want, prompt)
		}
	}
}

func assertFreshOnly(t *testing.T, path string, start time.Time) {
	t.Helper()
	if art := inspectArtifact(path, start); art.exists {
		t.Error("a review.json from another run satisfied the drain artifact assertion")
	}
	writeDrainFile(t, path)
	if art := inspectArtifact(path, start); !art.fresh {
		t.Error("a fresh drain.replies.json was not accepted")
	}
}

func drainBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"number":42,"state":"OPEN"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func drainRequest(root string, cmdr *scripted) *core.RunRequest {
	req := drainState(root, "42", "digest").req
	req.Commander = cmdr
	req.Environ = []string{}
	return req
}

func TestDrainLaunchAssertsRepliesArtifact(t *testing.T) {
	t.Parallel()
	root, dir := drainBundle(t)
	script := &scripted{res: core.Result{ExitCode: 0}}
	script.write = func() {
		name := promptedArtifact(t, script.cmd.Argv[len(script.cmd.Argv)-1])
		writeDrainFile(t, filepath.Join(dir, name))
	}
	req := drainRequest(root, script)
	res, ref := Drain(context.Background(), req, "digest")
	if ref != nil || res == nil || res.Data.Artifact == "" || !res.Data.Fresh {
		t.Fatalf("drain launch = %+v, refusal %+v", res, ref)
	}
	if filepath.Base(res.Data.Artifact) != drainFile || !strings.Contains(res.Next, drainFile) || strings.Contains(res.Next, "--post") {
		t.Errorf("artifact = %q, next = %q", res.Data.Artifact, res.Next)
	}
	assertMissingArtifact(t, req, script, dir)
}

func promptedArtifact(t *testing.T, prompt string) string {
	t.Helper()
	const prefix = "Write exactly one file, "
	_, after, found := strings.Cut(prompt, prefix)
	if !found {
		t.Fatalf("prompt does not name an artifact:\n%s", prompt)
	}
	name, _, ok := strings.Cut(after, ",")
	if !ok {
		t.Fatalf("prompt does not delimit the artifact:\n%s", prompt)
	}
	return filepath.Base(strings.TrimSpace(name))
}

func TestRunDirectiveStillNamesReviewFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	start := time.Now().Add(-time.Second)
	st := drainState(root, "7", "")
	st.req.Now = func() time.Time { return start }
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args, ref := st.argsFor(&agentcatalog.Launch{Name: "claude", Binary: "/bin/true"}, dir, 30)
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(args.prompt, reviewFile) || strings.Contains(args.prompt, drainFile) {
		t.Errorf("run directive changed:\n%s", args.prompt)
	}
}

func assertMissingArtifact(t *testing.T, req *core.RunRequest, script *scripted, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, drainFile)); err != nil {
		t.Fatal(err)
	}
	script.write = nil
	if _, ref := Drain(context.Background(), req, "digest"); ref == nil || ref.Code != "no-review-artifact" || !strings.Contains(ref.Message, drainFile) {
		t.Errorf("missing artifact refusal = %+v", ref)
	}
	script.write = func() { writeDrainFile(t, filepath.Join(dir, reviewFile)) }
	if _, ref := Drain(context.Background(), req, "digest"); ref == nil || ref.Code != "no-review-artifact" {
		t.Errorf("only a review.json was written; refusal = %+v", ref)
	}
}
