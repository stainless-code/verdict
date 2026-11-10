package verdict_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// TestClientNext proves the next verb through the SDK: the typed envelope
// fields, the ranked finding (findingOf), the status filter, and the empty
// store's nil item.
func TestClientNext(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", sdkStore())
	c := newSDKClient(nil, nil)
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "6101"}

	next, err := c.Next(context.Background(), &verdict.NextRequest{Invocation: inv})
	requireNoError(t, err)
	if next.Repo != "acme/app" || next.PR != "6101" || next.At != sdkAt || next.Status != "open" {
		t.Errorf("envelope = repo %q pr %q at %q status %q", next.Repo, next.PR, next.At, next.Status)
	}
	assertNextItem(t, next.Item)
	if !contains(next.Human, "c1111111 [open] notes.txt:2") {
		t.Errorf("human = %q, want the ranked line", next.Human)
	}

	filtered, err := c.Next(context.Background(), &verdict.NextRequest{Invocation: inv, Status: "addressed"})
	requireNoError(t, err)
	assertFilteredItem(t, filtered.Item)
	if !contains(filtered.Human, "[addressed] notes.txt:2") {
		t.Errorf("addressed human = %q, want the addressed locator line", filtered.Human)
	}

	writeFindings(t, "6101", "[]")
	empty, err := c.Next(context.Background(), &verdict.NextRequest{Invocation: inv})
	requireNoError(t, err)
	if empty.Item != nil {
		t.Errorf("empty item = %+v, want nil", empty.Item)
	}
}

// assertNextItem proves the ranked finding carries its typed locator fields
// and the full store record.
func assertNextItem(t *testing.T, item *verdict.Finding) {
	t.Helper()
	if item == nil {
		t.Fatal("item = nil, want the top ranked finding")
	}
	if item.Sig != sdkSigCrit || item.Status != "open" || item.Rank != 1 {
		t.Errorf("item = sig %q status %q rank %d, want critical at rank 1", item.Sig, item.Status, item.Rank)
	}
	if item.Locator != "notes.txt:2" || item.Anchored != true {
		t.Errorf("item = locator %q anchored %v, want notes.txt:2 and true", item.Locator, item.Anchored)
	}
	assertRecord(t, item.Raw, sdkSigCrit, `"severity":"critical"`, `"locator":"notes.txt:2"`)
}

// assertFilteredItem proves the status filter picks the addressed record.
func assertFilteredItem(t *testing.T, item *verdict.Finding) {
	t.Helper()
	if item == nil || item.Sig != sdkSigAddr || item.Status != "addressed" {
		t.Fatalf("addressed item = %+v, want the addressed record", item)
	}
}

// TestClientShow proves the show verb through the SDK: the full record for a
// unique prefix, and the typed refusals for the unknown and ambiguous
// prefixes (code plus hint).
func TestClientShow(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", sdkStore())
	c := newSDKClient(nil, nil)
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "6101"}

	show, err := c.Show(context.Background(), &verdict.ShowRequest{Invocation: inv, Sig: "c1111111"})
	requireNoError(t, err)
	if show.Repo != "acme/app" || show.PR != "6101" || show.At != sdkAt {
		t.Errorf("envelope = repo %q pr %q at %q", show.Repo, show.PR, show.At)
	}
	assertShowRecord(t, show.Item)

	for _, tc := range []struct{ name, sig, code string }{
		{"unknown", "deadbeef", "unknown-sig"},
		{"ambiguous", "c1111111", "ambiguous-sig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A second finding sharing the "c1111111" prefix makes it
			// ambiguous; "deadbeef" matches nothing at all.
			writeFindings(t, "6101", "["+sdkFinding(sdkSigCrit, "open", "critical", "walked")+","+
				sdkFinding("c111111177777777777777777777777777777777", "open", "important", "pointed")+"]")
			_, err := c.Show(context.Background(), &verdict.ShowRequest{Invocation: inv, Sig: tc.sig})
			requireRefusal(t, err, tc.code, "try: copy the full sig prefix from report output")
		})
	}
}

