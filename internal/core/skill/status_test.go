package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/skill"
)

// TestStatusFreshInstallInSync pins the in-sync keep with no diffs.
func TestStatusFreshInstallInSync(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	st, ref := skill.Status(context.Background(), req, true)
	if ref != nil {
		t.Fatalf("Status: %v", ref)
	}
	if !st.InSync || st.Plan.Status != skill.StatusKeep {
		t.Fatalf("fresh install status = %+v, want an in-sync keep", st)
	}
	if len(st.Diffs) != 0 {
		t.Fatalf("fresh install rendered %d diffs, want none", len(st.Diffs))
	}
}

// TestStatusReportsDriftWritesNothing pins the read-only contract: a hand edit
// reads update with the edit in its unified diff, and the tree is unchanged.
// TestStatusReportsDriftWritesNothing pins the read-only drift report.
//
//nolint:cyclop,revive // the test walks the drift arm
func TestStatusReportsDriftWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("hand edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, target)

	st, ref := skill.Status(context.Background(), req, true)
	if ref != nil {
		t.Fatalf("Status: %v", ref)
	}
	if st.InSync {
		t.Fatal("hand-edited target reads in sync")
	}
	if st.Plan.Status != skill.StatusUpdate {
		t.Fatalf("status = %q, want update", st.Plan.Status)
	}
	if !slices.Equal(st.Plan.Changed, []string{"SKILL.md"}) {
		t.Fatalf("changed = %v, want [SKILL.md]", st.Plan.Changed)
	}
	if len(st.Diffs) != 1 || st.Diffs[0].Path != "SKILL.md" {
		t.Fatalf("diffs = %+v, want one for SKILL.md", st.Diffs)
	}
	if !strings.Contains(st.Diffs[0].Body, "-hand edited\n") {
		t.Fatalf("diff does not show the removed hand edit:\n%s", st.Diffs[0].Body)
	}
	if !strings.Contains(st.Diffs[0].Body, "+---\n") {
		t.Fatalf("diff does not show the embedded first line as an addition:\n%s", st.Diffs[0].Body)
	}
	if st.Diffs[0].Truncated {
		t.Fatal("small edit reported truncated")
	}
	after := treeSnapshot(t, target)
	if !mapsEqual(before, after) {
		t.Fatal("Status wrote to the target tree")
	}
}

// TestStatusBlockedWithoutReceipt pins the foreign target as drift.
func TestStatusBlockedWithoutReceipt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "foreign.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, ref := skill.Status(context.Background(), req, false)
	if ref != nil {
		t.Fatalf("Status: %v", ref)
	}
	if st.InSync || st.Plan.Status != skill.StatusBlocked {
		t.Fatalf("foreign target status = %+v, want blocked drift", st)
	}
}

// TestStatusReportsEscapingPayloadDirWithoutFollowingTheLink pins that status
// refuses the escape through the root instead of reading the linked bytes.
//
//nolint:revive // the test walks the escape path
func TestStatusReportsEscapingPayloadDirWithoutFollowingTheLink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	outside := filepath.Join(dir, "outside")
	outsideFile := filepath.Join(outside, "review.md")
	writeTestFile(t, outsideFile, "outside bytes\n")
	if err := os.RemoveAll(filepath.Join(target, "prompts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "prompts")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	_, ref := skill.Status(context.Background(), req, true)
	if ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("status refusal = %v, want skill-foreign-conflict", ref)
	}
	if !strings.Contains(ref.Message, "path escapes from parent") {
		t.Fatalf("status message = %q, want the root escape named", ref.Message)
	}
	if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "outside bytes\n" {
		t.Fatalf("outside file = %q, %v; want it untouched", got, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
		t.Fatalf("outside dir = %v, %v; want only review.md", entries, err)
	}
}

// TestStatusAgentsInArgvOrder pins dedupe and argv order.
//
//nolint:revive // the test walks the multi-agent arm
func TestStatusAgentsInArgvOrder(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	for _, agent := range []string{"claude", "cursor"} {
		at := newRequest(t)
		at.Env = map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}
		at.Agent = agent
		if _, ref := skill.Install(context.Background(), at); ref != nil {
			t.Fatalf("seed %s install: %v", agent, ref)
		}
	}
	set, ref := skill.StatusAgents(context.Background(), req, []string{"cursor", "claude", "cursor"}, false)
	if ref != nil {
		t.Fatalf("StatusAgents: %v", ref)
	}
	if want := []string{"cursor", "claude"}; !slices.Equal(set.Agents, want) {
		t.Fatalf("agents = %v, want %v", set.Agents, want)
	}
	if len(set.Targets) != 2 || !set.InSync {
		t.Fatalf("status set = %+v, want two in-sync targets", set)
	}
	if set.Targets[0].Plan.Agent != "cursor" || set.Targets[1].Plan.Agent != "claude" {
		t.Fatalf("target order = %s, %s; want cursor, claude", set.Targets[0].Plan.Agent, set.Targets[1].Plan.Agent)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// TestStatusMissingTargetDiffsEveryFile pins that a create plan has one diff
// per changed file, each against empty.
//
//nolint:cyclop,gocognit,revive // the create-plan assertions read as one block
func TestStatusMissingTargetDiffsEveryFile(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = t.TempDir()
	st, ref := skill.Status(context.Background(), req, true)
	if ref != nil {
		t.Fatalf("Status: %v", ref)
	}
	if st.Plan.Status != skill.StatusCreate {
		t.Fatalf("status = %q, want create", st.Plan.Status)
	}
	if len(st.Plan.Changed) == 0 {
		t.Fatal("create plan lists no changed files")
	}
	if len(st.Diffs) != len(st.Plan.Changed) {
		t.Fatalf("diffs = %d, want one per changed file (%d)", len(st.Diffs), len(st.Plan.Changed))
	}
	for i, diff := range st.Diffs {
		if diff.Path != st.Plan.Changed[i] {
			t.Fatalf("diff %d path = %q, want %q", i, diff.Path, st.Plan.Changed[i])
		}
		if diff.Body == "" || diff.Truncated {
			continue
		}
		if !strings.Contains(diff.Body, "--- a/"+diff.Path+"\n") || !strings.Contains(diff.Body, "+++ b/"+diff.Path+"\n") {
			t.Fatalf("diff %d lacks the file header:\n%s", i, diff.Body)
		}
		if strings.Contains(diff.Body, "\n-") {
			t.Fatalf("diff %d removes lines from a missing target:\n%s", i, diff.Body)
		}
	}
	if !strings.Contains(st.Diffs[0].Body, "@@ -0,0 ") {
		t.Fatalf("first diff is not an addition hunk:\n%s", st.Diffs[0].Body)
	}
}

// TestStatusSurfacesAnUnopenableTarget pins that a target that exists but
// cannot be opened refuses typed instead of reporting an empty diff list.
func TestStatusSurfacesAnUnopenableTarget(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = t.TempDir()
	target := filepath.Join(req.Dir, "verdict")
	if err := os.WriteFile(target, []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := skill.Status(context.Background(), req, true)
	if ref == nil || ref.Code != "skill-read-failed" {
		t.Fatalf("refusal = %v, want skill-read-failed", ref)
	}
	if !strings.Contains(ref.Message, "cannot read "+target) {
		t.Fatalf("message = %q, want it to name %s", ref.Message, target)
	}
}
