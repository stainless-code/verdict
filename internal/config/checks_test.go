package config

import (
	"os"
	"path/filepath"
	"testing"

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
	plan := Plan{Status: PendingStatus, Groups: []PlanGroup{
		{Profile: "a", Checks: []string{CheckNoOpenAboveBlock, CheckHeadCurrent}},
		{Profile: "b", Checks: []string{CheckNoOpenAboveBlock}},
	}}
	if got := PendingChecks(plan, nil); len(got) != 3 {
		t.Fatalf("pending with no observations = %d, want 3", len(got))
	}
	obs := []CheckObservation{{Check: CheckNoOpenAboveBlock, Group: "a", Passed: true}}
	pending := PendingChecks(plan, obs)
	if len(pending) != 2 {
		t.Fatalf("pending = %+v, want head-current for a and the block check for b", pending)
	}
	status := ObservePlan(plan, obs)
	if !status[0].Checks[CheckNoOpenAboveBlock] || status[1].Checks[CheckNoOpenAboveBlock] {
		t.Fatalf("status = %+v, want the observation scoped to group a", status)
	}
}

// TestRunStateRoundTrip pins the run bundle: write then read returns the
// captured inputs, and appended outcomes preserve them.
func TestRunStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	rs := &RunState{
		BaseSHA:      "abc123",
		Trusted:      true,
		CapturedAt:   "2026-11-06T00:00:00Z",
		ConfigSHA256: "deadbeef",
		Evidence:     Evidence{SchemaVersion: "1", BaseSHA: "abc123"},
		Resolved:     &Resolved{Plan: Plan{Status: PendingStatus}},
	}
	if err := WriteRunState(root, "42", rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	loaded, found, rerr := ReadRunState(root, "42")
	if rerr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, rerr)
	}
	if loaded.BaseSHA != "abc123" || !loaded.Trusted || loaded.ConfigSHA256 != "deadbeef" {
		t.Fatalf("loaded = %+v", loaded)
	}
	ok, aerr := AppendOutcome(root, "42", &CheckObservation{Check: CheckHeadCurrent, Group: "a", Passed: true, At: "2026-11-06T01:00:00Z"})
	if aerr != nil || !ok {
		t.Fatalf("AppendOutcome: ok=%v err=%v", ok, aerr)
	}
	reloaded, found, rerr := ReadRunState(root, "42")
	if rerr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, rerr)
	}
	if len(reloaded.Outcomes) != 1 || reloaded.BaseSHA != "abc123" || reloaded.Evidence.SchemaVersion != "1" {
		t.Fatalf("reloaded = %+v, want inputs preserved with one outcome", reloaded)
	}
}

// TestRunStateAbsent pins the no-init degrade: a missing bundle reads
// absent without error, and appending to it appends nothing.
func TestRunStateAbsent(t *testing.T) {
	root := t.TempDir()
	if _, found, err := ReadRunState(root, "42"); err != nil || found {
		t.Fatalf("ReadRunState absent: found=%v err=%v", found, err)
	}
	if ok, err := AppendOutcome(root, "42", &CheckObservation{Check: CheckHeadCurrent}); ok || err != nil {
		t.Fatalf("absent append must return (false, nil): ok=%v err=%v", ok, err)
	}
	if _, err := os.Lstat(filepath.Join(root, VerdictDir)); !os.IsNotExist(err) {
		t.Fatal("AppendOutcome created state for an absent bundle")
	}
}

// TestSnapshotWorktreeInventory pins the local inventory read: tracked
// files and override fragments snapshot from the worktree, absent files
// read absent.
func TestSnapshotWorktreeInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, VerdictDir, "overrides"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ConfigPath), []byte(`{"schema_version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, VerdictDir, "overrides", "extra.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := SnapshotWorktreeInventory(root)
	if string(got[ConfigPath]) != `{"schema_version":"1"}` {
		t.Fatalf("config = %q", got[ConfigPath])
	}
	if string(got[filepath.Join(OverridesDir, "extra.md")]) != "x" {
		t.Fatalf("override = %q", got[filepath.Join(OverridesDir, "extra.md")])
	}
	if _, ok := got[KBPath]; ok {
		t.Fatalf("absent kb snapshotted: %q", got[KBPath])
	}
}