// assertShowRecord proves the full store record came back in key order.
func assertShowRecord(t *testing.T, item *verdict.Finding) {
	t.Helper()
	if item == nil {
		t.Fatal("item = nil, want the full record")
	}
	assertRecord(t, item.Raw, sdkSigCrit, `"status":"open"`, `"severity":"critical"`, `"body":"Usable body`)
}

// assertRecord proves a Finding.Raw carries the store record itself, with
// every wanted fragment and none of the Obj struct's Go fields.
func assertRecord(t *testing.T, raw []byte, wants ...string) {
	t.Helper()
	record := string(raw)
	for _, want := range wants {
		if !contains(record, want) {
			t.Errorf("raw = %s, want the store record (%q missing)", record, want)
		}
	}
	if contains(record, `"Fields"`) {
		t.Errorf("raw = %s, want the record, not the Obj struct", record)
	}
}

// TestClientTrend proves the trend verb through the SDK: the rollup counts,
// the positionals-free envelope, and the streamed human summary.
func TestClientTrend(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", sdkStore())
	writeFile(".verdict/state/6101/post.json",
		`{"schema_version":"1","review":{"id":99},"head":"`+sdkHeadA+`",`+
			`"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`)
	c := newSDKClient(nil, nil)
	defer c.Close()

	trend, err := c.Trend(context.Background(), &verdict.TrendRequest{Repo: "acme/app"})
	requireNoError(t, err)
	if trend.Repo != "acme/app" || trend.At != sdkAt ||
		trend.Next != "calibrate recent bundles for per-model tier precision" {
		t.Errorf("envelope = repo %q at %q next %q", trend.Repo, trend.At, trend.Next)
	}
	doc := decodeData(t, trend.Data)
	assertNumbers(t, doc, map[string]float64{"bundles": 1, "findings": 3, "open": 2, "addressed": 1, "closed": 0})
	tiers := asMap(t, doc["by_tier"], "by_tier")
	assertNumbers(t, tiers, map[string]float64{"critical": 1, "important": 2, "informational": 0})
	if len(trend.Human) == 0 ||
		trend.Human[0] != "trend: acme/app — 1 bundle(s), 3 findings (open=2 addressed=1 closed=0)" {
		t.Errorf("human = %v, want the rendered rollup summary", trend.Human)
	}
}

// TestClientCalibrate proves the calibrate verb through the SDK: the offline
// probe grades every finding, counts them, writes the grade store, and the
// bad judgment refuses typed.
func TestClientCalibrate(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", calibrateStore())
	c := newSDKClient(nil, nil)
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "6101"}

	resp, err := c.Calibrate(context.Background(), &verdict.CalibrateRequest{Invocation: inv, Probe: true})
	requireNoError(t, err)
	if resp.Repo != "acme/app" || resp.PR != "6101" || resp.At != sdkAt ||
		resp.Next != "adopted-pair signal present — feed X3 into the H2 evidence gate" {
		t.Errorf("envelope = repo %q pr %q at %q next %q", resp.Repo, resp.PR, resp.At, resp.Next)
	}
	doc := decodeData(t, resp.Data)
	counts := asMap(t, doc["counts"], "counts")
	assertNumbers(t, counts, map[string]float64{"findings": 3, "real": 1, "false": 1, "unknown": 1})
	if grades := asSlice(t, doc["grades"], "grades"); len(grades) != 3 {
		t.Errorf("grades = %v, want 3 rows", grades)
	}
	if store := readText(t, ".verdict/state/6101/kb-grades.jsonl"); len(splitLines(store)) != 3 {
		t.Errorf("kb-grades.jsonl = %s, want 3 lines", store)
	}

	_, err = c.Calibrate(context.Background(), &verdict.CalibrateRequest{
		Invocation: inv, Probe: true, Specs: []string{"bogus"},
	})
	requireRefusal(t, err, "bad-judgment", "try: re-run with one of the valid values named above")
}

