package verdict_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// These tests prove the flags the public facade forwards actually reach the
// core services: a dry run writes nothing, strict promotes warnings, the
// cost-name repair renames entries without touching counts, and the fetch
// flags change which checkout and which controls snapshot the bundle reads.

// sdkRecordReview is the passing review every record fixture merges: one
// inline comment anchored on the fixture diff's line 2.
const sdkRecordReview = `{"schema_version":"1","body":"✅ Looks good — one minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
	`"rigor":"pointed","severity":"informational"}]}`

// sdkWarnReview is warning-only input: the comment carries no rigor, so a
// lenient record reports the warning and a strict one refuses.
const sdkWarnReview = `{"schema_version":"1","body":"✅ marker plus enough length to pass the guard ok.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"body":"A finding with enough substance."}]}`

// sdkCostRecord is a two-entry cost panel with no names yet: the cost-name
// repair adds them in panel order and leaves every count alone.
const sdkCostRecord = `{"at":"2026-11-01T00:00:00Z","models":[` +
	`{"model":"m1","role":"synthesizer","input":10,"output":2},` +
	`{"model":"m2","role":"lens","input":5,"output":1}]}`

// writeRecordBundle writes one local bundle record can merge, plus the
// controls snapshot and typed evidence its gates re-verify.
func writeRecordBundle(t *testing.T, slug, review string) {
	t.Helper()
	dir := ".verdict/state/" + slug
	writeFile(dir+"/source.json",
		`{"head_sha":"`+sdkHeadA+`","base_sha":"`+sdkBaseB+`","head_ref":"work"}`)
	writeFile(dir+"/full.diff", sdkDiff)
	writeFile(dir+"/review.json", review)
	seedControls(t, slug)
}

// TestClientRecordDryRun proves DryRun reaches the record service: the
// response reports the dry run and no store or record artifact appears,
// while the plain call writes both and a second dry run leaves them intact.
func TestClientRecordDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	writeRecordBundle(t, "local-dryrun", sdkRecordReview)
	c := newSDKClient(nil, nil)
	defer c.Close()

	assertDryRunEnvelope(t, c)
	before := recordAndSnapshot(t, c)
	assertDryRunLeavesArtifacts(t, c, before)
}

// assertDryRunEnvelope proves a dry run reports the mode and writes neither
// findings.json nor record.json.
func assertDryRunEnvelope(t *testing.T, c *verdict.Client) {
	t.Helper()
	out, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-dryrun"),
		DryRun:     true,
	})
	requireNoError(t, err)
	if out.At != sdkAt || out.Next != "re-run without --dry-run to record; fix every problem first" {
		t.Errorf("envelope = at %q next %q", out.At, out.Next)
	}
	doc := decodeData(t, out.Data)
	assertFields(t, doc, map[string]any{"dir": ".verdict/state/local-dryrun", "dry_run": true})
	assertNumbers(t, asMap(t, doc["findings"], "findings"), map[string]float64{"open": 1})
	requireAbsent(t, ".verdict/state/local-dryrun/findings.json", ".verdict/state/local-dryrun/record.json")
}

// recordAndSnapshot records the fixture, proves both artifacts are non-empty,
// and returns their bytes for a later no-op check.
func recordAndSnapshot(t *testing.T, c *verdict.Client) map[string]string {
	t.Helper()
	recorded, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-dryrun"),
	})
	requireNoError(t, err)
	assertFields(t, decodeData(t, recorded.Data), map[string]any{"dir": ".verdict/state/local-dryrun"})
	before := map[string]string{
		"findings.json": readText(t, ".verdict/state/local-dryrun/findings.json"),
		"record.json":   readText(t, ".verdict/state/local-dryrun/record.json"),
	}
	for name, want := range before {
		if want == "" {
			t.Fatalf("%s is empty, want the recorded artifact", name)
		}
	}
	return before
}

// assertDryRunLeavesArtifacts proves a later dry run does not rewrite either
// artifact written by the plain record call.
func assertDryRunLeavesArtifacts(t *testing.T, c *verdict.Client, before map[string]string) {
	t.Helper()
	if _, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-dryrun"),
		DryRun:     true,
	}); err != nil {
		t.Fatalf("second dry run: %v", err)
	}
	for name, want := range before {
		if got := readText(t, ".verdict/state/local-dryrun/"+name); got != want {
			t.Errorf("a later dry run rewrote %s:\n%s\nwant\n%s", name, got, want)
		}
	}
}

// TestClientRecordStrict proves Strict reaches the record service: the same
// warning-only review records leniently and refuses typed with Strict set.
func TestClientRecordStrict(t *testing.T) {
	t.Chdir(t.TempDir())
	writeRecordBundle(t, "local-strict", sdkWarnReview)
	c := newSDKClient(nil, nil)
	defer c.Close()

	lenient, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-strict"),
		DryRun:     true,
	})
	requireNoError(t, err)
	if !strings.Contains(strings.Join(lenient.Warnings, "\n"), "rigor is unset") {
		t.Fatalf("warnings = %v, want the unset-rigor warning", lenient.Warnings)
	}

	_, err = c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-strict"),
		DryRun:     true,
		Strict:     true,
	})
	requireRefusal(t, err, "bad-review-input",
		"try: fix the malformed agent input named above "+
			"(review.json wants {body, approved, comments[]}; claims want string sigs), then re-run")
	requireAbsent(t, ".verdict/state/local-strict/findings.json", ".verdict/state/local-strict/record.json")
}

