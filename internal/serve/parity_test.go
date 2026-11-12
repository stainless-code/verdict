package serve

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/golden"
)

var harness *golden.Harness

// TestMain builds the CLI once for the parity legs and runs the suite.
func TestMain(m *testing.M) {
	os.Exit(golden.RunMain(m, &harness))
}

// cliEnvelope runs one CLI command in dir and returns its decoded envelope.
func cliEnvelope(t *testing.T, dir string, args ...string) map[string]any {
	t.Helper()
	home := t.TempDir()
	out := harness.Run(t, dir, home, nil, args...)
	if out.Status != 0 && out.Status != 1 {
		t.Fatalf("%v exited %d\nstdout: %s\nstderr: %s", args, out.Status, out.Stdout, out.Stderr)
	}
	var envelope map[string]any
	dec := json.NewDecoder(strings.NewReader(out.Stdout))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
		t.Fatalf("decode %v envelope: %v\n%s", args, err, out.Stdout)
	}
	return envelope
}

// sameJSON compares two wire values after the harness's timestamp scrub.
func sameJSON(t *testing.T, label string, want, got any) {
	t.Helper()
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal %s want: %v", label, err)
	}
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal %s got: %v", label, err)
	}
	wantText := golden.Scrub(string(wantRaw))
	gotText := golden.Scrub(string(gotRaw))
	if wantText != gotText {
		t.Errorf("%s differs from the CLI:\nwant: %s\ngot:  %s", label, wantText, gotText)
	}
}

// TestDetailParity proves data.report and data.gate are byte-identical to the
// report verb's --json data in the same fixture.
func TestDetailParity(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	cli := cliEnvelope(t, root, "report", "acme/app", "1234", "--json", "--cost", "--check", "critical", "--live", "skip")
	layer := query.New(query.Snapshot{
		Version: "1.2.3",
		Roots:   []query.Root{{Path: root}},
		Runtime: &core.Runtime{Now: fixedNow},
	}, query.Options{Check: "critical", MaxBundles: 10})
	view, err := layer.Detail(t.Context(), query.RootID(mustAbs(t, root)), "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	data := wire(t, view.Data)
	sameJSON(t, "data.report", cli["data"], data["report"])
	sameJSON(t, "data.gate", asMap(t, cli["data"])["gate"], data["gate"])
}

// TestShowParity proves the show route's data is byte-identical to
// show --json's data.
func TestShowParity(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	cli := cliEnvelope(t, root, "show", "acme/app", "1234", "--sig", sigCrit[:4], "--json")
	layer := query.New(query.Snapshot{
		Version: "1.2.3",
		Roots:   []query.Root{{Path: root}},
		Runtime: &core.Runtime{Now: fixedNow},
	}, query.Options{Check: "critical", MaxBundles: 10})
	view, err := layer.Show(t.Context(), query.RootID(mustAbs(t, root)), "1234", sigCrit[:4])
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	sameJSON(t, "show data", cli["data"], wire(t, view.Data))
}

// TestGateParity proves the detail's gate dimensions are the gate verb's,
// minus the coverage dimension the detail does not request.
func TestGateParity(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, fixtureFiles())
	cli := cliEnvelope(t, root, "gate", "acme/app", "1234", "--json",
		"--max-severity", "critical", "--live", "skip", "--coverage", "warn")
	layer := query.New(query.Snapshot{
		Version: "1.2.3",
		Roots:   []query.Root{{Path: root}},
		Runtime: &core.Runtime{Now: fixedNow},
	}, query.Options{Check: "critical", MaxBundles: 10})
	view, err := layer.Detail(t.Context(), query.RootID(mustAbs(t, root)), "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	serveGate := asMap(t, wire(t, view.Data)["gate"])
	cliGate := asMap(t, cli["data"])
	if serveGate["passed"] != cliGate["passed"] {
		t.Errorf("gate passed = %v, want %v", serveGate["passed"], cliGate["passed"])
	}
	sameJSON(t, "gate dimensions",
		withoutCoverage(t, cliGate["dimensions"]),
		withoutCoverage(t, serveGate["dimensions"]))
}

// withoutCoverage drops the coverage dimension from a gate dimension list.
func withoutCoverage(t *testing.T, raw any) []any {
	t.Helper()
	dimensions, ok := raw.([]any)
	if !ok {
		t.Fatalf("dimensions = %v, want a list", raw)
	}
	out := make([]any, 0, len(dimensions))
	for _, dimension := range dimensions {
		row, ok := dimension.(map[string]any)
		if !ok {
			t.Fatalf("dimension = %v, want an object", dimension)
		}
		if row["id"] == "coverage" {
			continue
		}
		out = append(out, row)
	}
	return out
}
