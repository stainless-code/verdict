package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// lensPlan is a two-group plan with one capped group and one uncapped
// group. Ids are literals so a production change to the identity hashing
// cannot silently satisfy these cases.
func lensPlan() *Plan {
	two := 2
	return &Plan{Groups: []PlanGroup{
		{ID: "group-a", Label: "alpha", MaxLenses: &two},
		{ID: "group-c", Label: "gamma"},
	}}
}

// sharedPlan caps two groups so one label serving both can be counted
// twice without either group inheriting the other's work.
func sharedPlan() *Plan {
	one, two := 1, 2
	return &Plan{Groups: []PlanGroup{
		{ID: "group-a", Label: "alpha", MaxLenses: &one},
		{ID: "group-b", Label: "beta", MaxLenses: &two},
	}}
}

// mixedPlan pairs a working group with a zero-cap group, the shape a flat
// dispatch list could not represent: known work in A and explicit zero in B.
func mixedPlan() *Plan {
	two, zero := 2, 0
	return &Plan{Groups: []PlanGroup{
		{ID: "group-a", Label: "alpha", MaxLenses: &two},
		{ID: "group-b", Label: "beta", MaxLenses: &zero},
	}}
}

// entry is one group attestation: labels may be empty for an explicit zero.
func entry(group string, labels ...string) core.LensUsage {
	if labels == nil {
		labels = []string{}
	}
	return core.LensUsage{Group: group, Labels: labels}
}

// lensCase is one evaluation expectation: literal counts, known flags, and
// violation reasons in report order, never derived from the subject.
type lensCase struct {
	name        string
	plan        func() *Plan
	state       string
	entries     []core.LensUsage
	wantGroups  []int
	wantKnown   []bool
	wantReason  []string
	wantUnknown []string
}

// TestEvaluateLensUsage pins the evaluation rules per case: counts, known
// flags, violation reasons, and unknown groups.
func TestEvaluateLensUsage(t *testing.T) {
	for _, tt := range evaluateCases() {
		t.Run(tt.name, func(t *testing.T) {
			plan := lensPlan()
			if tt.plan != nil {
				plan = tt.plan()
			}
			evaluated := EvaluateLensUsage(plan, tt.state, tt.entries)
			assertLensCase(t, &evaluated, &tt)
		})
	}
}

// evaluateCases are the evaluation expectations, one per semantic rule.
func evaluateCases() []lensCase {
	return []lensCase{
		{
			name: "within cap", state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness", "security")},
			wantGroups: []int{2, 0}, wantKnown: []bool{true, false},
		},
		{
			name: "over cap", state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness", "security", "docs")},
			wantGroups: []int{3, 0}, wantKnown: []bool{true, false},
			wantReason: []string{LensOverCap},
		},
		{
			name: "repeated label counts once", state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness", "correctness")},
			wantGroups: []int{1, 0}, wantKnown: []bool{true, false},
		},
		{
			name: "mixed work and explicit zero", plan: mixedPlan, state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness", "security"), entry("group-b")},
			wantGroups: []int{2, 0}, wantKnown: []bool{true, true},
		},
		{
			name: "explicit empty array attests zero for every group", plan: mixedPlan, state: LensUsageRecorded,
			wantGroups: []int{0, 0}, wantKnown: []bool{true, true},
		},
		{
			name: "absent usage is unknown not zero", plan: mixedPlan, state: LensUsageAbsent,
			wantGroups: []int{0, 0}, wantKnown: []bool{false, false},
			wantReason: []string{LensUsageUnknown, LensUsageUnknown},
		},
		{
			name: "unreadable usage is unknown not zero", plan: mixedPlan, state: LensUsageMalformed,
			wantGroups: []int{0, 0}, wantKnown: []bool{false, false},
			wantReason: []string{LensUsageUnknown, LensUsageUnknown},
		},
		{
			name: "unbound record is unknown not zero", plan: mixedPlan, state: LensUsageUnbound,
			wantGroups: []int{0, 0}, wantKnown: []bool{false, false},
			wantReason: []string{LensUsageUnknown, LensUsageUnknown},
		},
		{
			name: "group A entry cannot satisfy capped group B", plan: sharedPlan, state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness")},
			wantGroups: []int{1, 0}, wantKnown: []bool{true, false},
			wantReason: []string{LensUsageUnknown},
		},
		{
			name: "a shared label counts against each group", plan: sharedPlan, state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "review"), entry("group-b", "review", "security")},
			wantGroups: []int{1, 2}, wantKnown: []bool{true, true},
		},
		{
			name: "unknown group satisfies no capped group", plan: mixedPlan, state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-zzz", "correctness")},
			wantGroups: []int{0, 0}, wantKnown: []bool{false, false},
			wantReason: []string{LensUsageUnknown, LensUsageUnknown, LensUnbound}, wantUnknown: []string{"group-zzz"},
		},
		{
			name: "duplicate group entry is ambiguous", plan: mixedPlan, state: LensUsageRecorded,
			entries:    []core.LensUsage{entry("group-a", "correctness"), entry("group-a", "security")},
			wantGroups: []int{1, 0}, wantKnown: []bool{false, false},
			wantReason: []string{LensAmbiguous, LensUsageUnknown},
		},
	}
}

