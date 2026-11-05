package recordcore_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

const normalizeBody = "> ✅ marker plus enough length to pass the guard ok."

func pretty(t *testing.T, raw string) string {
	t.Helper()
	doc, err := jq.DecodeOrdered([]byte(raw))
	if err != nil {
		t.Fatalf("decode expected %s: %v", raw, err)
	}
	return jq.PrettyOrdered(doc)
}

func repairCodes(problems []recordcore.Problem) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Code)
	}
	return out
}

// repairCase is one row of the repair table: the input bytes, the
// normalized bytes, and the warning codes the caller receives.
type repairCase struct {
	name  string
	in    string
	want  string
	codes []string
}

// repairCases pins every repair code: the input bytes, the normalized bytes,
// and the warning the caller receives.
func repairCases() []repairCase {
	base := `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[]}`
	return []repairCase{
		{
			name:  "fence",
			in:    "```json\n" + base + "\n```",
			want:  base,
			codes: []string{core.ProblemCodeNormalizedFence},
		},
		{
			name:  "fence-upper",
			in:    "```JSON\n" + base + "\n```",
			want:  base,
			codes: []string{core.ProblemCodeNormalizedFence},
		},
		{
			name:  "extracted",
			in:    "Here is the review:\n" + base + "\nLet me know",
			want:  base,
			codes: []string{core.ProblemCodeNormalizedExtracted},
		},
		{
			name: "scalar-to-array",
			in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[],` +
				`"resolved":"a1b2","chunks":"a"}`,
			want: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[],` +
				`"resolved":["a1b2"],"chunks":["a"]}`,
			codes: []string{core.ProblemCodeScalarToArray, core.ProblemCodeScalarToArray},
		},
		{
			name: "key-alias",
			in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"file":"notes.txt","line":2,"body":"A finding with enough substance.","rung":"walked","inBounds":true}]}`,
			want: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rigor":"walked","in-bounds":true}]}`,
			codes: []string{core.ProblemCodeKeyAlias, core.ProblemCodeKeyAlias, core.ProblemCodeKeyAlias},
		},
		{
			name: "enum-synonym",
			in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rigor":"traced","severity":"Critical"}]}`,
			want: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rigor":"walked","severity":"critical"}]}`,
			codes: []string{core.ProblemCodeEnumSynonym, core.ProblemCodeEnumSynonym},
		},
		{
			name: "alias-null",
			in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rung":null,"inBounds":null}]}`,
			want: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance."}]}`,
			codes: []string{core.ProblemCodeNormalizedTrivia, core.ProblemCodeNormalizedTrivia},
		},
		{
			name: "trivia",
			in: "\ufeff" + `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","severity":null},` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance."}]}`,
			want: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"A finding with enough substance."}]}`,
			codes: []string{core.ProblemCodeNormalizedTrivia, core.ProblemCodeNormalizedTrivia, core.ProblemCodeNormalizedTrivia},
		},
	}
}

// TestNormalizeReviewRepairs runs the repair table: input normalizes to the
// pinned bytes with one warning per rewrite, and normalizing twice is stable.
func TestNormalizeReviewRepairs(t *testing.T) {
	t.Parallel()
	for _, tc := range repairCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRepairCase(t, tc)
		})
	}
}

// checkRepairCase proves one repair input normalizes, warns, and stays fixed.
func checkRepairCase(t *testing.T, tc repairCase) {
	t.Helper()
	got, repairs := recordcore.NormalizeReview([]byte(tc.in))
	if string(got) != pretty(t, tc.want) {
		t.Errorf("normalized = %s, want %s", got, pretty(t, tc.want))
	}
	if codes := repairCodes(repairs); strings.Join(codes, ",") != strings.Join(tc.codes, ",") {
		t.Errorf("repair codes = %v, want %v", codes, tc.codes)
	}
	checkRepairWarnings(t, repairs)
	again, againRepairs := recordcore.NormalizeReview(got)
	if !bytes.Equal(again, got) || len(againRepairs) != 0 {
		t.Errorf("normalize is not idempotent: %s -> %s (%v)", got, again, againRepairs)
	}
}

// checkRepairWarnings proves every repair is a complete warning.
func checkRepairWarnings(t *testing.T, repairs []recordcore.Problem) {
	t.Helper()
	for i := range repairs {
		if !repairComplete(&repairs[i]) {
			t.Errorf("repair is not a complete warning: %+v", repairs[i])
		}
	}
}

// repairComplete reports whether a repair carries the full warning shape.
func repairComplete(p *recordcore.Problem) bool {
	return p.Level == core.ProblemLevelWarning && p.Message != "" && p.Fix != "" && p.Path != ""
}

// neverCase is one row of the never-repaired table: an input NormalizeReview
// must return untouched, and the checker code it still reports.
type neverCase struct {
	name string
	in   string
	code string
}

// neverCases pins the shapes that stay errors.
func neverCases() []neverCase {
	return []neverCase{
		{name: "unparsable", in: `{broken`, code: core.ProblemCodeInvalidJSON},
		{name: "non-object", in: `[1,2]`, code: core.ProblemCodeWrongType},
		{name: "numeric-path", in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[{"path":7,"line":2,"body":"A finding with enough substance."}]}`, code: core.ProblemCodeWrongType},
		{name: "missing-body", in: `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[{"path":"notes.txt","line":2}]}`, code: core.ProblemCodeMissingField},
		{name: "unknown-schema", in: `{"schema_version":"2","body":"` + normalizeBody + `","approved":false,"comments":[]}`, code: core.ProblemCodeBadEnum},
	}
}

