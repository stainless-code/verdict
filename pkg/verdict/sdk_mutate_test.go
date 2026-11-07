package verdict_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/pkg/verdict"
)

// sdkReview is the review both post tests submit: one inline comment.
const sdkReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
	`"rigor":"pointed","severity":"informational"}]}`

// seedControls snapshots the review controls for a bundle and writes the
// run bundle that binds them. Post gates on that binding: a snapshot whose
// run bundle is missing is a deleted bundle and refuses, and the head
// policy keeps the fixture offline.
func seedControls(t *testing.T, pr string) {
	t.Helper()
	dir := ".verdict/state/" + pr
	snap, ref := controls.Resolve(context.Background(), nil, &controls.Request{
		BundleDir: dir, BaseSHA: sdkBaseB, AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:    sdkBaseB,
		Trusted:    true,
		CapturedAt: sdkAt,
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: sdkBaseB, Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:   &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Inventory:  config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("seed run state: %v", serr)
	}
}

// postCommander scripts the one live read a dry run performs (the live
// head) and fails on any other spawn.
func postCommander(t *testing.T) *fakeCommander {
	t.Helper()
	return &fakeCommander{t: t, rules: []cannedRule{{
		tokens: []string{"repos/acme/app/pulls/7", "head.sha"},
		result: verdict.CommandResult{Stdout: sdkHeadA + "\n"},
	}}}
}

// TestClientPostDryRun proves the dry-run post path through the SDK: the
// typed payload summary, the staged findings file, the streamed preview
// lines, and that nothing was submitted.
func TestClientPostDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "7")
	writeFile(".verdict/state/7/review.json", sdkReview)
	seedControls(t, "7")
	cmdr := postCommander(t)
	progress := &recordedProgress{}
	c := newSDKClient(progress, cmdr)
	defer c.Close()

	resp, err := c.Post(context.Background(), &verdict.PostRequest{
		Invocation: sdkInvocation("7"),
		ReviewFile: ".verdict/state/7/review.json",
		Approve:    "false",
		DryRun:     true,
	})
	requireNoError(t, err)
	if resp.At != sdkAt || resp.Next != "payload NOT posted (dry-run)" {
		t.Errorf("envelope = at %q next %q", resp.At, resp.Next)
	}
	assertPostDryRun(t, decodeData(t, resp.Data), cmdr)
	if staged := readText(t, ".verdict/state/7/findings.dryrun.json"); !strings.Contains(staged, `"body": "Consider clarifying this line."`) {
		t.Errorf("staged store = %s, want the merged comment", staged)
	}
	for _, want := range []string{"preflight:", "dry-run: payload NOT posted"} {
		if !strings.Contains(progress.joinedHuman(), want) {
			t.Errorf("human = %q, want %q", progress.joinedHuman(), want)
		}
	}
}

// assertPostDryRun proves the dry-run payload summary and that no review was
// submitted.
func assertPostDryRun(t *testing.T, doc map[string]any, cmdr *fakeCommander) {
	t.Helper()
	assertFields(t, doc, map[string]any{"dry_run": true, "event": "COMMENT", "commit_id": sdkHeadA})
	anchors := asMap(t, doc["anchors"], "anchors")
	assertNumbers(t, anchors, map[string]float64{"kept": 1})
	findings := asMap(t, doc["findings"], "findings")
	assertFields(t, findings, map[string]any{"file": ".verdict/state/7/findings.dryrun.json"})
	assertNumbers(t, findings, map[string]float64{"open": 1})
	if cmdr.spawned("/reviews") {
		t.Error("dry run spawned the reviews POST")
	}
}

// TestClientPostRefusal proves the post refusal path through the SDK: a
// missing review file refuses typed with the fetch-first hint, before any
// spawn.
func TestClientPostRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "7")
	cmdr := &fakeCommander{t: t}
	c := newSDKClient(nil, cmdr)
	defer c.Close()

	_, err := c.Post(context.Background(), &verdict.PostRequest{
		Invocation: sdkInvocation("7"),
		ReviewFile: ".verdict/state/7/missing.json",
		Approve:    "false",
		DryRun:     true,
	})
	requireRefusal(t, err, "missing-review-file", "try: run the fetch/record step named above first, then re-run")
	if len(cmdr.calls) != 0 {
		t.Errorf("spawns = %v, want none before the review gate", cmdr.calls)
	}
}

