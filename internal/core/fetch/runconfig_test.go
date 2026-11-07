package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// baseLsTree scripts one ls-tree -z hit for the trusted-base config read:
// mode, blob, sha, size, tab, path, NUL.
func baseLsTree(base, body string) string {
	return "100644 blob " + base + " " + itoa(len(body)) + "\t.verdict/config.json\x00"
}

// TestResolvePhaseTwoPrefersBaseOverWorktree pins the trust posture with
// independently authored inputs: the base commit carries panel
// base-panel and budget 9.5 while the worktree file names head-panel
// and 1.5. The resolution must carry the base values; a head/worktree
// leak fails the test.
func TestResolvePhaseTwoPrefersBaseOverWorktree(t *testing.T) {
	rc, base := resolveOverDirtyWorktree(t)
	assertBasePolicyWins(t, rc, base)
}

// resolveOverDirtyWorktree seeds a worktree config that contradicts the base
// commit and resolves phase two against the base, returning the raw base bytes.
func resolveOverDirtyWorktree(t *testing.T) (*runConfig, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	base := `{"schema_version":"1","defaults":{"panel":["base-panel"],"budget_usd":9.5}}`
	head := `{"schema_version":"1","defaults":{"panel":["head-panel"],"budget_usd":1.5}}`
	if err := os.MkdirAll(".verdict", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".verdict/config.json", []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	const baseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"git\x00ls-tree\x00-lz\x00" + baseSHA + "\x00--\x00.verdict/config.json": {stdout: baseLsTree(baseSHA, base)},
		"git\x00show\x00" + baseSHA + ":.verdict/config.json":                    {stdout: base},
	}}
	rc, ref := resolvePhaseOne(map[string]string{}, "", false)
	if ref != nil {
		t.Fatalf("phase one: %v", ref)
	}
	if rerr := rc.resolvePhaseTwo(context.Background(), cmdr, "o/r", baseSHA, false, nil, ""); rerr != nil {
		t.Fatalf("phase two: %v", rerr)
	}
	return rc, base
}

// assertBasePolicyWins checks the resolution carries the base commit's values,
// bytes, and trust flag — a head or worktree leak fails here.
func assertBasePolicyWins(t *testing.T, rc *runConfig, base string) {
	t.Helper()
	if got := rc.resolved.Defaults.Values.Panel; len(got) != 1 || got[0] != "base-panel" {
		t.Fatalf("panel = %v, want [base-panel] from the base commit", got)
	}
	if got := rc.resolved.Defaults.Values.BudgetUSD; got == nil || *got != 9.5 {
		t.Fatalf("budget = %v, want 9.5 from the base commit", got)
	}
	if string(rc.raw) != base {
		t.Fatalf("raw = %s, want the base bytes", rc.raw)
	}
	if !rc.trusted {
		t.Fatal("base policy present but trusted is false")
	}
}

// spendResolved builds a resolution with a repository guard and a
// personal ceiling for the spend-gate tests: repo budget 5, personal
// ceiling 3. The oracle is the spec sentence (personal ceiling wins,
// no consent path), not the gate's own arithmetic.
func spendResolved(t *testing.T, enforceCap, requireKnown bool) *config.Resolved {
	t.Helper()
	limit := 3.0
	repo, ref := config.Parse([]byte(`{"schema_version":"1","defaults":{"budget_usd":5.0}}`))
	if ref != nil {
		t.Fatalf("repo parse: %v", ref)
	}
	resolved, rerr := config.Resolve(&config.Inputs{
		Repo: repo,
		Personal: &config.PersonalSettings{Spend: config.PersonalSpend{
			BudgetUSD:            &limit,
			EnforceCap:           enforceCap,
			RequireKnownEstimate: requireKnown,
		}},
	})
	if rerr != nil {
		t.Fatalf("resolve: %v", rerr)
	}
	return resolved
}