// TestClientRecordCostNames proves CostNames with CostPanel reaches the
// cost-name repair: the unnamed models[] entries take the panel's names in
// order and every token count stays where it was.
func TestClientRecordCostNames(t *testing.T) {
	t.Chdir(t.TempDir())
	writeRecordBundle(t, "local-costs", sdkRecordReview)
	writeFile(".verdict/state/local-costs/cost.json", sdkCostRecord)
	c := newSDKClient(nil, nil)
	defer c.Close()

	if _, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-costs"),
		DryRun:     true,
	}); err != nil {
		requireNoError(t, err)
	}
	if got := readText(t, ".verdict/state/local-costs/cost.json"); strings.Contains(got, `"name"`) {
		t.Fatalf("cost.json gained a name without the flag: %s", got)
	}

	out, err := c.Record(context.Background(), &verdict.RecordRequest{
		Invocation: sdkInvocation("local-costs"),
		CostNames:  true,
		CostPanel:  []string{"lead", "lens-1"},
	})
	requireNoError(t, err)
	assertCostNames(t, readText(t, ".verdict/state/local-costs/cost.json"))
	if !strings.Contains(strings.Join(out.Warnings, "\n"),
		"cost names: named 2 models[] entries from the panel; no token count changed") {
		t.Errorf("warnings = %v, want the cost-name summary", out.Warnings)
	}
}

