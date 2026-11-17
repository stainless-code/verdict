package readycore_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	"github.com/stainless-code/verdict/internal/testenv"
)

const triggerHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeTriggerBundle produces a real PR bundle whose snapshot was resolved
// from a committed base, then drifts the worktree config to prove the base
// bytes are the policy the gate reads.
func writeTriggerBundle(t *testing.T, cfg string) (bundle, root, base string) {
	t.Helper()
	root = t.TempDir()
	t.Chdir(root)
	mkdir(t, filepath.Join(root, ".verdict"))
	write(t, filepath.Join(root, ".verdict", "config.json"), cfg)
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", ".verdict/config.json")
	fixtureGit(t, root, "commit", "-qm", "base")
	base = fixtureGit(t, root, "rev-parse", "HEAD")

	bundle = filepath.Join(root, ".verdict", "state", "5201")
	mkdir(t, bundle)
	write(t, filepath.Join(bundle, "PR.json"), `{"provider":"github","number":5201,`+
		`"title":"fix: retry","baseRefName":"main","head_sha":"`+triggerHead+`",`+
		`"isDraft":false,"author":{"login":"ana"},"labels":[{"name":"deps"}],`+
		`"files":[{"path":"src/retry.go"}],"headRefOid":"`+triggerHead+`",`+
		`"baseRefOid":"`+base+`"}`)
	rs := &config.RunState{BaseSHA: base, Trusted: true}
	if err := config.WriteRunState(bundle, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), coreexec.Runner{}, &controls.Request{
		BundleDir: bundle, Root: root, BaseSHA: base,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	write(t, filepath.Join(root, ".verdict", "config.json"),
		`{"schema_version":"1","trigger":{"paths_include":["never/**"]}}`)
	return bundle, root, base
}

func triggerRequest(root string) *readycore.Request {
	req := &readycore.Request{Filter: true}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Commander: &readyCommander{head: triggerHead}},
		RepoRoot: root,
		Repo:     "acme/app", Pr: "5201",
	}
	return req
}

func TestReadyFilterRunsMatchingPR(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["src/**"],"authors":["ana"]}}`
	dir, root, _ := writeTriggerBundle(t, cfg)
	req := triggerRequest(root)
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("matching PR refused: %v", ref)
	}
	if resp.Data == nil || !resp.Data.Ready {
		t.Fatalf("matching PR did not reach readiness: %+v", resp.Data)
	}
	assertTriggerReceipt(t, dir, true, "matched")
}

func assertTriggerReceipt(t *testing.T, dir string, wantRun bool, wantReason string) {
	t.Helper()
	stored, found, serr := config.ReadRunState(dir)
	if serr != nil || !found || stored.Trigger == nil {
		t.Fatalf("run receipt has no trigger decision (found=%v err=%v)", found, serr)
	}
	if stored.Trigger.Run != wantRun || stored.Trigger.Reason != wantReason {
		t.Errorf("receipt = %+v, want run=%v/%s", stored.Trigger, wantRun, wantReason)
	}
	if stored.Trigger.Head != triggerHead || stored.Trigger.At == "" {
		t.Errorf("receipt = %+v, want the bundle head and an invocation time", stored.Trigger)
	}
	if _, ref := config.VerifyRunState(dir); ref != nil {
		t.Errorf("the appended decision invalidated the run state: %v", ref)
	}
}

func TestReadyFilterRefusesFilteredOutPR(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["protected/**"]}}`
	dir, root, _ := writeTriggerBundle(t, cfg)
	cmdr := &countingCommander{head: triggerHead}
	req := triggerRequest(root)
	req.Commander = cmdr
	_, ref := readycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "filtered-out" {
		t.Fatalf("refusal = %+v, want filtered-out", ref)
	}
	if !strings.Contains(ref.Message, "paths_include") {
		t.Errorf("refusal misses the failing predicate: %q", ref.Message)
	}
	if cmdr.calls != 0 {
		t.Errorf("host reads after filter = %d, want 0", cmdr.calls)
	}
	assertTriggerReceipt(t, dir, false, "paths_include")
}

