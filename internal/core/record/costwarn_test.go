package recordcore_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The literal warning a nameless models[] entry carries on every surface:
// stderr, the envelope warnings[], and the progress collector.
const namelessCostWarning = `cost.json: models[0] has no name — the name is what attribution reads; add "name"`

// TestRecordWarnsNamelessCostEntry proves the repro records, stays priced, and
// warns exactly once with the literal text.
func TestRecordWarnsNamelessCostEntry(t *testing.T) {
	bundle(t, "local-costwarn", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costwarn/cost.json",
		`{"schema_version":"2","run":"dog","inputs_sha256":"deadbeef","models":[{"model":"claude-x","input_tokens":1200,"output_tokens":300}]}`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costwarn"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if got := countCostWarnings(resp.Warnings, namelessCostWarning); got != 1 {
		t.Errorf("warnings = %q, want exactly one %q", resp.Warnings, namelessCostWarning)
	}
	if !slices.Contains(c.warns, namelessCostWarning) {
		t.Errorf("collector warns = %q, want %q", c.warns, namelessCostWarning)
	}
	if _, err := os.ReadFile(".verdict/state/local-costwarn/record.json"); err != nil {
		t.Errorf("record.json: %v, want the record written", err)
	}
	rollup := readcommon.CostRollup(".verdict/state/local-costwarn")
	if in, _ := readcommon.NumVal(mustCostField(t, rollup, "input_tokens")); in != 1200 {
		t.Errorf("input_tokens = %v, want 1200", in)
	}
	if out, _ := readcommon.NumVal(mustCostField(t, rollup, "output_tokens")); out != 300 {
		t.Errorf("output_tokens = %v, want 300", out)
	}
}

// TestRecordWarnsNonStringCostName proves a name that is not a string still
// warns: the schema requires a name and only a string satisfies it.
func TestRecordWarnsNonStringCostName(t *testing.T) {
	bundle(t, "local-costnum", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costnum/cost.json",
		`{"schema_version":"2","models":[{"model":"a","input_tokens":1,"name":5}]}`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costnum"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if got := countCostWarnings(resp.Warnings, namelessCostWarning); got != 1 {
		t.Errorf("warnings = %q, want exactly one %q", resp.Warnings, namelessCostWarning)
	}
}

// TestRecordSilentOnEmptyStringCostName proves an empty-string name does not
// warn: the schema sets no minimum length, so only a missing or non-string
// name warns.
func TestRecordSilentOnEmptyStringCostName(t *testing.T) {
	bundle(t, "local-costempty", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costempty/cost.json",
		`{"schema_version":"2","models":[{"model":"a","input_tokens":1,"name":""}]}`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costempty"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "has no name") {
			t.Errorf("warnings = %q, want no nameless warning for an empty-string name", resp.Warnings)
			break
		}
	}
}

// TestRecordWarnsEachRecordOfMultiRecordCostFile proves a document holding two
// records qualifies each warning with its record, as a literal.
func TestRecordWarnsEachRecordOfMultiRecordCostFile(t *testing.T) {
	bundle(t, "local-costmulti", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costmulti/cost.json",
		`[{"models":[{"model":"a","input_tokens":1}]},`+
			`{"models":[{"model":"b","input_tokens":2},{"name":"lead","model":"c","input_tokens":3}]}]`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costmulti"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, want := range []string{
		`cost.json record 1: models[0] has no name — the name is what attribution reads; add "name"`,
		`cost.json record 2: models[0] has no name — the name is what attribution reads; add "name"`,
	} {
		if got := countCostWarnings(resp.Warnings, want); got != 1 {
			t.Errorf("warnings = %q, want exactly one %q", resp.Warnings, want)
		}
	}
	if got := countNamelessWarnings(resp.Warnings); got != 2 {
		t.Errorf("warnings = %q, want exactly the two record-qualified warnings", resp.Warnings)
	}
}

func mustCostField(t *testing.T, res readcommon.CostResult, key string) any {
	t.Helper()
	if res.Cost == nil {
		t.Fatalf("rollup cost is nil, warns = %v", res.Warns)
	}
	got, ok := res.Cost.Get(key)
	if !ok {
		t.Fatalf("rollup cost has no %q", key)
	}
	return got
}

