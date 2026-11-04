package archtest

// Repository guard for the ci-complete gate declaration: the workflow names
// once which jobs block a merge (required) and which only report (advisory),
// and this test fails the build when that declaration drifts from the real
// job graph. Without it, adding a job would leave the gate silently optional
// and a platform-only failure could not be told apart from a merge blocker.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ciGate is the gate contract read out of the workflow text.
type ciGate struct {
	jobs           []string // every job id except ci-complete
	needs          []string // the ci-complete needs list
	required       []string // REQUIRED_JOBS: a merge blocker
	macOS          []string // MACOS_JOBS: required lanes that skip only on a pull request
	advisoryEvents []string // MACOS_ADVISORY_EVENTS: events where a macOS lane only reports
	advisory       []string // ADVISORY_JOBS: reports, never blocks
}

// TestCICompleteGateDeclaration pins the declaration to the job graph.
func TestCICompleteGateDeclaration(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	gate := parseCIGate(t, string(raw))
	checkGateCoverage(t, &gate)
	checkGateLanes(t, &gate)
	checkGateWiring(t, string(raw))
}

func checkGateCoverage(t *testing.T, gate *ciGate) {
	t.Helper()
	if missing := diff(gate.jobs, gate.needs); len(missing) != 0 {
		t.Errorf("jobs missing from ci-complete needs (an unlisted job is silently optional): %v", missing)
	}
	if extra := diff(gate.needs, gate.jobs); len(extra) != 0 {
		t.Errorf("ci-complete needs lists unknown jobs: %v", extra)
	}
	declared := union(gate.required, gate.advisory)
	if missing := diff(declared, gate.needs); len(missing) != 0 {
		t.Errorf("declared lanes absent from needs (the gate could not see them): %v", missing)
	}
	if extra := diff(gate.needs, declared); len(extra) != 0 {
		t.Errorf("needs jobs with no required/advisory lane: %v", extra)
	}
}

func checkGateLanes(t *testing.T, gate *ciGate) {
	t.Helper()
	if len(gate.required) == 0 {
		t.Fatal("REQUIRED_JOBS is empty: the gate would pass with no merge blocker")
	}
	checkNoLaneDeclaredTwice(t, gate)
	if !slices.Contains(gate.required, "go-windows") {
		t.Errorf("go-windows is not required any more (required = %v)", gate.required)
	}
	checkMacOSLanesAreRequired(t, gate)
}

// checkNoLaneDeclaredTwice fails when one lane sits in both lists, which would
// leave the gate reading two rules for it.
func checkNoLaneDeclaredTwice(t *testing.T, gate *ciGate) {
	t.Helper()
	for _, job := range union(gate.required, gate.advisory) {
		if slices.Contains(gate.required, job) && slices.Contains(gate.advisory, job) {
			t.Errorf("%s is both required and advisory", job)
		}
	}
}

// checkMacOSLanesAreRequired pins the skip exception to real merge blockers: a
// MACOS_JOBS entry the gate does not require would hand that lane two rules,
// and one no job answers to would make the exception a no-op.
func checkMacOSLanesAreRequired(t *testing.T, gate *ciGate) {
	t.Helper()
	if len(gate.macOS) == 0 {
		t.Fatal("MACOS_JOBS is empty: the gate would reject the macOS skip on every PR")
	}
	for _, job := range gate.macOS {
		if !slices.Contains(gate.required, job) {
			t.Errorf("%s is a macOS lane but not required (required = %v)", job, gate.required)
		}
		if !slices.Contains(gate.jobs, job) {
			t.Errorf("MACOS_JOBS names %s, which is no job in this workflow", job)
		}
	}
}

// checkGateWiring proves the gate step still reads the declaration: a step
// that dropped the binding would silently restore the old static gate.
func checkGateWiring(t *testing.T, workflow string) {
	t.Helper()
	for _, want := range []string{
		"$REQUIRED_JOBS", "$MACOS_JOBS", "$MACOS_ADVISORY_EVENTS", "$EVENT",
		"EVENT: ${{ github.event_name }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow no longer reads %s", want)
		}
	}
}

