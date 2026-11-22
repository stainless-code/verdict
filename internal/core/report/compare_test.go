package reportcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// compareNow is the fixed clock every compare fixture stamps.
var compareNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const (
	compareHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	compareBaseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// compareFullDiff is the reported run's reviewed diff: three files carry
// hunks, and src/removed.ts is absent because the fix reverted it to base.
const compareFullDiff = `diff --git a/src/excluded.ts b/src/excluded.ts
index 1111111..2222222 100644
--- a/src/excluded.ts
+++ b/src/excluded.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
diff --git a/src/kept.ts b/src/kept.ts
index 3333333..4444444 100644
--- a/src/kept.ts
+++ b/src/kept.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
diff --git a/src/seen.ts b/src/seen.ts
index 5555555..6666666 100644
--- a/src/seen.ts
+++ b/src/seen.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
diff --git a/src/uncovered.ts b/src/uncovered.ts
index 7777777..8888888 100644
--- a/src/uncovered.ts
+++ b/src/uncovered.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`

// compareReview attests src/seen.ts only, so src/uncovered.ts stays
// unattested even though the reviewer saw its hunk in the diff.
const compareReview = `{"schema_version":"1","coverage":[{"file":"src/seen.ts","ranges":"1-3"}]}`

const compareExcluded = `{"schema_version":"1","excluded":[{"path":"src/excluded.ts","rule_id":"builtin:secret","pattern":"*","reason":"default","bytes":120}]}`

// compareBaselineFindings is the prior run: one finding per bucket rule,
// plus a pathless section finding that can never prove coverage.
const compareBaselineFindings = `[
 {"kind":"inline","path":"src/kept.ts","side":"RIGHT","line":2,"body":"kept bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"open"},
 {"kind":"inline","path":"src/seen.ts","side":"RIGHT","line":2,"body":"seen bug","severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"},
 {"kind":"inline","path":"src/removed.ts","side":"RIGHT","line":2,"body":"removed bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03","status":"open"},
 {"kind":"inline","path":"src/excluded.ts","side":"RIGHT","line":2,"body":"excluded bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04","status":"open"},
 {"kind":"inline","path":"src/uncovered.ts","side":"RIGHT","line":2,"body":"uncovered bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05","status":"open"},
 {"kind":"section","heading":"Missing migration note","body":"no location","severity":"informational","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa06","status":"open"}
]`

// compareTargetFindings is the reported run: the kept bug persists with a
// severity and status move, and one finding is new.
const compareTargetFindings = `[
 {"kind":"inline","path":"src/kept.ts","side":"RIGHT","line":2,"body":"kept bug","severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"addressed"},
 {"kind":"inline","path":"src/new.ts","side":"RIGHT","line":9,"body":"new bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa07","status":"open"}
]`

// compareEnv is one fixture pair on disk.
type compareEnv struct {
	dir      string
	target   string
	baseline string
}

// writeCompareFile writes one fixture file, creating parents.
func writeCompareFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// compareFixture builds a provider PR target bundle and a local baseline
// bundle in a throwaway working directory.
func compareFixture(t *testing.T) *compareEnv {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	env := &compareEnv{dir: dir, target: ".verdict/state/6201", baseline: ".verdict/state/local-prior"}
	writeCompareFile(t, env.target+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+compareHeadSHA+`","baseRefOid":"`+compareBaseSHA+`",`+
			`"url":"https://github.com/acme/app/pull/6201"}`)
	writeCompareFile(t, env.target+"/findings.json", compareTargetFindings)
	writeCompareFile(t, env.target+"/full.diff", compareFullDiff)
	writeCompareFile(t, env.target+"/review.json", compareReview)
	writeCompareFile(t, env.target+"/excluded.json", compareExcluded)
	writeCompareFile(t, env.baseline+"/source.json",
		`{"repo":"acme/app","head_sha":"`+compareBaseSHA+`","base_sha":"cccccccccccccccccccccccccccccccccccccccc"}`)
	writeCompareFile(t, env.baseline+"/findings.json", compareBaselineFindings)
	return env
}

// runCompare executes one report --compare and returns the response.
func runCompare(t *testing.T, _ *compareEnv, compare string, flags *Flags) (*Response, *core.RefusalError) {
	t.Helper()
	flags.Compare, flags.CompareSet = compare, true
	return Run(context.Background(), &Request{
		Runtime:   &core.Runtime{Now: func() time.Time { return compareNow }},
		Repo:      "acme/app",
		Pr:        "6201",
		Flags:     *flags,
		Effective: core.Defaults(),
	})
}

// TestCompareFourBuckets pins the whole classification: new, persisting with
// its moves, both resolved proofs, and every not-reviewed reason.
//
//nolint:revive,cyclop // one assertion block per bucket
func TestCompareFourBuckets(t *testing.T) {
	env := compareFixture(t)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	compare := resp.Data.Compare
	if compare == nil {
		t.Fatal("data.compare absent")
	}
	if compare.Counts.New != 1 || compare.Counts.Persisting != 1 ||
		compare.Counts.Resolved != 2 || compare.Counts.NotReviewed != 3 {
		t.Fatalf("counts = %+v, want new=1 persisting=1 resolved=2 not_reviewed=3", compare.Counts)
	}
	if compare.Baseline.Findings != 6 || compare.Baseline.Name != "local-prior" ||
		compare.Baseline.Dir != ".verdict/state/local-prior" {
		t.Errorf("baseline metadata = %+v", compare.Baseline)
	}
	if compare.Baseline.HeadSHA == nil || *compare.Baseline.HeadSHA != compareBaseSHA {
		t.Errorf("baseline head = %v, want %s", compare.Baseline.HeadSHA, compareBaseSHA)
	}

	assertBucket(t, "new", compare.New, []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa07:not-in-baseline",
	})
	assertBucket(t, "persisting", compare.Persisting, []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01:in-both",
	})
	if changes := compare.Persisting[0].Changes; len(changes) != 2 ||
		changes[0] != "severity: important→critical" || changes[1] != "status: open→addressed" {
		t.Errorf("persisting changes = %v", changes)
	}
	assertBucket(t, "resolved", compare.Resolved, []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03:removed-from-diff",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02:covered-and-absent",
	})
	assertBucket(t, "not reviewed", compare.NotReviewed, []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa06:no-location",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04:excluded",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05:uncovered-hunk",
	})
}