// assertLensCase compares one evaluation result to its literal expectation:
// per-group counts, per-group known flags, the violation reasons in order,
// and the unknown-group list length.
func assertLensCase(t *testing.T, usage *LensUsage, want *lensCase) {
	t.Helper()
	assertLensCounts(t, usage.Groups, want.wantGroups, want.wantKnown)
	assertLensReasons(t, usage.Violations(), want.wantReason)
	if got, expected := len(usage.Unknown), len(want.wantUnknown); got != expected {
		t.Fatalf("unknown = %v, want %v", usage.Unknown, want.wantUnknown)
	}
}

// assertLensCounts compares the per-group unique-label counts and known
// flags against the literals the case declares.
func assertLensCounts(t *testing.T, groups []LensGroupUsage, wantCounts []int, wantKnown []bool) {
	t.Helper()
	if len(groups) != len(wantCounts) {
		t.Fatalf("groups = %d, want %d", len(groups), len(wantCounts))
	}
	for i, count := range wantCounts {
		if got := groups[i].Count; got != count {
			t.Errorf("group %d count = %d, want %d", i, got, count)
		}
	}
	for i, known := range wantKnown {
		if got := groups[i].Known; got != known {
			t.Errorf("group %d known = %v, want %v", i, got, known)
		}
	}
}