// TestRecordWarnsEachNamelessCostEntry proves entries 0 and 2 nameless (1
// named) warn twice, naming each position.
func TestRecordWarnsEachNamelessCostEntry(t *testing.T) {
	bundle(t, "local-costwarn3", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costwarn3/cost.json",
		`{"schema_version":"2","models":[{"model":"a","input_tokens":1},{"name":"lead","model":"b","input_tokens":2},{"model":"c","input_tokens":3}]}`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costwarn3"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, want := range []string{
		`cost.json: models[0] has no name — the name is what attribution reads; add "name"`,
		`cost.json: models[2] has no name — the name is what attribution reads; add "name"`,
	} {
		if got := countCostWarnings(resp.Warnings, want); got != 1 {
			t.Errorf("warnings = %q, want exactly one %q", resp.Warnings, want)
		}
	}
	if got := countNamelessWarnings(resp.Warnings); got != 2 {
		t.Errorf("warnings = %q, want exactly the two position warnings", resp.Warnings)
	}
}

// TestRecordSilentOnNamedCostEntries proves a valid record warns nothing
// about cost.
func TestRecordSilentOnNamedCostEntries(t *testing.T) {
	bundle(t, "local-costok", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costok/cost.json",
		`{"schema_version":"2","models":[{"name":"lead","model":"a","input_tokens":1}]}`)
	c := &collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-costok"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "models[") || strings.HasPrefix(w, "cost.json") {
			t.Errorf("warnings = %q, want no cost warning", resp.Warnings)
			break
		}
	}
}

// TestDryRunWarnsNamelessCostEntry proves the dry run carries the same
// warning text and writes nothing.
func TestDryRunWarnsNamelessCostEntry(t *testing.T) {
	bundle(t, "local-costdry", reviewJSON(""))
	writeFile(t, ".verdict/state/local-costdry/cost.json",
		`{"schema_version":"2","run":"dog","inputs_sha256":"deadbeef","models":[{"model":"claude-x","input_tokens":1200,"output_tokens":300}]}`)
	before, err := os.ReadFile(".verdict/state/local-costdry/cost.json")
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{}
	req := recordRequest(t, c, "local-costdry")
	req.DryRun = true
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run refusal: %v", ref)
	}
	if got := countCostWarnings(resp.Warnings, namelessCostWarning); got != 1 {
		t.Errorf("dry-run warnings = %q, want exactly one %q", resp.Warnings, namelessCostWarning)
	}
	after, err := os.ReadFile(".verdict/state/local-costdry/cost.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("dry run rewrote cost.json")
	}
	if _, err := os.Stat(".verdict/state/local-costdry/record.json"); !os.IsNotExist(err) {
		t.Errorf("dry run wrote record.json")
	}
}

func countCostWarnings(warnings []string, want string) int {
	n := 0
	for _, w := range warnings {
		if w == want {
			n++
		}
	}
	return n
}

func countNamelessWarnings(warnings []string) int {
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, "has no name") {
			n++
		}
	}
	return n
}