// assertBucket pins one bucket's exact sig:reason order.
func assertBucket(t *testing.T, name string, rows []core.CompareFinding, want []string) {
	t.Helper()
	got := make([]string, 0, len(rows))
	for i := range rows {
		got = append(got, rows[i].Sig+":"+rows[i].Reason)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s rows = %v, want %v", name, got, want)
	}
}

// TestCompareIncrementalDeltaNeverResolves proves an incremental run's
// reviewed set is its delta: a baseline finding whose file still appears in
// the full PR diff but not in incremental.diff reads not-reviewed, never
// resolved.
func TestCompareIncrementalDeltaNeverResolves(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/incremental.diff", `diff --git a/src/new.ts b/src/new.ts
index 1111111..2222222 100644
--- a/src/new.ts
+++ b/src/new.ts
@@ -8,2 +8,3 @@
 eight
+nine
`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	compare := resp.Data.Compare
	if compare.Counts.Resolved != 0 {
		t.Fatalf("resolved = %d on an incremental run, want 0 (rows %+v)", compare.Counts.Resolved, compare.Resolved)
	}
	reasons := map[string]string{}
	for i := range compare.NotReviewed {
		reasons[compare.NotReviewed[i].Sig] = compare.NotReviewed[i].Reason
	}
	for _, sig := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05",
	} {
		if reasons[sig] != reasonNotInDelta {
			t.Errorf("%s reason = %q, want %q", sig, reasons[sig], reasonNotInDelta)
		}
	}
}

// TestCompareLocalBundleNeverReadsRemovedFromDiff proves a local bundle's
// narrowed diff proves nothing: the baseline finding whose file is absent is
// partial-review, not resolved.
//
//nolint:revive // one fixture and its bucket assertions read as one block
func TestCompareLocalBundleNeverReadsRemovedFromDiff(t *testing.T) {
	env := compareFixture(t)
	// Move the baseline findings to a local target (no PR.json) so the
	// provider full-diff proof is unavailable.
	local := ".verdict/state/local-head"
	writeCompareFile(t, local+"/source.json", `{"repo":"acme/app","head_sha":"`+compareHeadSHA+`"}`)
	writeCompareFile(t, local+"/findings.json", compareTargetFindings)
	writeCompareFile(t, local+"/full.diff", compareFullDiff)
	writeCompareFile(t, local+"/review.json", compareReview)
	writeCompareFile(t, local+"/excluded.json", compareExcluded)
	resp, ref := Run(context.Background(), &Request{
		Runtime:   &core.Runtime{Now: func() time.Time { return compareNow }},
		Repo:      "acme/app",
		Pr:        "local-head",
		Flags:     Flags{Compare: env.baseline},
		Effective: core.Defaults(),
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	compare := resp.Data.Compare
	if compare.Counts.Resolved != 1 {
		t.Fatalf("resolved = %d, want 1 (only the attested src/seen.ts proof)", compare.Counts.Resolved)
	}
	for i := range compare.Resolved {
		if compare.Resolved[i].Sig != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02" {
			t.Errorf("resolved row = %+v, want only covered-and-absent", compare.Resolved[i])
		}
	}
	for i := range compare.NotReviewed {
		if compare.NotReviewed[i].Sig == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03" {
			if compare.NotReviewed[i].Reason != reasonPartialReview {
				t.Errorf("local removed finding reason = %q, want %q", compare.NotReviewed[i].Reason, reasonPartialReview)
			}
			return
		}
	}
	t.Error("local removed finding missing from not-reviewed")
}

// TestCompareUnreadableExclusionReceiptDegrades proves a corrupt
// excluded.json withholds every resolved proof rather than guessing.
func TestCompareUnreadableExclusionReceiptDegrades(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/excluded.json", `{not json`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	compare := resp.Data.Compare
	if compare.Counts.Resolved != 0 {
		t.Fatalf("resolved = %d with an unreadable exclusion receipt, want 0", compare.Counts.Resolved)
	}
	for i := range compare.NotReviewed {
		if compare.NotReviewed[i].Sig == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02" &&
			compare.NotReviewed[i].Reason != reasonPartialReview {
			t.Errorf("covered finding reason = %q, want partial-review", compare.NotReviewed[i].Reason)
		}
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "excluded.json unreadable") {
		t.Errorf("warnings = %v, want the exclusion warning", resp.Warnings)
	}
}

// TestCompareDisplayFiltersDoNotNarrowTheBuckets proves --status/--kind/
// --category narrow the ranked list only.
func TestCompareDisplayFiltersDoNotNarrowTheBuckets(t *testing.T) {
	env := compareFixture(t)
	base, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	filtered, ref := runCompare(t, env, env.baseline, &Flags{Status: "addressed", Kind: "section", Category: "security"})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if filtered.Data.Compare.Counts != base.Data.Compare.Counts {
		t.Errorf("filtered counts = %+v, want %+v", filtered.Data.Compare.Counts, base.Data.Compare.Counts)
	}
}

// TestCompareRefusesBadBaselines pins every typed baseline refusal: missing,
// symlinked, a file, unrecorded, corrupt, the reported bundle itself, and
// another repository.
//
//nolint:revive // the refusal table is one block
func TestCompareRefusesBadBaselines(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, ".verdict/state/local-unrecorded/source.json", `{"repo":"acme/app"}`)
	writeCompareFile(t, ".verdict/state/local-corrupt/findings.json", `{not json`)
	writeCompareFile(t, ".verdict/state/local-unsafe/findings.json", `[]`)
	if err := os.Symlink(filepath.Join(env.dir, env.baseline), ".verdict/state/local-link"); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	writeCompareFile(t, ".verdict/state/local-file/findings.json", `[]`)
	writeCompareFile(t, "not-a-bundle", `plain file`)
	writeCompareFile(t, ".verdict/state/local-other/findings.json", `[]`)
	writeCompareFile(t, ".verdict/state/local-other/source.json", `{"repo":"other/repo"}`)
	// A PR baseline on another host with the same project path: the host check
	// is the only rule that can refuse it.
	writeCompareFile(t, ".verdict/state/6301/PR.json",
		`{"state":"OPEN","headRefOid":"`+compareBaseSHA+`","url":"https://gitlab.com/acme/app/-/merge_requests/7"}`)
	writeCompareFile(t, ".verdict/state/6301/findings.json", "[]")

	cases := []struct {
		name    string
		compare string
		want    string
	}{
		{"missing identifier", "local-absent", codeMissingCompare},
		{"missing path", "no/such/bundle", codeMissingCompare},
		{"symlink", "local-link", codeUnsafeCompare},
		{"symlink with a trailing slash", "local-link/", codeUnsafeCompare},
		{"regular file", "not-a-bundle", codeUnsafeCompare},
		{"unrecorded", "local-unrecorded", codeUnrecordedCompare},
		{"corrupt", "local-corrupt", codeCorruptCompare},
		{"same bundle", env.target, codeBadCompare},
		{"other repository", "local-other", codeBadCompare},
		{"other host, same project", "6301", codeBadCompare},
		{"empty value", "", codeBadCompare},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := runCompare(t, env, tc.compare, &Flags{})
			if ref == nil {
				t.Fatalf("compare %q = success, want refusal %s", tc.compare, tc.want)
			}
			if ref.Code != tc.want {
				t.Errorf("code = %q, want %q (%s)", ref.Code, tc.want, ref.Message)
			}
		})
	}
}

// TestCompareWireOrderAndEmptyArrays pins the --json shape: the object key
// order, the baseline metadata, and [] rather than null for an empty bucket.
//
//nolint:revive // the wire shape reads as one block
func TestCompareWireOrderAndEmptyArrays(t *testing.T) {
	env := compareFixture(t)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	wire := resp.Data.Wire()
	raw, ok := wire.Get("compare")
	if !ok {
		t.Fatal("data.compare absent from the wire object")
	}
	obj, ok := raw.(*jq.Obj)
	if !ok {
		t.Fatalf("compare wire value is %T", raw)
	}
	text := obj.MarshalCompact()
	for _, want := range []string{
		`"baseline":{"dir":".verdict/state/local-prior","name":"local-prior","head_sha":"` + compareBaseSHA + `"`,
		`"counts":{"new":1,"persisting":1,"resolved":2,"not_reviewed":3}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("compare wire %s\nmissing %s", text, want)
		}
	}
	// An empty bucket must render [] and never null.
	empty, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	empty.Data.Compare.New = nil
	value, ok := empty.Data.Wire().Get("compare")
	if !ok {
		t.Fatal("compare disappeared from the wire object")
	}
	emptyObj, ok := value.(*jq.Obj)
	if !ok {
		t.Fatalf("compare wire value is %T", value)
	}
	if got := emptyObj.MarshalCompact(); strings.Contains(got, `"new":null`) {
		t.Errorf("empty new bucket renders null: %s", got)
	}
}

// TestCompareCountsInvariant proves the tallies describe the stores: every
// baseline signature lands in exactly one bucket, and new plus persisting is
// every reported signature.
func TestCompareCountsInvariant(t *testing.T) {
	env := compareFixture(t)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	c := resp.Data.Compare
	if got := c.Counts.Persisting + c.Counts.Resolved + c.Counts.NotReviewed; got != c.Baseline.Findings {
		t.Errorf("baseline buckets sum to %d, want %d", got, c.Baseline.Findings)
	}
	if got := c.Counts.New + c.Counts.Persisting; got != 2 {
		t.Errorf("reported buckets sum to %d, want the 2 signature-bearing reported findings", got)
	}
}

// TestCompareHumanBlock pins the human block's summary line, bucket headings,
// and per-row reasons.
func TestCompareHumanBlock(t *testing.T) {
	env := compareFixture(t)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	text := strings.Join(resp.Human, "\n")
	for _, want := range []string{
		"compare: .verdict/state/local-prior @ " + compareBaseSHA[:7] + " — new=1 persisting=1 resolved=2 not-reviewed=3",
		"new (1)",
		"persisting (1)",
		"resolved (2)",
		"not reviewed (3)",
		"src/seen.ts:2 (aaaaaaaa, critical) — covered-and-absent — seen bug",
		"severity: important→critical, status: open→addressed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human block missing %q:\n%s", want, text)
		}
	}
}

