package postcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// lensPostState builds the real post gate state over a run bundle whose
// plan carries one capped group, with cost.json bytes written beside it.
func lensPostState(t *testing.T, maxLenses int, cost string) *state {
	t.Helper()
	m := maxLenses
	st := policyTestState(nil, &config.RunState{Name: "7", InputsSHA256: "feedface", Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
		Groups: []config.PlanGroup{{ID: "group-a", Label: "alpha", MaxLenses: &m, Profile: "alpha"}},
	}}})
	st.dir = t.TempDir()
	if cost != "" {
		cost = bindLensCost(cost)
		if err := os.WriteFile(filepath.Join(st.dir, "cost.json"), []byte(cost), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// bindLensCost stamps the current capture binding on a cost fixture, copied
// as the writer would from run.json: an unbound record is unknown, so every
// fixture that must certify this run needs the same run name and inputs hash.
func bindLensCost(cost string) string {
	return strings.Replace(cost, `"schema_version":"2",`,
		`"schema_version":"2","run":"7","inputs_sha256":"feedface",`, 1)
}

// groupEntry is one bound lens attestation naming the capped group.
const groupEntry = `{"group":"group-a","labels":["correctness"]}`

// lensStateForCase builds the gate state for one cap row: a negative cap is
// the unconfigured case, everything else uses the single capped group.
func lensStateForCase(t *testing.T, limit int, cost string) *state {
	t.Helper()
	if limit >= 0 {
		return lensPostState(t, limit, cost)
	}
	st := policyTestState(nil, &config.RunState{Resolved: &config.Resolved{Plan: config.Plan{
		Groups: []config.PlanGroup{{ID: "group-a", Label: "alpha", Profile: "alpha"}},
	}}})
	st.dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(st.dir, "cost.json"), []byte(bindLensCost(cost)), 0o600); err != nil {
		t.Fatal(err)
	}
	return st
}

// requireGate asserts the refusal code exactly, with an empty code meaning
// the approval proceeds.
func requireGate(t *testing.T, st *state, wantCode string) {
	t.Helper()
	ref := st.approveGate()
	if wantCode == "" {
		if ref != nil {
			t.Fatalf("approveGate refused: %v, want pass", ref)
		}
		return
	}
	if ref == nil || ref.Code != wantCode {
		t.Fatalf("ref = %v, want %q", ref, wantCode)
	}
}

// TestApproveRefusesIncompleteLensEvidence proves the binding approval gate
// reads group-attributed usage: over cap, missing, and unbound usage refuse
// an APPROVE, while recorded usage inside the cap and an unconfigured cap
// proceed. The evidence is agent self-report, so this gate never claims a
// lens ran — only that the report covers the capped group.
func TestApproveRefusesIncompleteLensEvidence(t *testing.T) {
	tests := []struct {
		name      string
		cost      string
		wantCode  string
		maxLenses int
	}{
		{
			name: "cap one, one dispatch", maxLenses: 1,
			cost: `{"schema_version":"2","lenses":[{"group":"group-a","labels":["correctness"]}]}`,
		},
		{
			name: "cap one, two dispatches", maxLenses: 1,
			cost:     `{"schema_version":"2","lenses":[{"group":"group-a","labels":["correctness","security"]}]}`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name: "cap zero, explicit empty attestation", maxLenses: 0,
			cost: `{"schema_version":"2","lenses":[{"group":"group-a","labels":[]}]}`,
		},
		{
			name: "cap zero, one dispatch", maxLenses: 0,
			cost:     `{"schema_version":"2","lenses":[{"group":"group-a","labels":["correctness"]}]}`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name: "capped group, no usage recorded", maxLenses: 1,
			cost:     `{"schema_version":"2"}`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name: "capped group, legacy string usage", maxLenses: 1,
			cost:     `{"lenses":["correctness"]}`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name: "capped group, usage names another group", maxLenses: 1,
			cost:     `{"lenses":[{"group":"group-b","labels":["correctness"]}]}`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name: "unconfigured cap, no usage", maxLenses: -1,
			cost: `{"schema_version":"2"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireGate(t, lensStateForCase(t, tt.maxLenses, tt.cost), tt.wantCode)
		})
	}
}

// TestApproveAcceptsMixedWorkAndZero proves the representability fix at the
// binding seam: a review can record work in one capped group and an explicit
// zero in a zero-cap group, and that mixed attestation approves. Omitting
// the zero group still refuses, so the zero must be stated, not inferred.
func TestApproveAcceptsMixedWorkAndZero(t *testing.T) {
	two, zero := 2, 0
	build := func(cost string) *state {
		st := policyTestState(nil, &config.RunState{Name: "7", InputsSHA256: "feedface", Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
			Groups: []config.PlanGroup{
				{ID: "group-a", Label: "alpha", MaxLenses: &two, Profile: "alpha"},
				{ID: "group-b", Label: "beta", MaxLenses: &zero, Profile: "beta"},
			},
		}}})
		st.dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(st.dir, "cost.json"), []byte(cost), 0o600); err != nil {
			t.Fatal(err)
		}
		return st
	}
	mixed := `{"schema_version":"2","run":"7","inputs_sha256":"feedface","lenses":[{"group":"group-a","labels":["correctness","security"]},{"group":"group-b","labels":[]}]}`
	if ref := build(mixed).approveGate(); ref != nil {
		t.Fatalf("mixed work and explicit zero refused: %v", ref)
	}
	omitted := `{"schema_version":"2","run":"7","inputs_sha256":"feedface","lenses":[{"group":"group-a","labels":["correctness","security"]}]}`
	if ref := build(omitted).approveGate(); ref == nil || ref.Code != CodeRequirementUnmet {
		t.Fatalf("omitted zero group = %v, want requirement-unmet", ref)
	}
}

// jsonStr renders one string literal for a hand-built JSON fixture: a native
// Windows root carries backslashes, which are escape introducers in JSON and
// must be encoded, never interpolated raw.
func jsonStr(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}

// deepReceipt is an independent literal of a tool-produced receipt: the post
// gate must accept only the exact run/group/head/root it names.
func deepReceipt(run, inputs, group, head, root string, passed bool, exit int) string {
	return `{"schema_version":"1","run":` + jsonStr(run) + `,"inputs_sha256":` + jsonStr(inputs) +
		`,"group":` + jsonStr(group) + `,"reviewed_head":` + jsonStr(head) + `,"execution_head":` +
		jsonStr(head) + `,"repo_root":` + jsonStr(root) +
		`,"command":"go test ./...","at":"2026-11-06T00:00:00Z","output_tail":"ok","exit":` +
		strconv.Itoa(exit) + `,"timed_out":false,"status":"` + receiptStatus(passed) + `","passed":` +
		strconv.FormatBool(passed) + `}`
}

// receiptStatus is the status token the receipt writer emits for a passing
// and a failing exit; the read side re-derives it from exit/timed_out.
func receiptStatus(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

// TestApproveRequiresDeepValidationReceipt proves the deep gate: a matching
// tool receipt is the only thing that lets a binding approval through, and a
// receipt for another group, head, run, or a failed exit never does.
func TestApproveRequiresDeepValidationReceipt(t *testing.T) {
	const head = "1234567890abcdef1234567890abcdef12345678"
	tests := []struct {
		name     string
		receipt  func(run, inputs, root string) string
		wantCode string
	}{
		{name: "matching receipt approves", receipt: func(run, inputs, root string) string {
			return deepReceipt(run, inputs, "group-a", head, root, true, 0)
		}},
		{name: "missing receipt refuses", receipt: nil, wantCode: CodeRequirementUnmet},
		{name: "failed exit refuses", receipt: func(run, inputs, root string) string {
			return deepReceipt(run, inputs, "group-a", head, root, false, 1)
		}, wantCode: CodeRequirementUnmet},
		{name: "stale head refuses", receipt: func(run, inputs, root string) string {
			return deepReceipt(run, inputs, "group-a", strings.Repeat("9", 40), root, true, 0)
		}, wantCode: CodeRequirementUnmet},
		{name: "other group's receipt refuses", receipt: func(run, inputs, root string) string {
			return deepReceipt(run, inputs, "group-b", head, root, true, 0)
		}, wantCode: CodeRequirementUnmet},
		{name: "wrong inputs hash refuses", receipt: func(run, _, root string) string {
			return deepReceipt(run, "deadbeef", "group-a", head, root, true, 0)
		}, wantCode: CodeRequirementUnmet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := deepGateState(t, head)
			if tt.receipt != nil {
				writeDeepReceipt(t, st, tt.receipt)
			}
			requireGate(t, st, tt.wantCode)
		})
	}
}

// deepGateState builds the real gate state over one deep and one plain group.
func deepGateState(t *testing.T, head string) *state {
	t.Helper()
	st := policyTestState(nil, &config.RunState{
		Name: "7", InputsSHA256: "feedface", Trusted: true,
		Resolved: &config.Resolved{Plan: config.Plan{Groups: []config.PlanGroup{
			{ID: "group-a", Label: "alpha", Depth: "deep"},
			{ID: "group-b", Label: "beta"},
		}}},
	})
	st.dir = t.TempDir()
	st.fetchedSHA = head
	st.req.RepoRoot = st.dir
	return st
}

// writeDeepReceipt records one fixture receipt for the deep group.
func writeDeepReceipt(t *testing.T, st *state, render func(run, inputs, root string) string) {
	t.Helper()
	canon, ref := verifycore.CanonicalRoot(st.dir)
	if ref != nil {
		t.Fatalf("CanonicalRoot: %v", ref)
	}
	if err := os.WriteFile(filepath.Join(st.dir, "validation-group-a.json"),
		[]byte(render("7", "feedface", canon)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestApproveRejectsContradictoryReceipt proves the read side re-derives the
// execution facts: a receipt claiming passed with a nonzero exit (or a
// timeout) is contradictory evidence and never approves.
func TestApproveRejectsContradictoryReceipt(t *testing.T) {
	const head = "1234567890abcdef1234567890abcdef12345678"
	for _, tt := range []struct{ name, body string }{
		{name: "passed with nonzero exit", body: `"exit":1,"timed_out":false,"status":"passed","passed":true`},
		{name: "passed while timed out", body: `"exit":0,"timed_out":true,"status":"passed","passed":true`},
		{name: "failed status claiming pass", body: `"exit":0,"timed_out":false,"status":"failed","passed":true`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requireGate(t, contradictoryState(t, head, tt.body), CodeRequirementUnmet)
		})
	}
}

// contradictoryState writes a receipt whose status contradicts its execution
// facts and returns the gate state pointing at the current capture.
func contradictoryState(t *testing.T, head, facts string) *state {
	t.Helper()
	st := deepGateState(t, head)
	st.policy.state.Resolved.Plan.Groups = st.policy.state.Resolved.Plan.Groups[:1]
	root, ref := verifycore.CanonicalRoot(st.dir)
	if ref != nil {
		t.Fatalf("CanonicalRoot: %v", ref)
	}
	body := `{"schema_version":"1","run":"7","inputs_sha256":"feedface","group":"group-a","reviewed_head":` +
		jsonStr(head) + `,"execution_head":` + jsonStr(head) + `,"repo_root":` + jsonStr(root) +
		`,"command":"true","at":"2026-11-06T00:00:00Z","output_tail":"","` + facts + `}`
	if err := os.WriteFile(filepath.Join(st.dir, "validation-group-a.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestApproveBindsLensCostToCapture proves the cost binding: a lens record
// copied from the current run.json certifies that capture, while a record
// with the same stable group id but another run/inputs hash stays unknown and
// refuses. The binding is capture freshness, never proof a lens ran.
func TestApproveBindsLensCostToCapture(t *testing.T) {
	tests := []struct {
		name, meta, wantCode string
	}{
		{name: "current capture record certifies", meta: `"run":"7","inputs_sha256":"feedface"`},
		{
			name:     "same group, older capture refuses",
			meta:     `"run":"7","inputs_sha256":"deadbeef"`,
			wantCode: CodeRequirementUnmet,
		},
		{
			name:     "same group, another run refuses",
			meta:     `"run":"8","inputs_sha256":"feedface"`,
			wantCode: CodeRequirementUnmet,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireGate(t, boundCostState(t, tt.meta), tt.wantCode)
		})
	}
}

// boundCostState builds the capped-group gate state over one cost record
// carrying the given capture metadata.
func boundCostState(t *testing.T, meta string) *state {
	t.Helper()
	maxLenses := 1
	st := policyTestState(nil, &config.RunState{
		Name: "7", InputsSHA256: "feedface", Trusted: true,
		Resolved: &config.Resolved{Plan: config.Plan{Groups: []config.PlanGroup{
			{ID: "group-a", Label: "alpha", Profile: "alpha", MaxLenses: &maxLenses},
		}}},
	})
	st.dir = t.TempDir()
	cost := `{"schema_version":"2",` + meta + `,"lenses":[` + groupEntry + `]}`
	if err := os.WriteFile(filepath.Join(st.dir, "cost.json"), []byte(cost), 0o600); err != nil {
		t.Fatal(err)
	}
	return st
}
