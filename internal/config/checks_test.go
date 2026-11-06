package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// complianceFixture builds a resolution with one scoped file and one
// root clause for the compliance tests.
func complianceFixture() (*Resolved, []ClauseProvenance) {
	root := []ClauseProvenance{
		{Clause: core.BlockClause{Name: "root-critical", Threshold: "critical"}, Sources: []string{"repo"}},
	}
	res := &Resolved{
		Files: []FileResolution{
			{
				Path:   "pay/core.go",
				Scopes: []string{"payments"},
				Requirements: RequirementSet{
					Clauses: []ClauseProvenance{
						{
							Clause: core.BlockClause{
								Name: "pay-block", Threshold: "important",
								Categories: []string{"logic", "security"}, Paths: []string{"pay/**"},
							},
							Sources: []string{"scope payments"},
						},
					},
				},
			},
			{
				Path:    "pay/renamed.go",
				OldPath: "pay/old.go",
				Scopes:  []string{"payments"},
				Requirements: RequirementSet{
					Clauses: []ClauseProvenance{
						{
							Clause: core.BlockClause{
								Name: "pay-block", Threshold: "important",
								Categories: []string{"logic", "security"}, Paths: []string{"pay/**"},
							},
							Sources: []string{"scope payments"},
						},
					},
				},
			},
		},
		Plan: Plan{Groups: []PlanGroup{{Profile: "deep-review", Files: []string{"pay/core.go"}}}},
	}
	return res, root
}

// TestCheckComplianceBlocks pins per-path/category compliance: open
// findings at or above threshold refuse, deferred and closed findings
// never block, wrong categories and out-of-scope paths pass, and the
// threshold ranks informational below important below critical.
func TestCheckComplianceBlocks(t *testing.T) {
	res, root := complianceFixture()
	findings := []FindingView{
		{Path: "pay/core.go", Category: "security", Severity: "critical", Status: "open"},
		{Path: "pay/core.go", Category: "logic", Severity: "important", Status: "open"},
		{Path: "pay/core.go", Category: "logic", Severity: "informational", Status: "open"},
		{Path: "pay/core.go", Category: "style", Severity: "critical", Status: "open"},
		{Path: "docs/guide.md", Category: "docs", Severity: "critical", Status: "open"},
		{Path: "pay/core.go", Category: "security", Severity: "critical", Status: "deferred"},
		{Path: "pay/core.go", Category: "security", Severity: "critical", Status: "addressed"},
	}
	blocked := CheckCompliance(res, root, findings)
	names := map[string]bool{}
	for _, b := range blocked {
		names[b.Finding.Path+"/"+b.Finding.Severity+"/"+b.Clause] = true
	}
	for _, want := range []string{
		"pay/core.go/critical/pay-block",
		"pay/core.go/important/pay-block",
		"docs/guide.md/critical/root-critical",
	} {
		if !names[want] {
			t.Errorf("missing blocker %q (got %v)", want, names)
		}
	}
	if len(blocked) != 3 {
		t.Errorf("blocked = %v, want exactly the 3 structural blockers", blocked)
	}
}

// TestCheckComplianceRenameOldPath pins the rename rule: a finding on
// the pre-rename path still evaluates against the file's clauses.
func TestCheckComplianceRenameOldPath(t *testing.T) {
	res, root := complianceFixture()
	blocked := CheckCompliance(res, root, []FindingView{
		{Path: "pay/old.go", Category: "logic", Severity: "critical", Status: "open"},
	})
	if len(blocked) != 1 || blocked[0].Clause != "pay-block" {
		t.Fatalf("blocked = %+v, want the rename-path clause", blocked)
	}
}

// TestCheckComplianceUnknownSeverity pins the conservative rule:
// severities outside the vocabulary never block.
func TestCheckComplianceUnknownSeverity(t *testing.T) {
	res, root := complianceFixture()
	blocked := CheckCompliance(res, root, []FindingView{
		{Path: "pay/core.go", Category: "security", Severity: "mystery", Status: "open"},
	})
	if len(blocked) != 0 {
		t.Fatalf("blocked = %+v, want none for unknown severity", blocked)
	}
}

