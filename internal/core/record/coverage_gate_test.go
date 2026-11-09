package recordcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// coverageSlug names the local bundle every gate case records into.
const coverageSlug = "local-cov"

// coverageHead is the head SHA the fixture bundle was fetched at.
const coverageHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// coverageLinePrefix is the one body line a partial attestation adds.
const coverageLinePrefix = "> ⚠️ coverage:"

// codeCoverageIncomplete is the refusal code the coverage gate raises.
const codeCoverageIncomplete = "coverage-incomplete"

// coverageDiff changes notes.txt across one hunk on both sides, so the
// evaluator unions OLD 1-3 with NEW 1-3 and the bundle has three changed
// lines: `all` attests 3/3 and `1-2` leaves exactly `notes.txt:3` unattested.
const coverageDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// generatedDiff changes only a generated path: the evaluator counts the file
// exempt and measures nothing, so `percent` must stay null, never 0.
const generatedDiff = "diff --git a/dist/app.min.js b/dist/app.min.js\nindex 3333333..4444444 100644\n" +
	"--- a/dist/app.min.js\n+++ b/dist/app.min.js\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// coverageReview builds one one-comment review for the given path, with extra
// as the top-level fragment carrying (or omitting) the coverage[] attestation.
func coverageReview(path, extra string) string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Gate fixture body with enough length to pass.","approved":false,` +
		extra + `"comments":[{"path":"` + path + `","line":2,"side":"RIGHT",` +
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`
}

// partialCoverage attests lines 1-2 of the three-line notes.txt fixture.
func partialCoverage() string {
	return coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"1-2"}],`)
}