// TestClientDoctor proves the doctor verb through the SDK: with no gh, git,
// or ps on PATH the preflight rows are the typed unhealthy set (readBare
// needs no repo/PR positionals).
func TestClientDoctor(t *testing.T) {
	t.Chdir(t.TempDir())
	onlyPath(t, "git")
	c := newSDKClient(nil, nil)
	defer c.Close()

	resp, err := c.Doctor(context.Background(), &verdict.DoctorRequest{})
	requireNoError(t, err)
	if resp.Healthy {
		t.Error("healthy = true, want false with no tools on PATH")
	}
	if resp.At != sdkAt || resp.Next != "fix FAIL lines above (WARN lines are advisory), then re-run" {
		t.Errorf("envelope = at %q next %q", resp.At, resp.Next)
	}
	doc := decodeData(t, resp.Data)
	if doc["healthy"] != false {
		t.Errorf("data.healthy = %v, want false", doc["healthy"])
	}
	assertDoctorRows(t, asSlice(t, doc["checks"], "checks"))
	if !contains(strings.Join(resp.Warnings, "\n"), "api-reachable: cannot reach api.github.com with this token") {
		t.Errorf("warnings = %v, want the api-reachable warning", resp.Warnings)
	}
}

// assertDoctorRows proves the tool-missing preflight rows.
func assertDoctorRows(t *testing.T, rows []any) {
	t.Helper()
	want := map[string]struct {
		severity, detail string
		ok               bool
	}{
		"gh-cli":           {"fail", "missing — install github.com/cli/cli", false},
		"gh-auth":          {"fail", "run gh auth login", false},
		"api-reachable":    {"warn", "cannot reach api.github.com with this token", false},
		"storage-writable": {"fail", "cwd writable — .verdict/state/ created on first fetch", true},
	}
	seen := make(map[string]bool, len(want))
	for _, raw := range rows {
		checkDoctorRow(t, asMap(t, raw, "check row"), want, seen)
	}
	for _, name := range doctorRowOrder {
		if !seen[name] {
			t.Errorf("doctor rows are missing %q; the preflight claim is unproven", name)
		}
	}
}

// doctorRowOrder fixes the order the missing-row report names rows in.
var doctorRowOrder = []string{"gh-cli", "gh-auth", "api-reachable", "storage-writable"}

// checkDoctorRow compares one response row with its expectation and records
// that the name appeared. The recording is what makes a renamed row visible:
// without it, an untracked name asserts nothing and the test reads green on no
// evidence.
func checkDoctorRow(t *testing.T, row map[string]any, want map[string]struct {
	severity, detail string
	ok               bool
}, seen map[string]bool,
) {
	t.Helper()
	name, named := row["name"].(string)
	if !named {
		t.Errorf("check row has no name: %v", row)
		return
	}
	expect, tracked := want[name]
	if !tracked {
		return
	}
	seen[name] = true
	if row["ok"] != expect.ok || row["severity"] != expect.severity || row["detail"] != expect.detail {
		t.Errorf("row %s = %v, want ok=%v severity=%s detail=%q",
			name, row, expect.ok, expect.severity, expect.detail)
	}
}

// TestClientCheckTree proves the check-tree gate through the SDK: the clean
// tree, the dirty tree's file list and exit code, and the typed not-a-repo
// refusal.
func TestClientCheckTree(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sdkGitRepo(t, dir, map[string]string{"notes.txt": "line1\n"})
	c := newSDKClient(nil, nil)
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app"}

	clean, err := c.CheckTree(context.Background(), &verdict.CheckTreeRequest{Invocation: inv})
	requireNoError(t, err)
	if clean.At != sdkAt || clean.Next != "check-tree: clean" || clean.Exit != 0 {
		t.Errorf("clean = at %q next %q exit %d", clean.At, clean.Next, clean.Exit)
	}
	if raw := string(clean.Data); raw != `{"clean":true,"ignored_review_lines":0}` {
		t.Errorf("clean data = %s, want the envelope-identical profile", raw)
	}

	writeFile("scratch.txt", "scratch\n")
	dirty, err := c.CheckTree(context.Background(), &verdict.CheckTreeRequest{Invocation: inv})
	requireNoError(t, err)
	if dirty.Exit != 1 || dirty.Next != "check-tree: dirty worktree — commit or stash first:" {
		t.Errorf("dirty = exit %d next %q", dirty.Exit, dirty.Next)
	}
	assertDirtyFiles(t, decodeData(t, dirty.Data))

	t.Chdir(t.TempDir())
	_, err = c.CheckTree(context.Background(), &verdict.CheckTreeRequest{Invocation: inv})
	requireRefusal(t, err, "not-a-repo", "try: run the fetch/record step named above first, then re-run")
}