// assertLensReasons compares the violation reasons in report order.
func assertLensReasons(t *testing.T, violations []LensViolation, want []string) {
	t.Helper()
	reasons := make([]string, 0, len(violations))
	for _, violation := range violations {
		reasons = append(reasons, violation.Reason)
	}
	if len(reasons) != len(want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
	for i := range reasons {
		if reasons[i] != want[i] {
			t.Errorf("reason %d = %q, want %q", i, reasons[i], want[i])
		}
	}
}

// The capture every reader fixture attests: a record must name this run and
// this inputs hash before it can certify lens work.
const (
	lensFixtureRun    = "7"
	lensFixtureInputs = "f00dfeed"
)

// lensRunState is the verified bundle the reader fixtures evaluate against.
func lensRunState() *RunState {
	return &RunState{
		Name: lensFixtureRun, InputsSHA256: lensFixtureInputs,
		Resolved: &Resolved{Plan: *mixedPlan()},
	}
}

// costBody renders one schema-v2 cost record bound to the fixture capture
// around the given lenses fragment.
func costBody(lenses string) string {
	return `{"schema_version":"2","effort":"standard","run":"` + lensFixtureRun +
		`","inputs_sha256":"` + lensFixtureInputs + `","lenses":` + lenses + `}`
}

// TestReadLensUsageDistinguishesPresence pins the wire reading: absence,
// null, a legacy string entry, a missing labels list, an explicit empty
// list, a foreign capture, and strict-JSON violations stay distinct, so a
// capped group is never certified from bytes it did not receive.
func TestReadLensUsageDistinguishesPresence(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		state string
	}{
		{name: "no file", state: LensUsageAbsent},
		{name: "no lenses key", body: `{"schema_version":"2","run":"7","inputs_sha256":"f00dfeed"}`, state: LensUsageAbsent},
		{name: "null lenses", body: costBody("null"), state: LensUsageAbsent},
		{name: "legacy strings", body: costBody(`["correctness"]`), state: LensUsageMalformed},
		{name: "missing labels", body: costBody(`[{"group":"group-a"}]`), state: LensUsageMalformed},
		{name: "null labels", body: costBody(`[{"group":"group-a","labels":null}]`), state: LensUsageMalformed},
		{name: "empty array", body: costBody(`[]`), state: LensUsageRecorded},
		{name: "grouped", body: costBody(`[{"group":"group-a","labels":["x"]}]`), state: LensUsageRecorded},
		{name: "explicit zero group", body: costBody(`[{"group":"group-b","labels":[]}]`), state: LensUsageRecorded},
		{name: "record array", body: `[` + costBody(`[{"group":"group-a","labels":["x"]}]`) + `]`, state: LensUsageRecorded},
		{name: "no binding", body: `{"schema_version":"2","lenses":[{"group":"group-a","labels":["x"]}]}`, state: LensUsageUnbound},
		{name: "foreign run", body: `{"schema_version":"2","run":"8","inputs_sha256":"f00dfeed","lenses":[]}`, state: LensUsageUnbound},
		{name: "foreign inputs", body: `{"schema_version":"2","run":"7","inputs_sha256":"beef","lenses":[]}`, state: LensUsageUnbound},
		{name: "version one", body: `{"schema_version":"1","run":"7","inputs_sha256":"f00dfeed","lenses":[]}`, state: LensUsageUnbound},
		{name: "duplicate keys", body: `{"schema_version":"2","run":"7","run":"7","inputs_sha256":"f00dfeed","lenses":[]}`, state: LensUsageMalformed},
		{name: "trailing document", body: costBody(`[]`) + `{"schema_version":"2"}`, state: LensUsageMalformed},
		{name: "unknown entry field", body: costBody(`[{"group":"group-a","labels":["x"],"note":"ok"}]`), state: LensUsageMalformed},
		{name: "unknown record field", body: `{"schema_version":"2","run":"7","inputs_sha256":"f00dfeed","sneaky":true,"lenses":[]}`, state: LensUsageMalformed},
		{name: "history picks the last bound record", body: `[` + costBody(`[{"group":"group-a","labels":["x","y"]}]`) + `,` + costBody(`[{"group":"group-a","labels":["x"]}]`) + `]`, state: LensUsageRecorded},
		{name: "incomplete last record does not fall back", body: `[` + costBody(`[{"group":"group-a","labels":["x"]}]`) + `,{"schema_version":"2","run":"7","inputs_sha256":"f00dfeed"}]`, state: LensUsageAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReaderState(t, costDir(t, tt.body), tt.state)
		})
	}
}

// costDir builds a bundle directory holding body as cost.json, skipping the
// write when the case has no file.
func costDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body == "" {
		return dir
	}
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertReaderState reads dir through the public reader and compares the
// reported presence state.
func assertReaderState(t *testing.T, dir, state string) {
	t.Helper()
	if usage := ReadLensUsage(lensRunState(), dir); usage.State != state {
		t.Fatalf("state = %q, want %q", usage.State, state)
	}
}