// TestCompareChunksAttestOnlyClaimedChunks proves a chunked bundle's coverage
// comes from the claimed chunks whose diffs cover the finding, not from the
// bundle merely being chunked.
func TestCompareChunksAttestOnlyClaimedChunks(t *testing.T) {
	env := compareFixture(t)
	if err := os.Remove(env.target + "/review.json"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/chunks/chunk-kept.diff", `diff --git a/src/kept.ts b/src/kept.ts
--- a/src/kept.ts
+++ b/src/kept.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`)
	writeCompareFile(t, env.target+"/chunks/chunk-seen.diff", `diff --git a/src/seen.ts b/src/seen.ts
--- a/src/seen.ts
+++ b/src/seen.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`)
	writeCompareFile(t, env.target+"/chunks/manifest.json", `{"chunks":[
 {"name":"chunk-seen","files":["src/seen.ts"],"diff":".verdict/state/6201/chunks/chunk-seen.diff"},
 {"name":"chunk-kept","files":["src/kept.ts"],"diff":".verdict/state/6201/chunks/chunk-kept.diff"}]}`)
	// Only the seen chunk is claimed, so the already-persisting kept finding
	// is irrelevant; a baseline-only finding inside the unclaimed kept chunk
	// must not read resolved through it.
	writeCompareFile(t, env.target+"/review.json", `{"chunks":["chunk-seen"]}`)
	writeCompareFile(t, env.baseline+"/findings.json", strings.Replace(compareBaselineFindings,
		"\n]",
		`
 ,{"kind":"inline","path":"src/kept.ts","side":"RIGHT","line":2,"body":"unclaimed kept bug","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa08","status":"open"}
]`, 1))
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	compare := resp.Data.Compare
	reasons := map[string]string{}
	for i := range compare.Resolved {
		reasons[compare.Resolved[i].Sig] = compare.Resolved[i].Reason
	}
	for i := range compare.NotReviewed {
		reasons[compare.NotReviewed[i].Sig] = compare.NotReviewed[i].Reason
	}
	if reasons["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"] != reasonCoveredAbsent {
		t.Errorf("claimed-chunk finding reason = %q, want %q",
			reasons["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"], reasonCoveredAbsent)
	}
	if reasons["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa08"] != reasonUncoveredHunk {
		t.Errorf("unclaimed-chunk finding reason = %q, want %q",
			reasons["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa08"], reasonUncoveredHunk)
	}
}

// TestCompareCoverageClaimParsing pins the attestation parser: an omitted
// ranges field widens to a whole-file claim, an explicit null attests nothing
// (the evaluator's rule), a malformed value attests nothing, and a range list
// is kept.
//
//nolint:revive,cyclop // the claim matrix reads as one assertion block
func TestCompareCoverageClaimParsing(t *testing.T) {
	if ranges, whole, ok := coverageClaim(nil, false); !ok || !whole || ranges != nil {
		t.Errorf("omitted ranges = %v/%v/%v, want whole-file", ranges, whole, ok)
	}
	if _, whole, ok := coverageClaim(nil, true); ok || whole {
		t.Error(`explicit "ranges": null attested coverage`)
	}
	if _, whole, ok := coverageClaim("all", true); !ok || !whole {
		t.Error(`"all" did not widen to a whole-file claim`)
	}
	if ranges, whole, ok := coverageClaim("10-20", true); !ok || whole ||
		len(ranges) != 1 || ranges[0].Start != 10 || ranges[0].End != 20 {
		t.Errorf(`"10-20" = %v/%v/%v`, ranges, whole, ok)
	}
	if ranges, whole, ok := coverageClaim("nope", true); ok || whole || ranges != nil {
		t.Errorf("malformed claim = %v/%v/%v, want nothing attested", ranges, whole, ok)
	}
}

// TestCompareSideMatching keeps a LEFT anchor out of NEW hunks.
func TestCompareSideMatching(t *testing.T) {
	cases := []struct {
		hunk, finding string
		want          bool
	}{
		{"NEW", "RIGHT", true},
		{"OLD", "RIGHT", false},
		{"OLD", "LEFT", true},
		{"NEW", "LEFT", false},
		{"OLD", "", true},
	}
	for _, tc := range cases {
		if got := sideMatches(tc.hunk, tc.finding); got != tc.want {
			t.Errorf("sideMatches(%q, %q) = %v, want %v", tc.hunk, tc.finding, got, tc.want)
		}
	}
}

// compareReceiptJSON is a 100% stored receipt for one head.
func compareReceiptJSON(head string) string {
	return `{"schema_version":"1","head":"` + head + `",` +
		`"coverage":{"percent":100,"attested":9,"total":9,"files":1,"exempt":0,"gaps":0}}`
}

// runCompareTarget drives one report run for a chosen target positional.
func runCompareTarget(t *testing.T, pr, compare string, flags *Flags) (*Response, *core.RefusalError) {
	t.Helper()
	flags.Compare, flags.CompareSet = compare, true
	return Run(context.Background(), &Request{
		Runtime:   &core.Runtime{Now: func() time.Time { return compareNow }},
		Repo:      "acme/app",
		Pr:        pr,
		Flags:     *flags,
		Effective: core.Defaults(),
	})
}

// rowReason finds one signature's bucket row reason.
//
//nolint:unparam // each assertion reads the one fixture signature it names
func rowReason(t *testing.T, rows []core.CompareFinding, sig string) string {
	t.Helper()
	for i := range rows {
		if rows[i].Sig == sig {
			return rows[i].Reason
		}
	}
	t.Fatalf("signature %s missing from the rows", sig)
	return ""
}

// writeLocalTarget writes a local (non-PR) copy of the reported bundle.
func writeLocalTarget(t *testing.T, env *compareEnv, slug string) {
	t.Helper()
	dir := ".verdict/state/" + slug
	writeCompareFile(t, dir+"/source.json", `{"repo":"acme/app","head_sha":"`+compareHeadSHA+`"}`)
	writeCompareFile(t, dir+"/findings.json", compareTargetFindings)
	writeCompareFile(t, dir+"/full.diff", compareFullDiff)
	writeCompareFile(t, dir+"/review.json", compareReview)
	writeCompareFile(t, dir+"/excluded.json", compareExcluded)
	_ = env
}

// TestComparePRUrlAndLocalBundleInSameRepo proves the repository guard folds a
// real review URL's project path against the local source.json repo: the pair
// a fixed-layout PR bundle and a fetch-local bundle actually produce.
func TestComparePRUrlAndLocalBundleInSameRepo(t *testing.T) {
	env := compareFixture(t)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("PR vs local refusal: %v", ref)
	}
	if resp.Data.Compare == nil || resp.Data.Compare.Counts.Resolved != 2 {
		t.Fatalf("compare = %+v, want the full four-bucket result", resp.Data.Compare)
	}
}

// TestCompareLocalBundleVsPRInSameRepo proves the guard works in the other
// direction too: a local target compared against a fetched PR baseline.
func TestCompareLocalBundleVsPRInSameRepo(t *testing.T) {
	env := compareFixture(t)
	writeLocalTarget(t, env, "local-head")
	writeCompareFile(t, ".verdict/state/6302/PR.json",
		`{"state":"OPEN","headRefOid":"`+compareBaseSHA+`","url":"https://github.com/acme/app/pull/6302"}`)
	writeCompareFile(t, ".verdict/state/6302/findings.json", compareBaselineFindings)
	resp, ref := runCompareTarget(t, "local-head", "6302", &Flags{})
	if ref != nil {
		t.Fatalf("local vs PR refusal: %v", ref)
	}
	if resp.Data.Compare == nil || resp.Data.Compare.Counts.Resolved != 1 {
		t.Fatalf("compare = %+v, want the local covered-and-absent proof only", resp.Data.Compare)
	}
}

// TestCompareLineLessFindingNeedsWholeFileProof proves a finding with a path
// but no line never reads removed-from-diff while its file is still reviewed:
// only a whole-file or complete attestation resolves it.
func TestCompareLineLessFindingNeedsWholeFileProof(t *testing.T) {
	env := compareFixture(t)
	lineLess := `[{"kind":"inline","path":"src/seen.ts","side":"RIGHT","body":"line-less bug",` +
		`"severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"}]`
	writeCompareFile(t, env.baseline+"/findings.json", lineLess)

	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("line-less finding reason = %q, want %q", reason, reasonUncoveredHunk)
	}
	if resp.Data.Compare.Counts.Resolved != 0 {
		t.Errorf("resolved = %d with a partial range claim, want 0", resp.Data.Compare.Counts.Resolved)
	}

	writeCompareFile(t, env.target+"/review.json", `{"coverage":[{"file":"src/seen.ts","ranges":"all"}]}`)
	resp, ref = runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.Resolved, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonCoveredAbsent {
		t.Errorf("line-less finding reason = %q, want %q", reason, reasonCoveredAbsent)
	}
}

// TestCompareIncrementalOutOfHunkIsNotInDelta proves a file the delta touches
// at another line still reads not-in-incremental-diff, never uncovered-hunk.
func TestCompareIncrementalOutOfHunkIsNotInDelta(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/incremental.diff", `diff --git a/src/seen.ts b/src/seen.ts
--- a/src/seen.ts
+++ b/src/seen.ts
@@ -100,3 +100,3 @@
 a
-b
+B
 c
`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonNotInDelta {
		t.Errorf("out-of-delta hunk reason = %q, want %q", reason, reasonNotInDelta)
	}
}

// TestCompareLocalOutOfHunkIsPartialReview proves a local bundle's narrowed
// diff never turns a line outside its hunks into removed-from-diff.
func TestCompareLocalOutOfHunkIsPartialReview(t *testing.T) {
	env := compareFixture(t)
	writeLocalTarget(t, env, "local-head")
	writeCompareFile(t, env.baseline+"/findings.json",
		`[{"kind":"inline","path":"src/seen.ts","side":"RIGHT","line":50,"body":"far bug",`+
			`"severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"}]`)
	resp, ref := runCompareTarget(t, "local-head", env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonPartialReview {
		t.Errorf("local out-of-hunk reason = %q, want %q", reason, reasonPartialReview)
	}
	if resp.Data.Compare.Counts.Resolved != 0 {
		t.Errorf("resolved = %d, want 0", resp.Data.Compare.Counts.Resolved)
	}
}

// TestCompareStaleReceiptIsIgnored proves a 100% receipt for another head is
// no proof at all.
func TestCompareStaleReceiptIsIgnored(t *testing.T) {
	env := compareFixture(t)
	if err := os.Remove(env.target + "/review.json"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/record.json", compareReceiptJSON(compareBaseSHA))
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compare.Counts.Resolved != 1 {
		t.Fatalf("resolved = %d with a stale receipt, want only the removed-from-diff proof",
			resp.Data.Compare.Counts.Resolved)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("stale-receipt finding reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}

// TestCompareReceiptForCurrentHeadCovers proves the same receipt naming the
// bundle's own head does stand in for an absent review.json.
func TestCompareReceiptForCurrentHeadCovers(t *testing.T) {
	env := compareFixture(t)
	if err := os.Remove(env.target + "/review.json"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/record.json", compareReceiptJSON(compareHeadSHA))
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.Resolved, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonCoveredAbsent {
		t.Errorf("current-head receipt reason = %q, want %q", reason, reasonCoveredAbsent)
	}
}

// TestCompareUnusableReviewBlocksReceiptFallback proves a corrupt review.json
// is present (and attests nothing), so a stored receipt cannot fill in.
func TestCompareUnusableReviewBlocksReceiptFallback(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/review.json", `{not json`)
	writeCompareFile(t, env.target+"/record.json", compareReceiptJSON(compareHeadSHA))
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compare.Counts.Resolved != 1 {
		t.Fatalf("resolved = %d with a corrupt review.json, want only removed-from-diff",
			resp.Data.Compare.Counts.Resolved)
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "review.json unreadable") {
		t.Errorf("warnings = %v, want the review warning", resp.Warnings)
	}
}

// TestCompareEmptyCoverageReviewBlocksReceiptFallback proves an explicit
// empty coverage[] is present evidence of 0%, not an absent attestation.
func TestCompareEmptyCoverageReviewBlocksReceiptFallback(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/review.json", `{"coverage":[]}`)
	writeCompareFile(t, env.target+"/record.json", compareReceiptJSON(compareHeadSHA))
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compare.Counts.Resolved != 1 {
		t.Fatalf("resolved = %d with an empty coverage[] review, want only removed-from-diff",
			resp.Data.Compare.Counts.Resolved)
	}
}

// TestCompareNullRangesAttestsNothing proves an explicit "ranges": null is a
// malformed claim, never a whole-file widening.
func TestCompareNullRangesAttestsNothing(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/review.json",
		`{"coverage":[{"file":"src/seen.ts","ranges":null}]}`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("null-ranges finding reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}

// writeChunkedTarget makes the reported bundle chunked: one manifest chunk
// whose diff carries a NEW-only hunk for src/seen.ts.
func writeChunkedTarget(t *testing.T, env *compareEnv, review string) {
	t.Helper()
	writeCompareFile(t, env.target+"/chunks/chunk-seen.diff", `diff --git a/src/seen.ts b/src/seen.ts
--- a/src/seen.ts
+++ b/src/seen.ts
@@ -0,0 +1,3 @@
+a
+b
+c
`)
	writeCompareFile(t, env.target+"/chunks/manifest.json", `{"chunks":[{"name":"chunk-seen",`+
		`"files":["src/seen.ts"],"diff":".verdict/state/6201/chunks/chunk-seen.diff"}]}`)
	writeCompareFile(t, env.target+"/review.json", review)
}

// TestCompareChunkedModeIgnoresCoverage proves a chunked bundle's coverage[]
// claim is ignored, exactly as the evaluator ignores it.
func TestCompareChunkedModeIgnoresCoverage(t *testing.T) {
	env := compareFixture(t)
	writeChunkedTarget(t, env, `{"coverage":[{"file":"src/seen.ts","ranges":"all"}],"chunks":[]}`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("chunked coverage[] finding reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}

// TestCompareChunkedModeChecksSide proves a claimed chunk's NEW hunks never
// attest a LEFT anchor.
func TestCompareChunkedModeChecksSide(t *testing.T) {
	env := compareFixture(t)
	writeChunkedTarget(t, env, `{"chunks":["chunk-seen"]}`)
	writeCompareFile(t, env.baseline+"/findings.json",
		`[{"kind":"inline","path":"src/seen.ts","side":"LEFT","line":2,"body":"old side",`+
			`"severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"}]`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("LEFT anchor against a NEW-only chunk reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}

// TestCompareChunkManifestPathOutsideChunksIgnored proves the chunk diff path
// is derived under the bundle's chunks/ directory, never taken from the
// manifest.
func TestCompareChunkManifestPathOutsideChunksIgnored(t *testing.T) {
	env := compareFixture(t)
	writeChunkedTarget(t, env, `{"chunks":["chunk-seen"]}`)
	// Point the manifest at full.diff (which does carry the seen hunk) and
	// remove the derived chunk diff: a manifest path cannot widen the read.
	if err := os.Remove(env.target + "/chunks/chunk-seen.diff"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/chunks/manifest.json", `{"chunks":[{"name":"chunk-seen",`+
		`"files":["src/seen.ts"],"diff":"`+env.target+`/full.diff"}]}`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("out-of-chunks manifest diff reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}

// TestCompareAbsoluteBaselinePath proves an absolute --compare path under a
// temp root (macOS /var -> /private/var) reads through its OS ancestors.
func TestCompareAbsoluteBaselinePath(t *testing.T) {
	env := compareFixture(t)
	abs := filepath.Join(env.dir, filepath.FromSlash(env.baseline))
	if !filepath.IsAbs(abs) {
		t.Fatalf("fixture path %q is not absolute", abs)
	}
	resp, ref := runCompare(t, env, abs, &Flags{})
	if ref != nil {
		t.Fatalf("absolute baseline refusal: %v", ref)
	}
	if resp.Data.Compare == nil {
		t.Fatal("compare absent for an absolute baseline path")
	}
}

// TestCompareEmptyValueRefuses proves an explicitly empty --compare value is a
// typed refusal, never a silent no-op.
func TestCompareEmptyValueRefuses(t *testing.T) {
	env := compareFixture(t)
	flags := &Flags{Compare: "", CompareSet: true}
	_, ref := runCompareTarget(t, "6201", "", flags)
	if ref == nil || ref.Code != codeBadCompare {
		t.Fatalf("empty compare value = %v, want %s", ref, codeBadCompare)
	}
	// Without the set marker the same zero value means "flag absent".
	off, ref := Run(context.Background(), &Request{
		Runtime:   &core.Runtime{Now: func() time.Time { return compareNow }},
		Repo:      "acme/app",
		Pr:        "6201",
		Effective: core.Defaults(),
	})
	if ref != nil {
		t.Fatalf("absent compare refusal: %v", ref)
	}
	if off.Data.Compare != nil {
		t.Errorf("compare = %+v without the flag, want nil", off.Data.Compare)
	}
	_ = env
}

// TestCompareURLlessPRUsesPositionalRepo proves a PR bundle without a review
// URL falls back to the positional's repository, so the cross-repo guard still
// refuses a different project instead of treating it as unknown.
func TestCompareURLlessPRUsesPositionalRepo(t *testing.T) {
	env := compareFixture(t)
	writeCompareFile(t, env.target+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+compareHeadSHA+`","baseRefOid":"`+compareBaseSHA+`"}`)
	if _, ref := runCompare(t, env, env.baseline, &Flags{}); ref != nil {
		t.Fatalf("URL-less PR vs same-repo local refusal: %v", ref)
	}
	writeCompareFile(t, ".verdict/state/local-other/findings.json", `[]`)
	writeCompareFile(t, ".verdict/state/local-other/source.json", `{"repo":"other/repo"}`)
	_, ref := runCompare(t, env, "local-other", &Flags{})
	if ref == nil || ref.Code != codeBadCompare {
		t.Fatalf("URL-less PR vs another repo = %v, want %s", ref, codeBadCompare)
	}
}

// TestCompareExemptReceiptSkipsGeneratedPaths proves a complete receipt that
// skipped generated files does not attest a generated path.
func TestCompareExemptReceiptSkipsGeneratedPaths(t *testing.T) {
	env := compareFixture(t)
	if err := os.Remove(env.target + "/review.json"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/full.diff", compareFullDiff+`diff --git a/package-lock.json b/package-lock.json
--- a/package-lock.json
+++ b/package-lock.json
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`)
	writeCompareFile(t, env.baseline+"/findings.json",
		`[{"kind":"inline","path":"package-lock.json","side":"RIGHT","line":2,"body":"lock bug",`+
			`"severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"}]`)
	receipt := `{"schema_version":"1","head":"` + compareHeadSHA + `",` +
		`"coverage":{"percent":100,"attested":9,"total":9,"exempt":1,"gaps":0}}`
	writeCompareFile(t, env.target+"/record.json", receipt)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("exempt-receipt generated path reason = %q, want %q", reason, reasonUncoveredHunk)
	}

	writeCompareFile(t, env.target+"/record.json", `{"schema_version":"1","head":"`+compareHeadSHA+`",`+
		`"coverage":{"percent":100,"attested":9,"total":9,"exempt":0,"gaps":0}}`)
	resp, ref = runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.Resolved, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonCoveredAbsent {
		t.Errorf("non-exempt receipt reason = %q, want %q", reason, reasonCoveredAbsent)
	}
}

// TestCompareReceiptModeMustMatch proves a line receipt cannot stand in for a
// chunked bundle, and a chunk receipt cannot stand in for a line bundle.
func TestCompareReceiptModeMustMatch(t *testing.T) {
	env := compareFixture(t)
	if err := os.Remove(env.target + "/review.json"); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, env.target+"/record.json", `{"schema_version":"1","head":"`+compareHeadSHA+`",`+
		`"chunk_coverage":{"attested":2,"total":2}}`)
	resp, ref := runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("chunk receipt on a line bundle reason = %q, want %q", reason, reasonUncoveredHunk)
	}

	writeCompareFile(t, env.target+"/chunks/manifest.json", `{"chunks":[]}`)
	writeCompareFile(t, env.target+"/record.json", compareReceiptJSON(compareHeadSHA))
	resp, ref = runCompare(t, env, env.baseline, &Flags{})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if reason := rowReason(t, resp.Data.Compare.NotReviewed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02"); reason != reasonUncoveredHunk {
		t.Errorf("line receipt on a chunked bundle reason = %q, want %q", reason, reasonUncoveredHunk)
	}
}