// TestObservePlanPending pins the pending-until-observed rule: planned
// checks with no passing observation stay pending, and a passing
// observation for one group never satisfies another.
func TestObservePlanPending(t *testing.T) {
	const head = "aaaaaaa"
	plan := Plan{Status: PendingStatus, Groups: []PlanGroup{
		{ID: "a-group", Label: "a", Profile: "a", Checks: []string{CheckNoOpenAboveBlock, CheckHeadCurrent}},
		{ID: "b-group", Label: "b", Profile: "b", Checks: []string{CheckNoOpenAboveBlock}},
	}}
	if got := PendingChecks(PlanScopes(plan), nil, head); len(got) != 3 {
		t.Fatalf("pending with no observations = %d, want 3", len(got))
	}
	obs := []CheckObservation{{Check: CheckNoOpenAboveBlock, Group: "a-group", Head: head, Passed: true}}
	pending := PendingChecks(PlanScopes(plan), obs, head)
	if len(pending) != 2 {
		t.Fatalf("pending = %+v, want head-current for a and the block check for b", pending)
	}
	status := ObservePlan(plan, obs, head)
	if !status[0].Checks[CheckNoOpenAboveBlock] || status[1].Checks[CheckNoOpenAboveBlock] {
		t.Fatalf("status = %+v, want the observation scoped to group a", status)
	}
}

// TestRunStateRoundTrip pins the run bundle: write then read returns the
// captured inputs, and appended outcomes preserve them.
func TestRunStateRoundTrip(t *testing.T) {
	dir := roundTripBundle(t)
	loaded := mustReadRunState(t, dir)
	if loaded.BaseSHA != "abc123" || !loaded.Trusted || loaded.Name != "42" {
		t.Fatalf("loaded = %+v", loaded)
	}
	ok, aerr := AppendOutcome(dir, &CheckObservation{Check: CheckHeadCurrent, Group: "a", Passed: true, At: "2026-11-06T01:00:00Z"})
	if aerr != nil || !ok {
		t.Fatalf("AppendOutcome: ok=%v err=%v", ok, aerr)
	}
	reloaded := mustReadRunState(t, dir)
	if len(reloaded.Outcomes) != 1 || reloaded.BaseSHA != "abc123" || reloaded.Evidence.SchemaVersion != "1" {
		t.Fatalf("reloaded = %+v, want inputs preserved with one outcome", reloaded)
	}
}

// roundTripBundle writes one independent run bundle and returns its dir.
func roundTripBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), VerdictDir, StateDirName, "42")
	rs := &RunState{
		Name:         "42",
		BaseSHA:      "abc123",
		Trusted:      true,
		CapturedAt:   "2026-11-06T00:00:00Z",
		ConfigSHA256: "",
		Evidence:     Evidence{SchemaVersion: "1", BaseSHA: "abc123"},
		Resolved:     &Resolved{Plan: Plan{Status: PendingStatus}},
	}
	if err := WriteRunState(dir, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	return dir
}

// mustReadRunState reads a bundle that must exist, failing otherwise.
func mustReadRunState(t *testing.T, dir string) *RunState {
	t.Helper()
	rs, found, err := ReadRunState(dir)
	if err != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, err)
	}
	return rs
}

// TestRunStateAbsent pins the no-init degrade: a missing bundle reads
// absent without error, and appending to it appends nothing.
func TestRunStateAbsent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, VerdictDir, StateDirName, "42")
	if _, found, err := ReadRunState(dir); err != nil || found {
		t.Fatalf("ReadRunState absent: found=%v err=%v", found, err)
	}
	if ok, err := AppendOutcome(dir, &CheckObservation{Check: CheckHeadCurrent}); ok || err != nil {
		t.Fatalf("absent append must return (false, nil): ok=%v err=%v", ok, err)
	}
	if _, err := os.Lstat(filepath.Join(root, VerdictDir)); !os.IsNotExist(err) {
		t.Fatal("AppendOutcome created state for an absent bundle")
	}
}