func knownCostSession(t *testing.T, estUSD string) *session {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".verdict", "state", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cost := `[{"effort":"standard","est_usd":` + estUSD + `,"models":[]}]`
	if err := os.WriteFile(filepath.Join(".verdict", "state", "1", "cost.json"), []byte(cost), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	return &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
}

// TestBudgetGatePersonalCeilingConsentProof pins the consent rule for the
// personal ceiling: a known $4 projection against a $3 ceiling refuses
// spend-capped until the operator consents, and the consented run arms a
// receipt instead of passing silently.
func TestBudgetGatePersonalCeilingConsentProof(t *testing.T) {
	r := knownCostSession(t, "4.00")
	resolved := spendResolved(t, false, false)
	resolved.Spend.PersonalLimit = new(3.0)
	budget := resolveBudget(r, resolved)
	if !budget.hasBudget {
		t.Fatal("ordinary guard missing: repo budget 5.0 should arm it")
	}
	gerr := budgetGate(r, &budget, resolved, false)
	var ref *core.RefusalError
	if !errors.As(gerr, &ref) || ref.Code != "spend-capped" {
		t.Fatalf("gate without consent = %v, want spend-capped", gerr)
	}
	if gerr := budgetGate(r, &budget, resolved, true); gerr != nil {
		t.Fatalf("gate with consent = %v, want nil", gerr)
	}
	if !budget.receiptNeeded {
		t.Fatal("a consented ceiling hit armed no receipt")
	}
}

// TestBudgetGateOrdinaryGuardConsentReceipt pins the consent path: a $6
// projection over the $5 guard with no personal ceiling proceeds on
// consent and arms exactly one fresh receipt.
func TestBudgetGateOrdinaryGuardConsentReceipt(t *testing.T) {
	r := knownCostSession(t, "6.00")
	repo, ref := config.Parse([]byte(`{"schema_version":"1","defaults":{"budget_usd":5.0}}`))
	if ref != nil {
		t.Fatalf("repo parse: %v", ref)
	}
	resolved, rerr := config.Resolve(&config.Inputs{Repo: repo, Personal: &config.PersonalSettings{}})
	if rerr != nil {
		t.Fatalf("resolve: %v", rerr)
	}
	budget := resolveBudget(r, resolved)
	if gerr := budgetGate(r, &budget, resolved, true); gerr != nil {
		t.Fatalf("consented gate = %v, want nil", gerr)
	}
	if !budget.receiptNeeded {
		t.Fatal("consented over-budget run armed no receipt")
	}
	gerr := budgetGate(r, &budget, resolved, false)
	if gerr == nil {
		t.Fatal("unconsented over-budget run passed")
	}
	var refusal *core.RefusalError
	if !errors.As(gerr, &refusal) || refusal.Code != "budget-exceeded" {
		t.Fatalf("unconsented gate = %v, want budget-exceeded", gerr)
	}
}

// TestBudgetGateUnknownEstimate pins the blind-run rules: a set budget
// with no usable projection refuses budget-unknown under the strict
// posture until the operator consents, and a consented blind run arms a
// receipt instead of proceeding silently.
func TestBudgetGateUnknownEstimate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var warnings []string
	r := &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
	strict := spendResolved(t, false, true)
	budget := resolveBudget(r, strict)
	gerr := budgetGate(r, &budget, strict, false)
	if gerr == nil {
		t.Fatal("unknown estimate without consent passed")
	}
	var ref *core.RefusalError
	if !errors.As(gerr, &ref) || ref.Code != "budget-unknown" {
		t.Fatalf("unknown gate = %v, want budget-unknown", gerr)
	}
	budget = resolveBudget(r, strict)
	if gerr := budgetGate(r, &budget, strict, true); gerr != nil {
		t.Fatalf("consented strict blind run = %v, want nil with receipt", gerr)
	}
	if !budget.receiptNeeded {
		t.Fatal("consented strict blind run armed no receipt")
	}
	lax := spendResolved(t, false, false)
	lax.Spend.RequireKnownEstimate = false
	lax.Spend.PersonalLimit = nil
	budget = resolveBudget(r, lax)
	if gerr := budgetGate(r, &budget, lax, true); gerr != nil {
		t.Fatalf("consented blind run = %v, want nil with receipt", gerr)
	}
	if !budget.receiptNeeded {
		t.Fatal("consented blind run armed no receipt")
	}
}

// TestBudgetGateEnforceCap pins the unsupported-cap rule: a request that
// sets enforce_cap refuses unsupported-cap before any budget, force, or
// consent can authorize the run.
func TestBudgetGateEnforceCap(t *testing.T) {
	r := knownCostSession(t, "6.00")
	resolved := spendResolved(t, true, false)
	resolved.Spend.PersonalLimit = nil
	for _, consent := range []bool{false, true} {
		budget := resolveBudget(r, resolved)
		gerr := budgetGate(r, &budget, resolved, consent)
		var ref *core.RefusalError
		if !errors.As(gerr, &ref) || ref.Code != "unsupported-cap" {
			t.Fatalf("enforce_cap gate (consent=%v) = %v, want unsupported-cap", consent, gerr)
		}
	}
}