// TestNormalizeReviewNeverRepairs runs the never-repaired table.
func TestNormalizeReviewNeverRepairs(t *testing.T) {
	t.Parallel()
	for _, tc := range neverCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkNeverRepaired(t, tc)
		})
	}
}

// checkNeverRepaired proves one unrecoverable input passes through untouched
// and still reports its checker error.
func checkNeverRepaired(t *testing.T, tc neverCase) {
	t.Helper()
	got, repairs := recordcore.NormalizeReview([]byte(tc.in))
	if string(got) != tc.in || len(repairs) != 0 {
		t.Errorf("normalize changed a never-repaired input: %s -> %s (%v)", tc.in, got, repairs)
	}
	res := recordcore.CheckReview(got, checkDiff)
	found := false
	for _, p := range res.Problems {
		if p.Code == tc.code {
			found = true
		}
	}
	if !found {
		t.Errorf("problems = %+v, want %s", res.Problems, tc.code)
	}
}

// TestNormalizeReviewPartialChunksStayErrors proves chunk coverage is not a
// repair: the normalized document still reaches the checker's error.
func TestNormalizeReviewPartialChunksStayErrors(t *testing.T) {
	t.Parallel()
	in := checkReview(`,"chunks":["a"]`)
	got, repairs := recordcore.NormalizeReview([]byte(in))
	if len(repairs) != 0 {
		t.Fatalf("repairs = %+v, want none", repairs)
	}
	res := recordcore.CheckReviewInput(got, recordcore.CheckInput{
		Diff: checkDiff, ChunkNames: []string{"a", "b"}, ChunkManifest: "manifest.json",
	})
	if res.Errors == 0 {
		t.Fatalf("partial chunks did not stay an error: %+v", res.Problems)
	}
}

// TestNormalizeReviewIdempotentProperty proves normalize(normalize(x)) is
// byte-identical for a spread of repairable and unrepairable documents.
func TestNormalizeReviewIdempotentProperty(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"```json\n" + checkReview("") + "\n```",
		"prose\n" + checkReview("") + "\nmore prose",
		"\ufeff" + checkReview(""),
		`{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[{"file":"notes.txt","line":2,"body":"A finding with enough substance.","rung":"traced","severity":"Critical","suggestion":null}]}`,
		`{broken`,
		`[1,2]`,
	}
	for _, in := range inputs {
		once, _ := recordcore.NormalizeReview([]byte(in))
		twice, repairs := recordcore.NormalizeReview(once)
		if !bytes.Equal(once, twice) || len(repairs) != 0 {
			t.Errorf("not idempotent for %q: %q -> %q (%v)", in, once, twice, repairs)
		}
	}
}

// TestRecordNormalizesWeakModelShapes proves the record path applies the
// repairs, reports them, and records the default rigor provenance.
func TestRecordNormalizesWeakModelShapes(t *testing.T) {
	review := "```json\n" + `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,` +
		`"resolved":"a1b2","comments":[{"file":"notes.txt","line":2,"body":"A finding with enough substance.",` +
		`"rung":"traced","severity":"Critical","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"Why is this safe?"}}]}` + "\n```"
	bundle(t, "local-norm", review)
	c := &collector{}
	req := recordRequest(t, c, "local-norm")
	req.DryRun = true
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry run refusal: %v", ref)
	}
	checkDryRunRepairs(t, resp)
	req.DryRun = false
	if _, ref := recordcore.Run(context.Background(), req); ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
	checkRecordedRepairs(t)
}

// checkDryRunRepairs proves the dry run carries the normalized object and
// one repair per applied rewrite.
func checkDryRunRepairs(t *testing.T, resp *core.RecordResponse) {
	t.Helper()
	if resp.Data.Normalized == nil || len(resp.Data.Repairs) == 0 {
		t.Fatalf("dry run data = %+v, want normalized and repairs", resp.Data)
	}
	codes := map[string]bool{}
	for _, repair := range resp.Data.Repairs {
		codes[repair.Code] = true
	}
	for _, code := range []string{"normalized-fence", "scalar-to-array", "key-alias", "enum-synonym"} {
		if !codes[code] {
			t.Errorf("repairs = %+v, want %s", resp.Data.Repairs, code)
		}
	}
}