// parseCIGate reads the job ids and the ci-complete declaration out of the
// workflow text. The parse is line-oriented on purpose: pulling a YAML
// dependency into the repo for one guard would cost more than it saves, and
// every shape it relies on is asserted by the checks above.
func parseCIGate(t *testing.T, workflow string) ciGate {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start, end := ciCompleteSpan(t, lines)
	block := lines[start:end]
	return ciGate{
		jobs:           jobIDs(lines),
		needs:          parseNeeds(t, block),
		required:       parseDeclared(t, block, "REQUIRED_JOBS"),
		macOS:          parseDeclared(t, block, "MACOS_JOBS"),
		advisoryEvents: parseDeclared(t, block, "MACOS_ADVISORY_EVENTS"),
		advisory:       parseDeclared(t, block, "ADVISORY_JOBS"),
	}
}

// jobIDs lists the workflow's job ids, ci-complete excluded.
func jobIDs(lines []string) []string {
	start := slices.Index(lines, "jobs:")
	if start < 0 {
		return nil
	}
	var out []string
	for _, line := range lines[start+1:] {
		if endsJobsSection(line) {
			break
		}
		if name, ok := jobID(line); ok && name != "ci-complete" {
			out = append(out, name)
		}
	}
	return out
}

func endsJobsSection(line string) bool {
	return !strings.HasPrefix(line, " ") && strings.TrimSpace(line) != ""
}

// ciCompleteSpan returns the half-open line range of the ci-complete job.
func ciCompleteSpan(t *testing.T, lines []string) (int, int) {
	t.Helper()
	start := slices.IndexFunc(lines, func(line string) bool {
		name, ok := jobID(line)
		return ok && name == "ci-complete"
	})
	if start < 0 {
		t.Fatal("no ci-complete job in the workflow")
	}
	for i := start + 1; i < len(lines); i++ {
		if _, ok := jobID(lines[i]); ok {
			return start, i
		}
	}
	return start, len(lines)
}

func jobID(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "  ")
	if !ok || strings.HasPrefix(rest, " ") {
		return "", false
	}
	name, ok := strings.CutSuffix(rest, ":")
	if !ok || name == "" || strings.ContainsAny(name, " #:\"'") {
		return "", false
	}
	return name, true
}

// needsEnd closes the bracketed needs list at the gate's step indent.
const needsEnd = "      ]"

// parseNeeds reads the bracketed needs list from the ci-complete block.
func parseNeeds(t *testing.T, block []string) []string {
	t.Helper()
	for i, line := range block {
		if strings.TrimSpace(line) == "needs:" {
			return needsTokens(t, block[i+1:])
		}
	}
	t.Fatal("ci-complete has no needs block")
	return nil
}

// needsTokens reads the list that follows `needs:`.
func needsTokens(t *testing.T, list []string) []string {
	t.Helper()
	var out []string
	for _, line := range list {
		if line == needsEnd {
			return out
		}
		out = append(out, bareNames(line)...)
	}
	t.Fatal("ci-complete needs list is unterminated")
	return nil
}

// parseDeclared reads one space-separated lane declaration from the block.
func parseDeclared(t *testing.T, block []string, key string) []string {
	t.Helper()
	for _, line := range block {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), key+":")
		if !ok {
			continue
		}
		return bareNames(strings.Trim(strings.TrimSpace(value), `"'`))
	}
	t.Fatalf("ci-complete declares no %s", key)
	return nil
}

func bareNames(line string) []string {
	var out []string
	for field := range strings.FieldsSeq(line) {
		if field = strings.Trim(field, `",`); field != "" && field != "[" && field != "]" {
			out = append(out, field)
		}
	}
	return out
}

func diff(want, have []string) []string {
	var out []string
	for _, entry := range want {
		if !slices.Contains(have, entry) {
			out = append(out, entry)
		}
	}
	return out
}

// union merges both lists without duplicates.
func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, entry := range b {
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out
}