// TestWithholdChunkFiles pins the privacy choke point with a
// hand-authored table: excluded paths never reach a chunk payload,
// included paths pass through untouched.
func TestWithholdChunkFiles(t *testing.T) {
	table := []fileSlice{
		{path: "notes.txt", size: 10},
		{path: "secret/plan.md", size: 20},
		{path: "secret/keys/env.txt", size: 30},
	}
	kept := withholdChunkFiles(table, []string{"secret/**"})
	if len(kept) != 1 || kept[0].path != "notes.txt" {
		t.Fatalf("kept = %+v, want only notes.txt", kept)
	}
	if kept := withholdChunkFiles(table, nil); len(kept) != 3 {
		t.Fatalf("empty exclusions dropped files: %+v", kept)
	}
}

// TestFilterDiffBytes pins the dump filter byte-for-byte: the excluded
// file's section leaves while every other byte stays identical,
// including the trailing newline contract.
func TestFilterDiffBytes(t *testing.T) {
	raw := "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/secret/plan.md b/secret/plan.md\n--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-x\n+y\n"
	filtered := string(filterDiffBytes([]byte(raw), []string{"secret/**"}))
	if strings.Contains(filtered, "secret/plan.md") || strings.Contains(filtered, "+y") {
		t.Fatalf("excluded content survived:\n%s", filtered)
	}
	want := "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n"
	if filtered != want {
		t.Fatalf("filtered = %q, want %q", filtered, want)
	}
	if got := string(filterDiffBytes([]byte(raw), nil)); got != raw {
		t.Fatal("empty exclusions rewrote the diff")
	}
}

// privacySentinelDiff is a two-file staged diff carrying a sentinel
// string inside the excluded file: the oracle names the exact bytes
// that must never reach model-facing outputs.
func privacySentinelDiff() string {
	return "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/secret/plan.md b/secret/plan.md\n--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-x\n+ySENTINEL-ALPHA-7\n"
}

// TestPrivacySentinelAbsentFromModelFacingOutputs pins the exclusion
// enforcement end to end: a personal privacy exclusion on secret/**
// withholds the sentinel from the prompt dump while the raw bundle
// keeps every byte and patch identity stays exact.
func TestPrivacySentinelAbsentFromModelFacingOutputs(t *testing.T) {
	resp := stagedPrivacyRun(t)
	assertSentinelWithheld(t, resp.Data.PromptDump)
}

// stagedPrivacyRun stages an over-cap-free local run with a personal privacy
// exclusion on secret/** and returns the response plus the bundle directory.
func stagedPrivacyRun(t *testing.T) *core.FetchResponse {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	settings := `{"privacy":{"exclusions":["secret/**"]}}`
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("prompts", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("prompts", "system.md"), []byte("system\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdr := stagedLocalCommander(t, privacySentinelDiff())
	var human, diag bytes.Buffer
	var streamed []string
	flags := core.FetchLocalFlags{Staged: true, ShowPrompts: true}
	req := localScriptReq(cmdr, &human, &diag, &streamed, nil, flags)
	req.SettingsPath = settingsPath
	req.JSONMode = true
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	return resp
}

// assertSentinelWithheld checks the model-facing dump omits the excluded
// sentinel while the raw bundle keeps every byte and patch identity.
func assertSentinelWithheld(t *testing.T, dump *string) {
	t.Helper()
	if dump == nil {
		t.Fatal("no prompt dump staged")
	}
	if strings.Contains(*dump, "SENTINEL-ALPHA-7") {
		t.Fatalf("sentinel reached the prompt dump:\n%s", *dump)
	}
	if !strings.Contains(*dump, "plan.txt") {
		t.Fatalf("plan surface missing from the dump:\n%s", *dump)
	}
	raw, err := os.ReadFile(filepath.Join(".verdict", "state", "local-mine", "full.diff"))
	if err != nil {
		t.Fatalf("read full.diff: %v", err)
	}
	if !strings.Contains(string(raw), "SENTINEL-ALPHA-7") {
		t.Fatal("raw bundle lost the sentinel: patch identity must stay complete")
	}
}