// TestCostWarningsMatchAcrossDryRunWithCostNames proves the dry run and the
// live run carry the same cost warnings once --cost-names repairs the entry:
// the repaired entry gets the repair message, never the nameless warning.
// Before the fix the dry run warned both, because the repair counts without
// writing while the nameless scan reads the file the repair has not touched.
func TestCostWarningsMatchAcrossDryRunWithCostNames(t *testing.T) {
	dryResp, liveResp := runCostParityPair(t, "local-costparity",
		core.RecordFlags{CostNames: true, CostPanel: []string{"lead"}})
	if got := countCostWarnings(dryResp.Warnings,
		`cost names: would name 1 models[] entries from the panel (dry run — nothing written)`); got != 1 {
		t.Errorf("dry-run warnings = %q, want exactly one would-name repair message", dryResp.Warnings)
	}
	if got := countCostWarnings(liveResp.Warnings,
		`cost names: named 1 models[] entries from the panel; no token count changed`); got != 1 {
		t.Errorf("live-run warnings = %q, want exactly one named repair message", liveResp.Warnings)
	}
	assertNoNamelessWarning(t, dryResp.Warnings, liveResp.Warnings)
	assertEqualCostSets(t, dryResp.Warnings, liveResp.Warnings)
	raw, err := os.ReadFile(".verdict/state/local-costparity/cost.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name": "lead"`) {
		t.Errorf("cost.json = %s, want the live run to carry the repaired name", raw)
	}
}

// TestCostWarningsMatchAcrossDryRunWithoutCostNames proves the other half: an
// entry no repair names still warns in both modes, with the same warning set.
func TestCostWarningsMatchAcrossDryRunWithoutCostNames(t *testing.T) {
	dryResp, liveResp := runCostParityPair(t, "local-costparityplain", core.RecordFlags{})
	if got := countCostWarnings(dryResp.Warnings, namelessCostWarning); got != 1 {
		t.Errorf("dry-run warnings = %q, want exactly one %q", dryResp.Warnings, namelessCostWarning)
	}
	if got := countCostWarnings(liveResp.Warnings, namelessCostWarning); got != 1 {
		t.Errorf("live-run warnings = %q, want exactly one %q", liveResp.Warnings, namelessCostWarning)
	}
	assertEqualCostSets(t, dryResp.Warnings, liveResp.Warnings)
}

// runCostParityPair records one nameless cost entry twice over the same
// bundle: first as a dry run, then live. Both runs share the flags, so the
// parity assertion compares only what the mode changes.
func runCostParityPair(t *testing.T, slug string, flags core.RecordFlags) (dry, live *core.RecordResponse) {
	t.Helper()
	bundle(t, slug, reviewJSON(""))
	writeFile(t, ".verdict/state/"+slug+"/cost.json",
		`{"schema_version":"2","models":[{"model":"a","input_tokens":1}]}`)
	c := &collector{}
	req := recordRequest(t, c, slug)
	req.Flags = flags
	req.DryRun = true
	dry, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run refusal: %v", ref)
	}
	req.DryRun = false
	live, ref = recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("live-run refusal: %v", ref)
	}
	return dry, live
}

// assertNoNamelessWarning fails when either mode warns a missing name: the
// repaired entry owns the repair message instead.
func assertNoNamelessWarning(t *testing.T, warningSets ...[]string) {
	t.Helper()
	for _, warnings := range warningSets {
		for _, w := range warnings {
			if strings.Contains(w, "has no name") {
				t.Errorf("warnings = %q, want no nameless warning for the repaired entry", warnings)
				return
			}
		}
	}
}

// assertEqualCostSets compares the two modes' cost warnings exactly after
// mapping the repair verb to one shape: the dry run says "would name" where
// the live run says "named", and everything else matches.
func assertEqualCostSets(t *testing.T, dryWarnings, liveWarnings []string) {
	t.Helper()
	dry, live := costWarnings(dryWarnings), costWarnings(liveWarnings)
	if dry, live = normalizeCostVerbs(dry), normalizeCostVerbs(live); !slices.Equal(dry, live) {
		t.Errorf("dry-run warnings = %q, live-run warnings = %q, want the same cost set",
			dryWarnings, liveWarnings)
	}
}

// costWarnings selects the warnings the cost repair and the cost scan own,
// so a parity assertion ignores mode-specific warnings (the real run rebuilds
// the hunk snapshot the dry run parses itself).
func costWarnings(warnings []string) []string {
	var out []string
	for _, w := range warnings {
		if strings.HasPrefix(w, "cost names:") || strings.Contains(w, "models[") {
			out = append(out, w)
		}
	}
	return out
}

// normalizeCostVerbs maps the repair message to one shape so a parity
// assertion compares the two modes' warning sets exactly: the dry run says
// "would name" where the live run says "named", and everything else matches.
func normalizeCostVerbs(warnings []string) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if strings.HasPrefix(w, "cost names: would name") || strings.HasPrefix(w, "cost names: named") {
			w = "cost names: *"
		}
		out = append(out, w)
	}
	return out
}