// assertCostNames proves the repaired entries carry the panel names in order
// with their token counts untouched.
func assertCostNames(t *testing.T, raw string) {
	t.Helper()
	var doc struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Input  int    `json:"input"`
			Output int    `json:"output"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("cost.json is not the cost record shape: %v\n%s", err, raw)
	}
	if len(doc.Models) != 2 {
		t.Fatalf("models = %v, want two entries", doc.Models)
	}
	for i, want := range []struct {
		name          string
		input, output int
	}{
		{name: "lead", input: 10, output: 2},
		{name: "lens-1", input: 5, output: 1},
	} {
		got := doc.Models[i]
		if got.Name != want.name || got.Input != want.input || got.Output != want.output {
			t.Errorf("models[%d] = %+v, want name %q counts %d/%d",
				i, got, want.name, want.input, want.output)
		}
	}
}

// sdkWorktreeStash is the canned stash-create commit the isolated checkout
// fixture pins; sdkHeadA is the HEAD it diffs against.
const sdkWorktreeStash = "cccccccccccccccccccccccccccccccccccccccc"

// worktreeCommander scripts fetch-local --worktree in the default dirty
// mode: the isolation stash and checkout, every bundle read through the
// checkout, and the teardown.
func worktreeCommander(t *testing.T, wt string) *fakeCommander {
	t.Helper()
	return &fakeCommander{t: t, rules: []cannedRule{
		{tokens: []string{"rev-parse", "--is-inside-work-tree"}, result: verdict.CommandResult{Stdout: "true\n"}},
		{tokens: []string{"branch", "--show-current"}, result: verdict.CommandResult{Stdout: "work\n"}},
		{tokens: []string{"rev-parse", "HEAD"}, result: verdict.CommandResult{Stdout: sdkHeadA + "\n"}},
		{tokens: []string{"stash", "create"}, result: verdict.CommandResult{Stdout: sdkWorktreeStash + "\n"}},
		{tokens: []string{"ls-tree", ".verdict/config.json"}, result: verdict.CommandResult{}},
		{tokens: []string{"worktree", "add"}, result: verdict.CommandResult{}},
		{tokens: []string{"-C", wt, "diff", "--no-renames"}, result: verdict.CommandResult{Stdout: "notes.txt\n"}},
		{tokens: []string{"-C", wt, "diff", "--numstat"}, result: verdict.CommandResult{Stdout: "1\t1\tnotes.txt\n"}},
		{tokens: []string{"-C", wt, "diff", sdkHeadA, sdkWorktreeStash}, result: verdict.CommandResult{Stdout: sdkDiff}},
		{tokens: []string{"ls-files", "--others"}, result: verdict.CommandResult{}},
		{tokens: []string{"worktree", "remove"}, result: verdict.CommandResult{}},
		{tokens: []string{"patch-id"}, result: verdict.CommandResult{Stdout: sdkPatchID}},
	}}
}

// worktreeAddCommander adds the fake worktree's filesystem side effect that
// the canned commander cannot express: the checkout exists before controls
// resolve from it.
type worktreeAddCommander struct {
	*fakeCommander
	dir string
}

func (c *worktreeAddCommander) Run(ctx context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	if strings.Contains(strings.Join(cmd.Argv, " "), "worktree add") {
		if err := os.MkdirAll(c.dir+"/.verdict", 0o755); err != nil {
			c.t.Fatal(err)
		}
		if err := os.WriteFile(c.dir+"/.verdict/config.json", []byte("{\"version\":1}\n"), 0o644); err != nil {
			c.t.Fatal(err)
		}
	}
	return c.fakeCommander.Run(ctx, cmd)
}

// TestClientFetchLocalWorktreeHeadControls proves the two local fetch flags
// reach the service: Worktree builds the bundle from the temporary isolated
// checkout and removes it, and HeadControls records a HEAD controls snapshot
// instead of reading the base.
func TestClientFetchLocalWorktreeHeadControls(t *testing.T) {
	t.Chdir(t.TempDir())
	stubTools(t, "git")
	wt := ".verdict/state/.worktree-probe"
	cmdr := worktreeCommander(t, wt)
	progress := &recordedProgress{}
	c := newSDKClient(progress, &worktreeAddCommander{fakeCommander: cmdr, dir: wt})
	defer c.Close()

	out, err := c.FetchLocal(context.Background(), &verdict.FetchLocalRequest{
		Invocation:    sdkInvocation("probe"),
		BudgetConsent: true,
		Worktree:      true,
		HeadControls:  true,
	})
	requireNoError(t, err)
	doc := decodeData(t, out.Data)
	assertFields(t, doc, map[string]any{
		"dir": ".verdict/state/local-probe", "kind": "worktree", "base_sha": sdkHeadA,
	})
	assertFields(t, asMap(t, doc["controls"], "controls"), map[string]any{"policy": "head", "sha": ""})
	assertNumbers(t, doc, map[string]float64{"files": 1, "additions": 1, "deletions": 1})
	assertArgvSpawned(t, cmdr, "git -C "+wt+" diff "+sdkHeadA+" "+sdkWorktreeStash)
	assertArgvSpawned(t, cmdr, "git worktree remove --force "+wt)
	if got := readText(t, ".verdict/state/local-probe/controls/config.json"); got != "{\"version\":1}\n" {
		t.Errorf("head controls came from the caller checkout, not the isolated worktree: %q", got)
	}
	// The trusted configuration read still comes from the base commit; only
	// the controls inventory moved to HEAD, so no inventory read appears.
	if cmdr.spawned(".verdict/kb.jsonl") {
		t.Errorf("head controls still read the base inventory: %q", cmdr.calls)
	}
	if !strings.Contains(strings.Join(out.Warnings, "\n"), "explicit opt-out --allow-head-controls") {
		t.Errorf("warnings = %v, want the head-controls opt-out warning", out.Warnings)
	}
	if !strings.Contains(progress.joinedHuman(), "worktree: "+wt+" (temporary isolated checkout; removed on exit)") {
		t.Errorf("human = %q, want the isolated checkout line", progress.joinedHuman())
	}
	if !strings.Contains(readText(t, ".verdict/state/local-probe/full.diff"), "+new line") {
		t.Error("full.diff does not carry the checkout's diff")
	}
}

// TestClientFetchHeadControls proves HeadControls reaches the remote fetch:
// the default read snapshots the base through git, while the flag snapshots
// the worktree and warns about the opt-out.
func TestClientFetchHeadControls(t *testing.T) {
	t.Chdir(t.TempDir())
	stubTools(t, "gh", "git", "curl", "glab")
	cmdr := fetchCommander(t)
	c := newSDKClient(nil, cmdr)
	defer c.Close()

	base, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    sdkInvocation("7"),
		BudgetConsent: true,
	})
	requireNoError(t, err)
	assertFields(t, asMap(t, decodeData(t, base.Data)["controls"], "controls"),
		map[string]any{"policy": "base", "sha": sdkBaseB})
	if !cmdr.spawned("ls-tree") {
		t.Fatalf("the default fetch read no base tree: %q", cmdr.calls)
	}

	cmdr.calls = nil
	head, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    sdkInvocation("7"),
		BudgetConsent: true,
		HeadControls:  true,
	})
	requireNoError(t, err)
	assertFields(t, asMap(t, decodeData(t, head.Data)["controls"], "controls"),
		map[string]any{"policy": "head", "sha": ""})
	if cmdr.spawned(".verdict/kb.jsonl") {
		t.Errorf("head controls still read the base inventory: %q", cmdr.calls)
	}
	if !strings.Contains(strings.Join(head.Warnings, "\n"), "explicit opt-out --allow-head-controls") {
		t.Errorf("warnings = %v, want the head-controls opt-out warning", head.Warnings)
	}
}

// assertArgvSpawned fails unless one recorded spawn joined to exactly argv.
func assertArgvSpawned(t *testing.T, cmdr *fakeCommander, argv string) {
	t.Helper()
	for _, call := range cmdr.calls {
		if strings.Join(call, " ") == argv {
			return
		}
	}
	t.Errorf("no spawn matched %q: %q", argv, cmdr.calls)
}

// requireAbsent fails when any named path exists.
func requireAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a run that must not write it (err=%v)", path, err)
		}
	}
}