// adoptSource is the local handoff store: one anchor the PR diff covers and
// one that drifted past it.
const adoptSource = `[` +
	`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
	`"body":"Adopt me.","suggestion":null,"inBounds":true,"rigor":"pointed",` +
	`"severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567",` +
	`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]},` +
	`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":99,"anchored":true,` +
	`"body":"Drifted anchor here.","suggestion":null,"inBounds":true,"rigor":"pointed",` +
	`"severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678",` +
	`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]}]`

// writeLocalBundle writes one local bundle shell carrying a source and diff.
func writeLocalBundle(t *testing.T, slug, findings string) {
	t.Helper()
	writeFile(".verdict/state/local-"+slug+"/source.json", `{"head_sha":"`+sdkHeadA+`","head_ref":"work"}`)
	writeFile(".verdict/state/local-"+slug+"/full.diff", sdkDiff)
	writeFile(".verdict/state/local-"+slug+"/findings.json", findings)
}

// TestClientAdopt proves the adopt handoff through the SDK: the typed
// summary, the handoff stamp in the target store, the rebuilt hunk snapshot,
// and the bad-from refusal.
func TestClientAdopt(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "4311")
	writeLocalBundle(t, "adopt1", adoptSource)
	progress := &recordedProgress{}
	c := newSDKClient(progress, nil)
	defer c.Close()

	resp, err := c.Adopt(context.Background(), &verdict.AdoptRequest{
		Invocation: sdkInvocation("4311"),
		From:       "adopt1",
	})
	requireNoError(t, err)
	if resp.At != sdkAt || resp.Next != "post the adopted findings, then calibrate to settle them" {
		t.Errorf("envelope = at %q next %q", resp.At, resp.Next)
	}
	doc := decodeData(t, resp.Data)
	assertFields(t, doc, map[string]any{"dir": ".verdict/state/4311", "from": "adopt1"})
	assertNumbers(t, doc, map[string]float64{"adopted": 2, "open": 2})
	if !strings.Contains(resp.Human, "adopted 2 findings from .verdict/state/local-adopt1 (open: 2)") {
		t.Errorf("human = %q, want the handoff summary", resp.Human)
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "no fetch-time hunk snapshot") {
		t.Errorf("warnings = %v, want the hunk-snapshot rebuild warning", resp.Warnings)
	}
	if target := readText(t, ".verdict/state/4311/findings.json"); !strings.Contains(target, `"event": "adopted"`) ||
		!strings.Contains(target, "Adopt me.") {
		t.Errorf("target store = %s, want the stamped handoff", target)
	}
	if readText(t, ".verdict/state/4311/hunks.tsv") == "" {
		t.Error("hunks.tsv is empty, want the rebuilt snapshot")
	}

	_, err = c.Adopt(context.Background(), &verdict.AdoptRequest{
		Invocation: sdkInvocation("4311"),
	})
	requireRefusal(t, err, "bad-from", "try: re-run with one of the valid values named above")
}

// fixRepoFiles is the scratch worktree the fix scope audit reads.
var fixRepoFiles = map[string]string{
	"notes.txt": "line1\nline2\nline3\n",
	"app.txt":   "alpha\nbeta\ngamma\n",
	"other.txt": "other1\nother2\n",
}