// assertDirtyFiles proves the dirty-tree file list.
func assertDirtyFiles(t *testing.T, doc map[string]any) {
	t.Helper()
	if doc["clean"] != false {
		t.Errorf("dirty data = %v, want clean false", doc)
	}
	files := asSlice(t, doc["files"], "files")
	if len(files) != 1 || files[0] != "?? scratch.txt" {
		t.Errorf("dirty files = %v, want [\"?? scratch.txt\"]", files)
	}
}

// TestClientReady proves the ready advisory through the SDK: one unresolved
// live thread blocks the review, the typed data carries the blocker, and
// --check reports the gate failure.
func TestClientReady(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", sdkStore())
	cmdr := &fakeCommander{t: t, rules: []cannedRule{
		{tokens: []string{"api", "graphql"}, result: verdict.CommandResult{Stdout: `{"data":{"repository":` +
			`{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":true}]}}}}}`}},
		{tokens: []string{"pr", "view"}, result: verdict.CommandResult{Stdout: `{"isDraft":false,` +
			`"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN",` +
			`"body":"A description that is long enough to pass the thin check easily here.",` +
			`"state":"OPEN","headRefOid":"` + sdkHeadA + `"}`}},
		{tokens: []string{"pr", "checks"}, result: verdict.CommandResult{Stdout: `[{"bucket":"pass","name":"ci / test","state":"SUCCESS"}]`}},
	}}
	progress := &recordedProgress{}
	c := newSDKClient(progress, cmdr)
	defer c.Close()

	ready, err := c.Ready(context.Background(), &verdict.ReadyRequest{
		Invocation: sdkInvocation("6101"), Check: true,
	})
	requireNoError(t, err)
	if ready.Ready || !ready.GateFailed {
		t.Errorf("ready = %v gateFailed = %v, want blocked with a failed gate", ready.Ready, ready.GateFailed)
	}
	assertReadyBlocker(t, decodeData(t, ready.Data))
	if !contains(progress.joinedHuman(), "ready: no — acme/app#6101 has 1 blocker(s)") {
		t.Errorf("human = %q, want the blocked verdict line", progress.joinedHuman())
	}

	_, err = c.Ready(context.Background(), &verdict.ReadyRequest{Invocation: sdkInvocation("9999")})
	requireRefusal(t, err, "missing-bundle", "try: run the fetch/record step named above first, then re-run")
}

// assertReadyBlocker proves the threads-open blocker and the tally.
func assertReadyBlocker(t *testing.T, doc map[string]any) {
	t.Helper()
	assertFields(t, doc, map[string]any{"ready": false, "recommendation": "nudge-author"})
	assertNumbers(t, doc, map[string]float64{"threads_open": 1})
	blockers := asSlice(t, doc["blockers"], "blockers")
	if len(blockers) != 1 {
		t.Fatalf("blockers = %v, want one", blockers)
	}
	blocker := asMap(t, blockers[0], "blocker")
	assertFields(t, blocker, map[string]any{
		"kind":   "threads-open",
		"action": "address or resolve open threads, then re-run ready",
	})
}

