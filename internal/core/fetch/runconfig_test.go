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
	if rerr := rc.resolvePhaseTwo(context.Background(), cmdr, "o/r", baseSHA, false, nil, "", config.LegacyInputs{}); rerr != nil {
		t.Fatalf("phase two: %v", rerr)
	}
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
	if err := os.MkdirAll(filepath.Join(".review", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cost := `[{"effort":"standard","est_usd":` + estUSD + `,"models":[]}]`
	if err := os.WriteFile(filepath.Join(".review", "1", "cost.json"), []byte(cost), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	return &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
}

// TestBudgetGatePersonalCeilingConsentProof pins the strict spend rule:
// a known $4 projection against a $5 ordinary guard but a $3 personal
// ceiling refuses spend-capped even with explicit consent. No consent
// path exists for the ceiling.
func TestBudgetGatePersonalCeilingConsentProof(t *testing.T) {
	r := knownCostSession(t, "4.00")
	resolved := spendResolved(t, false, false)
	budget := resolveBudget(r, resolved)
	if !budget.hasBudget {
		t.Fatal("ordinary guard missing: repo budget 5.0 should arm it")
	}
	gerr := budgetGate(r, &budget, resolved, true)
	var ref *core.RefusalError
	if !errors.As(gerr, &ref) || ref.Code != "spend-capped" {
		t.Fatalf("gate with consent = %v, want spend-capped", gerr)
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
// with no usable projection refuses spend-unknown when the settings
// require a known estimate, and a consented blind run arms a receipt
// instead of proceeding silently.
func TestBudgetGateUnknownEstimate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var warnings []string
	r := &session{Human: io.Discard, Diag: io.Discard, warnings: &warnings}
	strict := spendResolved(t, false, true)
	budget := resolveBudget(r, strict)
	gerr := budgetGate(r, &budget, strict, true)
	if gerr == nil {
		t.Fatal("unknown estimate with require_known_estimate passed")
	}
	var ref *core.RefusalError
	if !errors.As(gerr, &ref) || ref.Code != "spend-unknown" {
		t.Fatalf("unknown gate = %v, want spend-unknown", gerr)
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

// TestBudgetGateEnforceCap pins the hard-cap rule: with enforce_cap the
// ordinary guard refuses even under explicit consent.
func TestBudgetGateEnforceCap(t *testing.T) {
	r := knownCostSession(t, "6.00")
	resolved := spendResolved(t, true, false)
	resolved.Spend.PersonalLimit = nil
	budget := resolveBudget(r, resolved)
	gerr := budgetGate(r, &budget, resolved, true)
	if gerr == nil {
		t.Fatal("enforce_cap run passed under consent")
	}
	var ref *core.RefusalError
	if !errors.As(gerr, &ref) || ref.Code != "budget-exceeded" {
		t.Fatalf("enforce_cap gate = %v, want budget-exceeded", gerr)
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
	if resp.Data == nil || resp.Data.PromptDump == nil {
		t.Fatal("no prompt dump staged")
	}
	if strings.Contains(*resp.Data.PromptDump, "SENTINEL-ALPHA-7") {
		t.Fatalf("sentinel reached the prompt dump:\n%s", *resp.Data.PromptDump)
	}
	if !strings.Contains(*resp.Data.PromptDump, "plan.txt") {
		t.Fatalf("plan surface missing from the dump:\n%s", *resp.Data.PromptDump)
	}
	raw, err := os.ReadFile(filepath.Join(".review", "local-mine", "full.diff"))
	if err != nil {
		t.Fatalf("read full.diff: %v", err)
	}
	if !strings.Contains(string(raw), "SENTINEL-ALPHA-7") {
		t.Fatal("raw bundle lost the sentinel: patch identity must stay complete")
	}
}
