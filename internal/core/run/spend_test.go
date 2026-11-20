package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/storelock"
)

func spendState(t *testing.T) (*state, string) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"schema_version":"1","execution":{"id":"execution-A","finders":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	req := request(root, &scripted{})
	req.SpendPolicy = &spend.Policy{RequireKnown: true}
	return &state{req: req}, dir
}

func TestSpendConsentIsPerInvocation(t *testing.T) {
	state, dir := spendState(t)
	state.req.ConsentOverBudget = true
	if ref := state.authorizeSpend(t.Context(), dir, 2); ref != nil {
		t.Fatal(ref)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	state.req.ConsentOverBudget = false
	ref := state.authorizeSpend(t.Context(), dir, 2)
	if ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("previous consent reused: %v", ref)
	}
	after, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, raw) {
		t.Fatal("refused invocation appended authorization")
	}
}

func TestSpendKnownLowerBoundRequiresConsent(t *testing.T) {
	state, dir := spendState(t)
	state.req.Env["REVIEW_BUDGET_USD"] = "1"
	state.req.SpendPlan = []spend.Phase{{ExecutionID: "execution-A", LaunchID: "finder-1-discovery-1", Finder: "finder-1", Phase: "discovery", USD: new(2.0)}}
	ref := state.authorizeSpend(t.Context(), dir, 2)
	if ref == nil || ref.Code != "budget-exceeded" {
		t.Fatalf("known spend passed guard: %v", ref)
	}
}

func TestSpendReceiptChangesWithTopology(t *testing.T) {
	state, dir := spendState(t)
	state.req.ConsentOverBudget = true
	if ref := state.authorizeSpend(t.Context(), dir, 2); ref != nil {
		t.Fatal(ref)
	}
	if ref := state.authorizeSpend(t.Context(), dir, 1); ref != nil {
		t.Fatal(ref)
	}
	ledger := twoSpendReceipts(t, dir)
	first := readcommon.FieldOf(ledger[0], "projection_sha256")
	second := readcommon.FieldOf(ledger[1], "projection_sha256")
	if first == second {
		t.Fatal("changed topology reused projection digest")
	}
	if readcommon.FieldOf(ledger[1], "scope") != "invocation" || readcommon.FieldOf(ledger[1], "execution_id") != "execution-A" {
		t.Fatal("receipt lost invocation binding")
	}
}

func twoSpendReceipts(t *testing.T, dir string) []any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	ledger, ok := value.([]any)
	if !ok || len(ledger) != 2 {
		t.Fatalf("receipt ledger = %v", value)
	}
	return ledger
}

func TestSpendRefusesMalformedCapture(t *testing.T) {
	state, dir := spendState(t)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "launcher.json"), []byte(`{"execution_id":"execution-A"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := state.authorizeSpend(t.Context(), dir, 2)
	if ref == nil || ref.Code != "agent-record-failed" {
		t.Fatalf("malformed capture authorization = %v", ref)
	}
}

func TestSpendAppendKeepsLedgerReadable(t *testing.T) {
	state, dir := spendState(t)
	state.req.ConsentOverBudget = true
	raw := append(append([]byte(`["`), bytes.Repeat([]byte("a"), 1048570)...), []byte("\"]\n")...)
	path := filepath.Join(dir, "budget.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := state.authorizeSpend(t.Context(), dir, 2)
	if ref == nil || ref.Code != "agent-record-failed" {
		t.Fatalf("oversize append authorization = %v", ref)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, after) {
		t.Fatal("oversize append changed ledger")
	}
}

func TestCanceledSpendPreparation(t *testing.T) {
	state, dir := spendState(t)
	state.req.ConsentOverBudget = true
	unlock, err := storelock.Acquire(filepath.Join(dir, "budget.json"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ref := state.prepareLaunch(ctx, &launchArgs{target: launchTarget{evidence: dir}}, 2)
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("canceled spend preparation = %v", ref)
	}
}