// TestClientFix proves one fix pass through the SDK: the ranked finding, the
// pass ledger, the streamed instructions, and the local-bundle refusal.
func TestClientFix(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sdkGitRepo(t, dir, fixRepoFiles)
	writeFindings(t, "local-pass", "["+
		sdkFinding("1123456789abcdef0123456789abcdef01234567", "open", "informational", "pointed")+","+
		sdkFinding("c123456789abcdef0123456789abcdef01234567", "open", "critical", "pointed")+"]")
	progress := &recordedProgress{}
	c := newSDKClient(progress, nil)
	defer c.Close()

	out, err := c.Fix(context.Background(), &verdict.FixRequest{
		Invocation: sdkInvocation("local-pass"),
	})
	requireNoError(t, err)
	if out.At != sdkAt ||
		out.Next != "patch the anchor file, write verification.json, run record to land it, then re-run fix" {
		t.Errorf("envelope = at %q next %q", out.At, out.Next)
	}
	assertFixPass(t, decodeData(t, out.Data))
	if ledger := readText(t, ".verdict/state/local-pass/fix.json"); !strings.Contains(ledger, `"passes":1`) {
		t.Errorf("ledger = %s, want one recorded pass", ledger)
	}
	for _, want := range []string{
		"fix: c1234567 [critical] notes.txt:2 (pass 1/3",
		"» readiness: .verdict/state/local-pass",
	} {
		if !strings.Contains(progress.joinedHuman(), want) {
			t.Errorf("human = %q, want %q", progress.joinedHuman(), want)
		}
	}

	writeFindings(t, "4291", "["+
		sdkFinding("1123456789abcdef0123456789abcdef01234567", "open", "informational", "pointed")+"]")
	_, err = c.Fix(context.Background(), &verdict.FixRequest{
		Invocation: sdkInvocation("4291"),
	})
	requireRefusal(t, err, "not-local", "try: run the fetch/record step named above first, then re-run")
}

// assertFixPass proves the ranked pass and the ledger counters.
func assertFixPass(t *testing.T, doc map[string]any) {
	t.Helper()
	assertFields(t, doc, map[string]any{
		"sig":     "c123456789abcdef0123456789abcdef01234567",
		"locator": "notes.txt:2",
		"dir":     ".verdict/state/local-pass",
	})
	assertNumbers(t, doc, map[string]float64{"pass": 1, "max_passes": 3, "open_inline": 2})
	if doc["complete"] != false {
		t.Errorf("complete = %v, want false on a pass", doc["complete"])
	}
}

// TestClientInit proves the scaffold through the SDK: the typed preset data,
// the written control files, the streamed preview, and the typed refusals for
// a bad preset and a missing confirmation.
func TestClientInit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sdkGitRepo(t, dir, map[string]string{"notes.txt": "line1\n"})
	progress := &recordedProgress{}
	c := newSDKClient(progress, nil)
	defer c.Close()

	resp, err := c.Init(context.Background(), &verdict.InitRequest{Preset: "standard", Yes: true})
	requireNoError(t, err)
	if resp.ExitCode != 0 || resp.At != sdkAt ||
		resp.Next != "pin real panel slugs in .verdict/config.json, tune defaults.budget_usd, then fetch" {
		t.Errorf("envelope = exit %d at %q next %q", resp.ExitCode, resp.At, resp.Next)
	}
	assertInitScaffold(t, decodeData(t, resp.Data))
	requireInitArtifacts(t, resp.Warnings, progress.joinedHuman())
	for _, want := range []string{"init: preset standard", "wrote: .verdict/config.json"} {
		if !strings.Contains(progress.joinedHuman(), want) {
			t.Errorf("human = %q, want %q", progress.joinedHuman(), want)
		}
	}

	_, err = c.Init(context.Background(), &verdict.InitRequest{Preset: "bogus", Yes: true})
	requireRefusal(t, err, "bad-preset", "try: re-run with one of the valid values named above")

	// A changed control needs --yes; without it init refuses instead of
	// overwriting it.
	writeFile(".verdict/config.json", "{\"schema_version\":\"1\",\"defaults\":{\"budget_usd\":9.99}}\n")
	_, err = c.Init(context.Background(), &verdict.InitRequest{Preset: "standard"})
	requireRefusal(t, err, "needs-confirm", "try: re-run with --yes after reviewing the preview, then re-run")
}