// TestClientGate proves the composed gate through the SDK: the typed verdict,
// the dimension order with its tallies, a failed gate that is not a refusal,
// and the usage refusal when no dimension is requested.
func TestClientGate(t *testing.T) {
	t.Chdir(t.TempDir())
	writePRBundle(t, "6101")
	writeFindings(t, "6101", sdkStore())
	c := newSDKClient(nil, nil)
	defer c.Close()

	gate, err := c.Gate(context.Background(), &verdict.GateRequest{
		Invocation: sdkInvocation("6101"), MaxSeverity: "important", Live: "skip", Coverage: "warn",
	})
	requireNoError(t, err)
	if gate.Passed {
		t.Error("passed = true, want false with an open critical finding")
	}
	if gate.Repo != "acme/app" || gate.PR != "6101" || gate.At != sdkAt {
		t.Errorf("envelope = repo %q pr %q at %q", gate.Repo, gate.PR, gate.At)
	}
	assertGateDimensions(t, gate.Dimensions)

	clean, err := c.Gate(context.Background(), &verdict.GateRequest{
		Invocation: sdkInvocation("6101"), MaxSeverity: "critical", RequireHead: true, Live: "skip", Coverage: "warn",
	})
	requireNoError(t, err)
	if clean.Passed {
		t.Error("passed = true, want false with no completed post record")
	}
	if last := clean.Dimensions[len(clean.Dimensions)-1]; last.ID != "live_threads" || last.Status != "skip" {
		t.Errorf("last dimension = %+v, want live_threads/skip", last)
	}

	assertDefaultCoveragePosture(t, c)

	_, err = c.Gate(context.Background(), &verdict.GateRequest{Invocation: sdkInvocation("6101")})
	requireRefusal(t, err, "bad-flags", "try: re-run with one of the valid values named above")
}

// assertDefaultCoveragePosture pins the SDK's coverage default against the
// CLI's: an unset Coverage requires a stored receipt, and an unpublished value
// refuses instead of silently dropping the dimension.
func assertDefaultCoveragePosture(t *testing.T, c *verdict.Client) {
	t.Helper()
	required, err := c.Gate(context.Background(), &verdict.GateRequest{
		Invocation: sdkInvocation("6101"), MaxSeverity: "critical", Live: "skip",
	})
	requireNoError(t, err)
	if defaultCoverage := required.Dimensions[1]; defaultCoverage.ID != "coverage" || defaultCoverage.Status != "unknown" {
		t.Errorf("dimension 1 = %+v, want coverage/unknown under the default posture", defaultCoverage)
	}
	if required.Passed {
		t.Error("passed = true, want false: the default posture requires a stored coverage receipt")
	}
	_, err = c.Gate(context.Background(), &verdict.GateRequest{
		Invocation: sdkInvocation("6101"), MaxSeverity: "critical", Coverage: "sometimes",
	})
	requireRefusal(t, err, "bad-flags", "try: re-run with one of the valid values named above")
}

// wantDim is one expected gate dimension: the wire id and its status.
type wantDim struct{ id, status string }

// assertGateDimensions pins the composed gate's wire order and tallies.
func assertGateDimensions(t *testing.T, dims []verdict.GateDimension) {
	t.Helper()
	want := []wantDim{{"findings", "fail"}, {"coverage", "skip"}, {"live_threads", "skip"}}
	if len(dims) != len(want) {
		t.Fatalf("dimensions = %+v, want %d", dims, len(want))
	}
	for i, w := range want {
		if !matchesDimension(dims[i], w) {
			t.Errorf("dimension %d = %+v, want id %q, status %q, and a detail", i, dims[i], w.id, w.status)
		}
	}
	findings, live := dims[0], dims[2]
	if findings.Count == nil || *findings.Count != 2 {
		t.Errorf("findings count = %v, want the store's two important+ findings", findings.Count)
	}
	if live.Count != nil {
		t.Errorf("live count = %d, want null", *live.Count)
	}
}

// matchesDimension reports whether dim carries want's id and status and says
// what it checked.
func matchesDimension(dim verdict.GateDimension, want wantDim) bool {
	return dim.ID == want.id && dim.Status == want.status && dim.Detail != ""
}

// TestClientDoctorFallsBackWhenTheProbeFails pins doctor's documented posture
// on the SDK path: a probe that cannot answer (git absent from PATH inside a
// checkout) must not become a refusal, because reporting the missing tool is
// exactly what doctor is for. The root error is sealed at the service
// boundary, so this path cannot key on [errors.Is] and must not try.
func TestClientDoctorFallsBackWhenTheProbeFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PATH", t.TempDir())
	c := newSDKClient(nil, nil)
	defer c.Close()

	resp, err := c.Doctor(context.Background(), &verdict.DoctorRequest{})
	requireNoError(t, err)
	if resp.Healthy {
		t.Error("healthy = true, want false with no tools on PATH")
	}
	doc := decodeData(t, resp.Data)
	assertDoctorRows(t, asSlice(t, doc["checks"], "checks"))
}