func TestReadyFilterRefusesUnknownBaseConfigKey(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"bogus":true}}`
	_, root, _ := writeTriggerBundle(t, cfg)
	_, ref := readycore.Run(context.Background(), triggerRequest(root))
	if ref == nil || ref.Code != config.CodeBadConfig || !strings.Contains(ref.Message, "trigger.bogus") {
		t.Fatalf("refusal = %+v, want bad-config naming trigger.bogus", ref)
	}
}

func TestReadyFilterRefusesHeadPolicySnapshot(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["src/**"]}}`
	dir, root, base := writeTriggerBundle(t, cfg)
	// The PR's own worktree is never the trusted base, even when a fetch
	// explicitly allowed a head snapshot.
	if _, ref := controls.Resolve(t.Context(), coreexec.Runner{}, &controls.Request{
		BundleDir: dir, Root: root, BaseSHA: base, AllowHead: true,
	}); ref != nil {
		t.Fatalf("head controls: %v", ref)
	}
	_, ref := readycore.Run(context.Background(), triggerRequest(root))
	if ref == nil || ref.Code != "trigger-failed" {
		t.Fatalf("refusal = %+v, want trigger-failed for a head snapshot", ref)
	}
}

func TestReadyFilterRefusesMissingSnapshot(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["src/**"]}}`
	dir, root, _ := writeTriggerBundle(t, cfg)
	if err := os.Remove(filepath.Join(dir, "controls.json")); err != nil {
		t.Fatal(err)
	}
	_, ref := readycore.Run(context.Background(), triggerRequest(root))
	if ref == nil || ref.Code != "trigger-failed" {
		t.Fatalf("refusal = %+v, want trigger-failed for a missing snapshot", ref)
	}
}

func TestReadyFilterRefusesMalformedBaseConfig(t *testing.T) {
	_, root, _ := writeTriggerBundle(t, `{"schema_version":"1","trigger":{"title_matches":"["}}`)
	_, ref := readycore.Run(context.Background(), triggerRequest(root))
	if ref == nil || ref.Code != "trigger-failed" {
		t.Fatalf("refusal = %+v, want trigger-failed for a bad regexp", ref)
	}
}

func TestReadyFilterRefusesUnreadableFacts(t *testing.T) {
	dir, root, _ := writeTriggerBundle(t, `{"schema_version":"1","trigger":{"paths_include":["src/**"]}}`)
	write(t, filepath.Join(dir, "PR.json"), `{"files":[`)
	_, ref := readycore.Run(context.Background(), triggerRequest(root))
	if ref == nil || ref.Code != "trigger-failed" {
		t.Fatalf("refusal = %+v, want trigger-failed for corrupt PR.json", ref)
	}
}

func TestReadyFilterWaitsOnlyAfterTheGate(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["protected/**"]}}`
	_, root, _ := writeTriggerBundle(t, cfg)
	cmdr := &countingCommander{head: triggerHead}
	req := triggerRequest(root)
	req.Commander = cmdr
	req.WaitFor = "build"
	req.Timeout = "1s"
	req.PollRuntime = readycore.PollRuntime{
		Now:    time.Now,
		Sleep:  func(_ context.Context, _ time.Duration) error { return nil },
		Jitter: func(d time.Duration) time.Duration { return d },
	}
	_, ref := readycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "filtered-out" {
		t.Fatalf("refusal = %+v, want filtered-out before the wait", ref)
	}
	if cmdr.calls != 0 {
		t.Errorf("host calls before the gate = %d, want 0", cmdr.calls)
	}
}

func TestReadyFilterWarnsOnIncompleteFileList(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["src/**"]}}`
	dir, root, _ := writeTriggerBundle(t, cfg)
	write(t, filepath.Join(dir, "filesource.json"),
		`{"source":"cli","cli_cap":100,"count":100,"truncated":true}`)
	req := triggerRequest(root)
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil || resp.Data == nil || !resp.Data.Ready {
		t.Fatalf("incomplete file list should run the review: resp=%+v ref=%v", resp, ref)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "incomplete") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %q, want an incomplete-file-list warning", resp.Warnings)
	}
}

type countingCommander struct {
	readyCommander
	calls int
}

func (c *countingCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	c.calls++
	return c.readyCommander.Run(ctx, cmd)
}