// TestVerifyRunState pins the integrity binding with an independently
// authored bundle: a fresh write verifies, an appended outcome keeps it
// valid, an edited input refuses controls-stale, a deleted run bundle
// refuses instead of degrading, and a missing directory reads legacy.
func TestVerifyRunState(t *testing.T) {
	_, dir := verifiedRunBundle(t)
	verified, ref := VerifyRunState(dir)
	if ref != nil || verified == nil {
		t.Fatalf("VerifyRunState fresh = %v, %v; want verified", verified, ref)
	}
	if !verified.Verified() {
		t.Fatal("fresh bundle reports unverified")
	}
	ok, err := AppendOutcome(dir, &CheckObservation{Check: CheckHeadCurrent, Group: "g", Passed: true, At: "2026-11-06T01:00:00Z"})
	if err != nil || !ok {
		t.Fatalf("AppendOutcome: ok=%v err=%v", ok, err)
	}
	if _, ref := VerifyRunState(dir); ref != nil {
		t.Fatalf("valid outcome append broke verification: %v", ref)
	}
}

// TestVerifyRunStateRefusesEditedInputs pins the tamper rule: an edited
// captured input refuses controls-stale instead of passing as verified.
func TestVerifyRunStateRefusesEditedInputs(t *testing.T) {
	_, dir := verifiedRunBundle(t)
	rewriteRunState(t, dir, func(rs *RunState) { rs.BaseSHA = "tampered" })
	if _, ref := VerifyRunState(dir); ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("edited inputs verify = %v, want controls-stale", ref)
	}
}

// TestVerifyRunStateRefusesDeletedBundle pins the deletion rule: a bundle
// directory carrying the controls snapshot without its run bundle is drift,
// not legacy.
func TestVerifyRunStateRefusesDeletedBundle(t *testing.T) {
	_, dir := verifiedRunBundle(t)
	if err := os.Remove(filepath.Join(dir, runStateFileName)); err != nil {
		t.Fatal(err)
	}
	if _, ref := VerifyRunState(dir); ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("deleted run bundle verify = %v, want controls-stale", ref)
	}
}

// TestVerifyRunStateReadsMissingAsLegacy pins the legacy degrade: a
// directory with no snapshot and no bundle reads (nil, nil).
func TestVerifyRunStateReadsMissingAsLegacy(t *testing.T) {
	root, _ := verifiedRunBundle(t)
	if got, ref := VerifyRunState(filepath.Join(root, "missing")); got != nil || ref != nil {
		t.Fatalf("missing bundle verify = %v, %v; want legacy (nil, nil)", got, ref)
	}
}

// verifiedRunBundle seeds a trusted config, the run bundle, and its controls
// snapshot, and returns the repository root with the bundle directory.
func verifiedRunBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, VerdictDir, StateDirName, "7")
	writeTrustedConfig(t, root, `{"schema_version":"1"}`)
	rs := &RunState{
		Name:       "7",
		BaseSHA:    "abc123",
		Trusted:    true,
		CapturedAt: "2026-11-06T00:00:00Z",
		Evidence:   Evidence{SchemaVersion: "1", BaseSHA: "abc123"},
		Resolved:   &Resolved{Plan: Plan{Status: PendingStatus}},
	}
	if err := WriteRunState(dir, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: root, BaseSHA: "abc123", AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	return root, dir
}

// rewriteRunState mutates the persisted bundle without updating its controls
// snapshot, which is what a tamper or an out-of-band edit looks like.
func rewriteRunState(t *testing.T, dir string, mutate func(*RunState)) {
	t.Helper()
	rs, found, readErr := ReadRunState(dir)
	if readErr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, readErr)
	}
	mutate(rs)
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		t.Fatalf("encode run state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, runStateFileName), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInventoryHashes pins the capture-time manifest: every support
// byte hashes by snapshot path with the state-dir prefix shed, so the
// check recomputes the same map from the verified snapshot.
func TestInventoryHashes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, VerdictDir, StateDirName, "7")
	writeTrustedConfig(t, root, `{"schema_version":"1"}`)
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: root, BaseSHA: "abc123", AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	got := InventoryHashes(snap)
	if len(got) != 1 {
		t.Fatalf("hashes = %v, want the one used entry", got)
	}
	sum := sha256.Sum256([]byte(`{"schema_version":"1"}`))
	if want := hex.EncodeToString(sum[:]); got["config.json"] != want {
		t.Fatalf("hash config.json = %q, want %q", got["config.json"], want)
	}
}

// writeTrustedConfig seeds one tracked config file under a fresh root.
func writeTrustedConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, VerdictDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, VerdictDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