// TestMalformedRecordCannotCertifyCappedWork pins the strict-input rule: a
// valid capped-group entry followed by an unreadable entry is not a whole
// attestation, so the record is malformed and the cap stays unknown. Retained
// partial entries are diagnostics; they never become passing known use.
func TestMalformedRecordCannotCertifyCappedWork(t *testing.T) {
	dir := costDir(t, costBody(`[{"group":"group-a","labels":["x"]},{"group":"group-b"}]`))
	usage := ReadLensUsage(lensRunState(), dir)
	if usage.State != LensUsageMalformed {
		t.Fatalf("state = %q, want malformed", usage.State)
	}
	if usage.Groups[0].Known {
		t.Fatal("a malformed record certified group-a")
	}
	for _, violation := range usage.Violations() {
		if violation.GroupID == "group-a" && violation.Reason != LensUsageUnknown {
			t.Fatalf("group-a reason = %q, want %q", violation.Reason, LensUsageUnknown)
		}
	}
	retained := EvaluateLensUsage(mixedPlan(), LensUsageMalformed, []core.LensUsage{entry("group-a", "x")})
	if retained.Groups[0].Known {
		t.Fatal("a retained partial entry certified group-a outside the reader")
	}
}

// TestReadLensUsageCountsOnlyTheBoundRecord pins the history boundary
// end to end: the reader, the evaluation, and the violation surface agree
// that only the record bound to this capture may certify, and that a
// foreign capture leaves a capped group unknown rather than zero.
func TestReadLensUsageCountsOnlyTheBoundRecord(t *testing.T) {
	dir := t.TempDir()
	body := `[` + costBody(`[{"group":"group-a","labels":["x","y","z"]}]`) + `,` +
		costBody(`[{"group":"group-a","labels":["x"]},{"group":"group-b","labels":[]}]`) + `]`
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	usage := ReadLensUsage(lensRunState(), dir)
	if usage.State != LensUsageRecorded {
		t.Fatalf("state = %q, want recorded", usage.State)
	}
	if got := usage.Groups[0].Count; got != 1 {
		t.Fatalf("group-a count = %d, want 1 (last bound record only)", got)
	}
	if got := usage.Groups[1].Known; !got {
		t.Fatal("group-b explicit zero lost its known attestation")
	}
	if violations := usage.Violations(); len(violations) != 0 {
		t.Fatalf("violations = %v, want none within cap", violations)
	}
}

// TestReadLensUsageRefusesForeignCapture pins the approval gate's input
// side: a record from another capture leaves every capped group unknown, so
// no violation can ever be reported as within cap from foreign bytes.
func TestReadLensUsageRefusesForeignCapture(t *testing.T) {
	dir := t.TempDir()
	body := `{"schema_version":"2","run":"8","inputs_sha256":"f00dfeed","lenses":[]}`
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	usage := ReadLensUsage(lensRunState(), dir)
	if usage.State != LensUsageUnbound {
		t.Fatalf("state = %q, want unbound", usage.State)
	}
	violations := usage.Violations()
	if len(violations) != 2 {
		t.Fatalf("violations = %v, want one per capped group", violations)
	}
	for _, violation := range violations {
		if violation.Reason != LensUsageUnknown {
			t.Fatalf("reason = %q, want %q", violation.Reason, LensUsageUnknown)
		}
	}
}

// TestCostRecordEncodesGroupedLenses pins the published wire shape the
// agent must write and the schema publishes, including an explicit zero
// group beside a working one.
func TestCostRecordEncodesGroupedLenses(t *testing.T) {
	raw, err := json.Marshal(core.CostRecord{
		SchemaVersion: "2",
		Lenses: []core.LensUsage{
			{Group: "group-a", Labels: []string{"correctness"}},
			{Group: "group-b", Labels: []string{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":"2","lenses":[{"group":"group-a","labels":["correctness"]},{"group":"group-b","labels":[]}]}`
	if string(raw) != want {
		t.Fatalf("cost record = %s, want %s", raw, want)
	}
}

// TestReadLensUsageOpenFailureStaysUnreadable distinguishes failed reads from
// a missing record without relying on the process's filesystem permissions.
func TestReadLensUsageOpenFailureStaysUnreadable(t *testing.T) {
	assertReaderState(t, filepath.Join(t.TempDir(), "invalid\x00path"), LensUsageMalformed)
}