// coverageBundle writes the local fixture bundle — source, diff, hunk
// snapshot, review — and makes it the working directory, so every relative
// path the merge reads or writes lands in this test's temp tree.
func coverageBundle(t *testing.T, diff, review string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/" + coverageSlug
	writeFile(t, dir+"/source.json", `{"head_sha":"`+coverageHead+
		`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	writeFile(t, dir+"/full.diff", diff)
	writeFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(diff)))
	writeFile(t, dir+"/review.json", review)
	seedControls(t, dir)
}

// mergeCoverage runs the shared merge over the fixture bundle the way the
// record and post services do: the normalized review, the bundle's controls
// snapshot, and the injected runtime. It returns the merged body, the
// warnings the merge emitted, and the raw error.
func mergeCoverage(t *testing.T, require bool) (string, []string, error) {
	t.Helper()
	dir := ".verdict/state/" + coverageSlug
	review, err := recordcore.LoadReview(dir + "/review.json")
	if err != nil {
		t.Fatalf("load review: %v", err)
	}
	snap, ref := controls.Gate(dir, recordcore.BundleBase(dir, nil))
	if ref != nil {
		t.Fatalf("controls gate: %v", ref)
	}
	bodyRaw, _ := review.Get("body")
	c := &collector{}
	res, merr := recordcore.Merge(testRuntime(c), &recordcore.MergeParams{
		Dir:             dir,
		Prov:            "local",
		HeadFull:        coverageHead,
		Review:          review,
		ReviewFile:      dir + "/review.json",
		Root:            ".",
		RepoRoot:        ".",
		Body:            jq.R(jq.Alt(bodyRaw, "")),
		FindingsFile:    dir + "/findings.json",
		Effective:       core.Defaults(),
		Repo:            "acme/app",
		Pr:              coverageSlug,
		Cmd:             "record",
		Controls:        snap,
		InputChecked:    true,
		RequireCoverage: require,
	})
	return res.Body, c.warns, merr
}

// runCoverage runs the record service over the fixture bundle: the full path
// that would write the findings store, so a refused run's writes are graded.
func runCoverage(t *testing.T, require, dryRun bool) *core.RefusalError {
	t.Helper()
	c := &collector{}
	req := recordRequest(t, c, coverageSlug)
	req.Flags.RequireCoverage = require
	req.DryRun = dryRun
	_, ref := recordcore.Run(context.Background(), req)
	return ref
}

// coverageRefusal unwraps the typed refusal a gate case expects.
func coverageRefusal(t *testing.T, err error) *core.RefusalError {
	t.Helper()
	var ref *core.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("merge error = %v, want a typed refusal", err)
	}
	if ref.Code != codeCoverageIncomplete {
		t.Fatalf("refusal code = %q, want %q (message %q)", ref.Code, codeCoverageIncomplete, ref.Message)
	}
	return ref
}

// coverageWarnings keeps only the warnings the coverage surface emitted, so a
// case grades the gate's own voice instead of every warning in the run.
func coverageWarnings(warns []string) []string {
	var out []string
	for _, w := range warns {
		if strings.HasPrefix(w, "coverage:") {
			out = append(out, w)
		}
	}
	return out
}

// coverageBodyLines returns the body's machine-built coverage lines.
func coverageBodyLines(body string) []string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, coverageLinePrefix) {
			out = append(out, line)
		}
	}
	return out
}

// decodeObject decodes one written JSON artifact into its property map.
func decodeObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, raw)
	}
	return doc
}

// TestMergeCoverageGateRefusesAbsentAttestation pins criterion 1: the opt-in
// gate refuses a review with no coverage[] key at all, typed, naming the
// missing attestation rather than a bogus percentage.
func TestMergeCoverageGateRefusesAbsentAttestation(t *testing.T) {
	coverageBundle(t, coverageDiff, coverageReview("notes.txt", ""))
	_, warns, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	if !strings.Contains(ref.Message, "no coverage[] attestation") {
		t.Errorf("message = %q, want it to name the missing attestation", ref.Message)
	}
	if got := coverageWarnings(warns); len(got) != 0 {
		t.Errorf("refused merge warned %v; a refusal carries no summary write", got)
	}
}

// TestMergeCoverageGateRefusesPartialAttestation pins criterion 2's refusal
// half: a partial attestation refuses and names the unattested ranges, so the
// operator can act without another verb. Duplicate entries for one file union
// first, so a repeated claim cannot hide the range it never covered.
func TestMergeCoverageGateRefusesPartialAttestation(t *testing.T) {
	coverageBundle(t, coverageDiff, coverageReview("notes.txt",
		`"coverage":[{"file":"notes.txt","ranges":"1-2"},{"file":"notes.txt","ranges":"2"}],`))
	_, _, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	// Literal, not a re-render of the message: the basis names what the number
	// counts, so a shorter "changed lines" phrasing must fail this case.
	want := "coverage-incomplete: 66% of full hunk spans, both sides and context attested (2/3); unattested: notes.txt:3"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
}

// TestCoverageRefusalKeepsThePriorStore pins criterion 2's write half: the
// refusal is raised before any store write, so a refused run leaves the prior
// findings.json byte-identical and writes no record artifact, with and
// without --dry-run.
func TestCoverageRefusalKeepsThePriorStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{"record", false},
		{"dry-run", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusalKeepsPriorStore(t, tc.dryRun)
		})
	}
}

// TestMergeCoverageGateAcceptsComplete pins criterion 3: a 100% attestation
// merges, and neither the body line nor the summary warning fires — a
// complete review reads exactly as a legacy one.
func TestMergeCoverageGateAcceptsComplete(t *testing.T) {
	coverageBundle(t, coverageDiff, coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"}],`))
	body, warns, err := mergeCoverage(t, true)
	if err != nil {
		t.Fatalf("merge refused a complete attestation: %v", err)
	}
	if got := coverageWarnings(warns); len(got) != 0 {
		t.Errorf("complete merge warned %v, want none", got)
	}
	if got := coverageBodyLines(body); len(got) != 0 {
		t.Errorf("complete merge added %v, want no body line", got)
	}
}

// TestMergeCoveragePartialWarnsOnceAndAddsOneLine pins criterion 6: the
// default posture — no flag — warns exactly once and adds exactly one body
// line, and that line sits before the footer the operator reads last.
func TestMergeCoveragePartialWarnsOnceAndAddsOneLine(t *testing.T) {
	coverageBundle(t, coverageDiff, partialCoverage())
	body, warns, err := mergeCoverage(t, false)
	if err != nil {
		t.Fatalf("default posture refused a partial attestation: %v", err)
	}
	wantWarn := "coverage: 66% of " + coverage.Basis + " attested (2/3; 1 file(s) with gaps)"
	if got := coverageWarnings(warns); len(got) != 1 || got[0] != wantWarn {
		t.Errorf("coverage warnings = %v, want exactly [%q]", got, wantWarn)
	}
	lines := coverageBodyLines(body)
	wantLine := "> ⚠️ coverage: 66% of changed lines attested (unattested: notes.txt:3)"
	if len(lines) != 1 || lines[0] != wantLine {
		t.Errorf("coverage body lines = %v, want exactly [%q]", lines, wantLine)
	}
	lineAt := strings.Index(body, wantLine)
	footerAt := strings.Index(body, "<!-- verdict-footer -->")
	if lineAt < 0 || footerAt < 0 || lineAt > footerAt {
		t.Errorf("coverage line at %d, footer at %d: the line must precede the footer", lineAt, footerAt)
	}
}