// requireInitArtifacts proves init wrote every shared control and warned
// truthfully that they are uncommitted.
func requireInitArtifacts(t *testing.T, warnings []string, human string) {
	t.Helper()
	if cfg := readText(t, ".verdict/config.json"); !strings.Contains(cfg, `"budget_usd": 0.50`) {
		t.Errorf(".verdict/config.json = %q, want the standard budget", cfg)
	}
	requireFilesExist(t, ".verdict/config.json", ".verdict/kb.jsonl", ".verdict/.gitignore")
	if !slices.ContainsFunc(warnings, func(w string) bool {
		return strings.Contains(w, "non-.verdict dirt") || strings.Contains(w, "uncommitted")
	}) {
		t.Errorf("warnings = %v, want a truthful uncommitted-changes warning", warnings)
	}
	if !strings.Contains(human, "check-tree: dirty worktree") {
		t.Errorf("human = %q, want the check-tree summary", human)
	}
}

// requireFilesExist asserts every named shared control exists on disk.
func requireFilesExist(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("init did not write shared control %s: %v", name, err)
		}
	}
}

// assertInitScaffold proves the preset profile, the created controls, and the
// honest tree verdict: init writes shared files without committing them, so a
// fresh repository is correctly reported as carrying uncommitted changes.
func assertInitScaffold(t *testing.T, doc map[string]any) {
	t.Helper()
	assertFields(t, doc, map[string]any{"preset": "standard", "budget_usd": "0.50", "check_tree_clean": false})

	assertNumbers(t, doc, map[string]float64{"kb_min_judged": 5, "kb_max_fp_rate": 0.5})
	files := asSlice(t, doc["files"], "files")
	if len(files) != 3 || files[0] != "config.json:create" {
		t.Errorf("files = %v, want three created controls", files)
	}
}

// TestClientLearn proves the knowledge-base verb through the SDK: an append
// writes the store and returns the typed rule, status lists it, and a short
// veto reason refuses typed.
func TestClientLearn(t *testing.T) {
	t.Chdir(t.TempDir())
	c := newSDKClient(nil, nil)
	defer c.Close()

	out, err := c.Learn(context.Background(), &verdict.LearnRequest{
		Rule:       "Never log secrets in error paths",
		Scope:      "src/*.ts",
		ID:         "no-secret-logs",
		Provenance: "manual",
		Rigor:      "walked",
	})
	requireNoError(t, err)
	if out.At != sdkAt || out.Next != "cite the rule id in review.json kb lists" {
		t.Errorf("envelope = at %q next %q", out.At, out.Next)
	}
	assertAppendedRule(t, asMap(t, decodeData(t, out.Data)["rule"], "rule"))
	wantStore := `{"id":"no-secret-logs","rule":"Never log secrets in error paths",` +
		`"scope":"src/*.ts","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,` +
		`"judged":0,"status":"active"}`
	if store := splitLines(readText(t, ".verdict/kb.jsonl")); len(store) != 1 || store[0] != wantStore {
		t.Errorf("kb.jsonl = %v, want the stored rule row", store)
	}

	listed, err := c.Learn(context.Background(), &verdict.LearnRequest{Status: true})
	requireNoError(t, err)
	if listed.Next != "learn --rule ... to append; learn --retire/--restore to veto" {
		t.Errorf("status next = %q, want the modes hint", listed.Next)
	}
	assertListedRule(t, asSlice(t, decodeData(t, listed.Data)["rules"], "rules"))

	_, err = c.Learn(context.Background(), &verdict.LearnRequest{Retire: "no-secret-logs", Reason: "too short"})
	requireRefusal(t, err, "bad-reason", "try: re-run with one of the valid values named above")
}

// assertAppendedRule proves the typed rule the append returned.
func assertAppendedRule(t *testing.T, rule map[string]any) {
	t.Helper()
	assertFields(t, rule, map[string]any{
		"id":     "no-secret-logs",
		"scope":  "src/*.ts",
		"status": "active",
		"rigor":  "walked",
	})
}

// assertListedRule proves the status listing carries the appended rule.
func assertListedRule(t *testing.T, rules []any) {
	t.Helper()
	if len(rules) != 1 {
		t.Fatalf("rules = %v, want one listed rule", rules)
	}
	assertFields(t, asMap(t, rules[0], "rule"), map[string]any{
		"id":        "no-secret-logs",
		"precision": "n/a",
	})
}
