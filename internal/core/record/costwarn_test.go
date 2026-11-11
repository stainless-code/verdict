package recordcore_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

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