// checkRecordedRepairs proves the record wrote model rigor provenance and
// the repairs list.
func checkRecordedRepairs(t *testing.T) {
	t.Helper()
	raw, err := readFile(".review/local-norm/findings.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"rigor_source": "model"`) {
		t.Errorf("findings.json lacks model rigor provenance:\n%s", raw)
	}
	recordRaw, err := readFile(".review/local-norm/record.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recordRaw, `"repairs"`) || !strings.Contains(recordRaw, `"normalized-fence"`) {
		t.Errorf("record.json lacks repairs:\n%s", recordRaw)
	}
}

// TestRecordStrictRefusesRepairs proves strict input promotes every repair to
// an error and refuses before writing.
func TestRecordStrictRefusesRepairs(t *testing.T) {
	review := "```json\n" + `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,` +
		`"comments":[{"file":"notes.txt","line":2,"body":"A finding with enough substance.","rung":"traced"}]}` + "\n```"
	bundle(t, "local-norm-strict", review)
	c := &collector{}
	req := recordRequest(t, c, "local-norm-strict")
	req.DryRun = true
	req.Strict = true
	_, ref := recordcore.Run(context.Background(), req)
	if ref == nil || ref.Code != "bad-review-input" {
		t.Fatalf("strict refusal = %+v, want bad-review-input", ref)
	}
	found := false
	for _, p := range ref.Problems {
		if p.Code == core.ProblemCodeNormalizedFence && p.Level == core.ProblemLevelError {
			found = true
		}
	}
	if !found {
		t.Errorf("strict problems = %+v, want normalized-fence as an error", ref.Problems)
	}
}

// strictCase is one repair input that must record by default and refuse
// once strict input promotes its warning to an error.
type strictCase struct {
	name string
	in   string
	code string
}

// strictCases covers every repair family with a strict refusal.
func strictCases() []strictCase {
	base := `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[]}`
	comment := func(frag string) string {
		return `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` + frag + `]}`
	}
	return []strictCase{
		{"fence", "```json\n" + base + "\n```", core.ProblemCodeNormalizedFence},
		{"extracted", "Here is the review:\n" + base + "\nThanks", core.ProblemCodeNormalizedExtracted},
		{"scalar-to-array", `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[],"resolved":"a1b2"}`, core.ProblemCodeScalarToArray},
		{"key-alias", comment(`{"file":"notes.txt","line":2,"body":"A finding with enough substance.","rung":"walked"}`), core.ProblemCodeKeyAlias},
		{"enum-synonym", comment(`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rigor":"traced"}`), core.ProblemCodeEnumSynonym},
		{"trivia", "\ufeff" + comment(`{"path":"notes.txt","line":2,"body":"A finding with enough substance.","rigor":"walked","severity":null}`), core.ProblemCodeNormalizedTrivia},
	}
}

// TestRecordStrictRefusesEachRepair proves every repair family, clean under
// default input, refuses once strict input promotes its warning to an error.
func TestRecordStrictRefusesEachRepair(t *testing.T) {
	for _, tc := range strictCases() {
		t.Run(tc.name, func(t *testing.T) {
			checkStrictCase(t, tc)
		})
	}
}

// checkStrictCase proves one repair input records by default and refuses
// under strict input with its repair promoted to an error.
func checkStrictCase(t *testing.T, tc strictCase) {
	t.Helper()
	slug := "local-strict-" + tc.name
	bundle(t, slug, tc.in)
	c := &collector{}
	req := recordRequest(t, c, slug)
	req.DryRun = true
	if _, ref := recordcore.Run(context.Background(), req); ref != nil {
		t.Fatalf("default run refused: %v", ref)
	}
	cs := &collector{}
	sreq := recordRequest(t, cs, slug)
	sreq.DryRun = true
	sreq.Strict = true
	_, ref := recordcore.Run(context.Background(), sreq)
	if ref == nil || ref.Code != "bad-review-input" {
		t.Fatalf("strict refusal = %+v, want bad-review-input", ref)
	}
	if !strictPromoted(ref.Problems, tc.code) {
		t.Errorf("strict problems = %+v, want %s as an error", ref.Problems, tc.code)
	}
}

// strictPromoted reports whether the repair code refused as an error.
func strictPromoted(problems []recordcore.Problem, code string) bool {
	for _, p := range problems {
		if p.Code == code && p.Level == core.ProblemLevelError {
			return true
		}
	}
	return false
}

func readFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	return string(raw), err
}