// TestMergeCoverageAbsentChangesNothing pins criterion 7: a review with no
// coverage key is the pre-feature review — no body line, no warning, and the
// summary still reports the attestation absent.
func TestMergeCoverageAbsentChangesNothing(t *testing.T) {
	coverageBundle(t, coverageDiff, coverageReview("notes.txt", ""))
	body, warns, err := mergeCoverage(t, false)
	if err != nil {
		t.Fatalf("legacy review refused: %v", err)
	}
	if strings.Contains(body, "coverage:") {
		t.Errorf("legacy body carries a coverage line:\n%s", body)
	}
	if got := coverageWarnings(warns); len(got) != 0 {
		t.Errorf("legacy merge warned %v, want none", got)
	}
}

// TestCoverageReceiptStoredOnlyWhenAttested pins criterion 8's record half:
// record.json carries `coverage` only when the attestation was present, and
// an exempt-only bundle stores `percent: null`, never 0.
func TestCoverageReceiptStoredOnlyWhenAttested(t *testing.T) {
	cases := []struct {
		coverage map[string]any
		name     string
		diff     string
		review   string
		wantKey  bool
	}{
		{
			name:    "legacy-absent",
			diff:    coverageDiff,
			review:  coverageReview("notes.txt", ""),
			wantKey: false,
		},
		{
			name:    "partial-present",
			diff:    coverageDiff,
			review:  partialCoverage(),
			wantKey: true,
			coverage: map[string]any{
				"percent": 66.0, "attested": 2.0, "total": 3.0, "files": 1.0, "exempt": 0.0, "gaps": 1.0,
			},
		},
		{
			name:    "generated-only-null",
			diff:    generatedDiff,
			review:  coverageReview("dist/app.min.js", `"coverage":[{"file":"dist/app.min.js","ranges":"all"}],`),
			wantKey: true,
			coverage: map[string]any{
				"percent": nil, "attested": 0.0, "total": 0.0, "files": 0.0, "exempt": 1.0, "gaps": 0.0,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			coverageBundle(t, tc.diff, tc.review)
			if ref := runCoverage(t, false, false); ref != nil {
				t.Fatalf("record refused: %v", ref)
			}
			record := decodeObject(t, ".verdict/state/"+coverageSlug+"/record.json")
			assertStoredCoverage(t, record, tc.wantKey, tc.coverage)
		})
	}
}

// postCoverageBundle writes the provider bundle post merges: PR.json states
// the head/base pair the run state binds, and the diff/review pair is the
// same coverage fixture record uses.
func postCoverageBundle(t *testing.T, review string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/7"
	writeFile(t, dir+"/PR.json", `{"state":"OPEN","headRefOid":"`+coverageHead+
		`","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"someone-else"}}`)
	writeFile(t, dir+"/full.diff", coverageDiff)
	writeFile(t, dir+"/review.json", review)
	seedControls(t, dir)
}

// postCommanderStub answers the four gh calls a post run makes, routed by the
// argv substring that identifies each: no case depends on the post package's
// private filter string.
type postCommanderStub struct {
	t *testing.T
}

