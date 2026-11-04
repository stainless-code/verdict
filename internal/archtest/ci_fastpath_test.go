package archtest

// Repository guard for the docs-only fast path: `changes` decides which lanes a
// PR buys, so a lane that dropped its dependency on it, a fast lane that picked
// one up, or a gate that accepted a skip on a full run would all widen the bar
// in silence. The decision table runs the real classifier script; the wiring is
// pinned in the workflow text.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fastPathCondition is the one gate the fast path is allowed to use.
const fastPathCondition = "    if: needs.changes.outputs.full == 'true'"

// fastPathGated lanes never run for a docs-only change.
var fastPathGated = []string{
	"lint", "installer", "installers", "installers-macos", "installers-windows",
	"go", "go-macos", "go-windows", "cross", "evals", "evals-macos",
	"release-dry-run", "ts-client", "fuzz", "bench",
}

// fastPathFree lanes run for every change, docs-only included.
var fastPathFree = []string{"format", "docs", "audit"}

// changesCase is one classifier input and the verdict it must produce.
type changesCase struct {
	name  string
	want  string
	paths []string
}

// TestCIChangesDecision drives scripts/ci-changes.sh, the program ci.yml pipes
// the PR diff into.
func TestCIChangesDecision(t *testing.T) {
	t.Parallel()
	script := filepath.Join(moduleRoot(t), "scripts", "ci-changes.sh")
	sh := posixShell(t)
	for _, tc := range changesCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classify(t, sh, script, tc.paths); got != tc.want {
				t.Errorf("classify %q = %q; want %q", tc.paths, got, tc.want)
			}
		})
	}
}

// changesCases is the decision table: what the fast path may skip, and the
// markdown it may never skip because a test or a golden reads it.
func changesCases() []changesCase {
	return []changesCase{
		{
			name:  "docs site alone",
			want:  "full=false",
			paths: []string{"apps/docs/pages/index.mdx", "apps/docs/blume.config.ts"},
		},
		{name: "nested docs page", want: "full=false", paths: []string{"apps/docs/content/guides/a/b.md"}},
		{name: "changeset alone", want: "full=false", paths: []string{".changeset/x.md"}},
		{
			name:  "changelog and issue template",
			want:  "full=false",
			paths: []string{"CHANGELOG.md", ".github/ISSUE_TEMPLATE/bug.yml"},
		},
		{name: "skill prompt", want: "full=true", paths: []string{"skills/verdict/prompts/review.md"}},
		{name: "skill entrypoint", want: "full=true", paths: []string{"skills/verdict/SKILL.md"}},
		{name: "notice", want: "full=true", paths: []string{"NOTICE.md"}},
		{name: "root readme", want: "full=true", paths: []string{"README.md"}},
		{name: "manual", want: "full=true", paths: []string{"docs/manual.md"}},
		{name: "workflow", want: "full=true", paths: []string{".github/workflows/ci.yml"}},
		{name: "docs prefix sibling", want: "full=true", paths: []string{"apps/docs-archive/page.md"}},
		{
			name:  "mixed docs and go",
			want:  "full=true",
			paths: []string{"apps/docs/pages/index.mdx", "internal/cli/cli.go"},
		},
		{name: "empty diff", want: "full=true"},
	}
}

// posixShell returns the shell that runs the classifier, skipping where the
// script cannot run.
func posixShell(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed: the classifier is a POSIX shell script")
	}
	return sh
}

// classify pipes one path list through the classifier and returns its verdict.
func classify(t *testing.T, sh, script string, paths []string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), sh, script)
	cmd.Stdin = strings.NewReader(changesInput(paths))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("classify %q: %v\n%s", paths, err, out)
	}
	return strings.TrimSpace(string(out))
}

// changesInput renders classifier stdin: one path per line, nothing for an
// empty diff.
func changesInput(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return strings.Join(paths, "\n") + "\n"
}

// TestCIFastPathGating pins the `changes` contract and each lane's side of it.
func TestCIFastPathGating(t *testing.T) {
	workflow := readCIWorkflow(t)
	jobs := workflowJobs(t, "ci.yml", workflow)
	checkFastPathCoverage(t, jobs)
	checkChangesJob(t, jobs)
	checkGatedLanes(t, jobs)
	checkFreeLanes(t, jobs)
	checkGateAcceptsDocsOnlySkip(t, workflow)
}

// readCIWorkflow returns ci.yml as text.
func readCIWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// checkFastPathCoverage fails when a lane is in neither list, so a new job
// forces the fast-path decision instead of defaulting into it.
func checkFastPathCoverage(t *testing.T, jobs map[string][]string) {
	t.Helper()
	declared := append(slices.Clone(fastPathGated), fastPathFree...)
	for _, lane := range declared {
		if _, ok := jobs[lane]; !ok {
			t.Errorf("%s is classified for the fast path but ci.yml has no such job", lane)
		}
	}
	for lane := range jobs {
		if lane == "changes" || lane == "ci-complete" || slices.Contains(declared, lane) {
			continue
		}
		t.Errorf("job %q is in neither fastPathGated nor fastPathFree: classify it", lane)
	}
}

// checkChangesJob pins the deciding job: it must publish `full` from the
// script's verdict.
func checkChangesJob(t *testing.T, jobs map[string][]string) {
	t.Helper()
	block := jobs["changes"]
	if block == nil {
		t.Fatal("ci.yml has no changes job: the fast path has no decision to make")
	}
	joined := strings.Join(block, "\n")
	for _, want := range []string{
		"      full: ${{ steps.classify.outputs.full }}",
		"scripts/ci-changes.sh",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes job no longer publishes its verdict: missing %q", want)
		}
	}
}

// checkGatedLanes pins the lanes that a docs-only change must not buy.
func checkGatedLanes(t *testing.T, jobs map[string][]string) {
	t.Helper()
	for _, lane := range fastPathGated {
		joined := strings.Join(jobs[lane], "\n")
		if !strings.Contains(joined, fastPathCondition) {
			t.Errorf("%s lost %q: a docs-only PR would buy it", lane, strings.TrimSpace(fastPathCondition))
		}
		if !strings.Contains(joined, "    needs: changes") {
			t.Errorf("%s gates on needs.changes but does not depend on changes", lane)
		}
	}
}

// checkFreeLanes pins the lanes that must still run for every change.
func checkFreeLanes(t *testing.T, jobs map[string][]string) {
	t.Helper()
	for _, lane := range fastPathFree {
		if strings.Contains(strings.Join(jobs[lane], "\n"), "needs.changes.outputs.full") {
			t.Errorf("%s must still run for a docs-only change", lane)
		}
	}
}

// checkGateAcceptsDocsOnlySkip pins the single tolerated skip: a required lane
// skipped when `changes` proved the diff docs-only, never `changes` itself.
func checkGateAcceptsDocsOnlySkip(t *testing.T, workflow string) {
	t.Helper()
	for _, want := range []string{
		"REQUIRED_JOBS: changes ",
		"FULL: ${{ needs.changes.outputs.full }}",
		`--arg full "$FULL"`,
		`"skipped"`,
		`$k != "changes"`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("ci-complete no longer holds the fast-path contract: missing %q", want)
		}
	}
}