// Run answers one scripted gh call; an unrecognized argv fails the test
// instead of silently reaching a real forge.
func (c *postCommanderStub) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Helper()
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "/reviews"):
		return core.Result{Stdout: `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101",` +
			`"submitted_at":"2026-01-01T00:00:00Z"}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	case strings.Contains(argv, ".head.sha"):
		return core.Result{Stdout: coverageHead + "\n"}, nil
	case strings.Contains(argv, "user"):
		return core.Result{Stdout: "someone-else\n"}, nil
	default:
		c.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{ExitCode: 1}, errors.New("unexpected argv")
	}
}

// postRequest builds the post request the adapter would pass the service, over
// the scripted commander and this test's clock.
func postRequest(t *testing.T, flags core.PostFlags) *core.PostRequest {
	t.Helper()
	root := ""
	return &core.PostRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fixedNow },
			Progress:  &collector{},
			Commander: &postCommanderStub{t: t},
		},
		Repo:      "acme/app",
		Pr:        "7",
		Root:      &root,
		Flags:     flags,
		Effective: core.Defaults(),
		JSONMode:  true,
	}
}

// TestPostCoverageGateIgnoresForce pins criterion 5: post's --force bypasses
// the binding gates, never the coverage refusal — the gate is raised in the
// shared merge, so force has nothing to skip.
func TestPostCoverageGateIgnoresForce(t *testing.T) {
	postCoverageBundle(t, partialCoverage())
	_, ref := postcore.Run(context.Background(), postRequest(t, core.PostFlags{
		Approve:         "false",
		ReviewFile:      ".verdict/state/7/review.json",
		Force:           true,
		RequireCoverage: true,
		DryRun:          true,
	}))
	if ref == nil || ref.Code != codeCoverageIncomplete {
		t.Fatalf("refusal = %v, want %q on a forced run", ref, codeCoverageIncomplete)
	}
	if !strings.Contains(ref.Message, "notes.txt:3") {
		t.Errorf("message = %q, want it to name the unattested range", ref.Message)
	}
}

// TestPostReceiptCarriesCoverageOnlyWhenAttested pins criterion 8's post half:
// post.json carries `coverage` only when the attestation was present, and the
// stored receipt is the one the merge measured.
func TestPostReceiptCarriesCoverageOnlyWhenAttested(t *testing.T) {
	cases := []struct {
		name    string
		review  string
		wantKey bool
	}{
		{name: "legacy-absent", review: coverageReview("notes.txt", ""), wantKey: false},
		{name: "partial-present", review: partialCoverage(), wantKey: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			postCoverageBundle(t, tc.review)
			_, ref := postcore.Run(context.Background(), postRequest(t, core.PostFlags{
				Approve:    "false",
				ReviewFile: ".verdict/state/7/review.json",
			}))
			if ref != nil {
				t.Fatalf("post refused: %v", ref)
			}
			assertStoredCoverage(t, decodeObject(t, ".verdict/state/7/post.json"), tc.wantKey,
				map[string]any{"percent": 66.0, "attested": 2.0, "total": 3.0})
		})
	}
}

// assertRefusalKeepsPriorStore runs one refused coverage gate and proves the
// store was not touched: the prior findings.json survives byte for byte and no
// record.json appears.
func assertRefusalKeepsPriorStore(t *testing.T, dryRun bool) {
	t.Helper()
	coverageBundle(t, coverageDiff, partialCoverage())
	prior := "[]\n"
	writeFile(t, ".verdict/state/"+coverageSlug+"/findings.json", prior)
	if ref := runCoverage(t, true, dryRun); ref == nil || ref.Code != codeCoverageIncomplete {
		t.Fatalf("refusal = %v, want %q", ref, codeCoverageIncomplete)
	}
	after, err := os.ReadFile(".verdict/state/" + coverageSlug + "/findings.json")
	if err != nil {
		t.Fatalf("read prior findings: %v", err)
	}
	if string(after) != prior {
		t.Errorf("prior findings.json changed:\n%s", after)
	}
	if _, err := os.Stat(".verdict/state/" + coverageSlug + "/record.json"); !os.IsNotExist(err) {
		t.Error("a refused run wrote record.json")
	}
}

// assertStoredCoverage checks the stored coverage object for presence and, when
// present, every expected field. want == nil accepts the object as stored.
func assertStoredCoverage(t *testing.T, stored map[string]any, wantKey bool, want map[string]any) {
	t.Helper()
	cov, present := stored["coverage"]
	if present != wantKey {
		t.Fatalf("coverage present = %v, want %v: %v", present, wantKey, stored)
	}
	if !wantKey {
		return
	}
	got, ok := cov.(map[string]any)
	if !ok {
		t.Fatalf("coverage = %T, want an object", cov)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("coverage.%s = %v (%T), want %v", key, got[key], got[key], value)
		}
	}
}
