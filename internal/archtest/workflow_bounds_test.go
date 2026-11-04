package archtest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const maxJobMinutes = 60

// TestWorkflowJobBudgets fails when a job declares no timeout, or one outside
// [1, maxJobMinutes].
func TestWorkflowJobBudgets(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !isWorkflowFile(entry.Name()) {
			continue
		}
		checkWorkflowBudgets(t, filepath.Join(dir, entry.Name()))
		checked++
	}
	if checked == 0 {
		t.Fatal("no workflow files found: the guard would pass on nothing")
	}
}

func checkWorkflowBudgets(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	name := filepath.Base(path)
	for id, block := range workflowJobs(t, name, string(raw)) {
		checkJobBudget(t, name, id, block)
	}
}

func isWorkflowFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

func checkJobBudget(t *testing.T, workflow, job string, block []string) {
	t.Helper()
	const key = "    timeout-minutes: "
	for _, line := range block {
		value, ok := strings.CutPrefix(line, key)
		if !ok {
			continue
		}
		minutes, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || minutes < 1 || minutes > maxJobMinutes {
			t.Errorf("%s: job %q declares timeout-minutes %q; want a literal 1..%d",
				workflow, job, value, maxJobMinutes)
		}
		return
	}
	t.Errorf("%s: job %q declares no timeout-minutes (a wedged step burns the 360-minute default)",
		workflow, job)
}

// TestCICancelsSupersededRuns pins cancel-in-progress and the main/tag
// exemption.
func TestCICancelsSupersededRuns(t *testing.T) {
	path := filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block := topLevelBlock(strings.Split(string(raw), "\n"), "concurrency")
	if len(block) == 0 {
		t.Fatal("ci.yml declares no workflow-level concurrency: superseded runs keep queueing")
	}
	joined := strings.Join(block, "\n")
	if !strings.Contains(joined, "cancel-in-progress:") {
		t.Fatal("ci.yml concurrency has no cancel-in-progress: the group only queues")
	}
	if strings.Contains(joined, "cancel-in-progress: false") {
		t.Fatal("ci.yml never cancels superseded runs; a full run queues behind the previous one")
	}
	prOnly := strings.Contains(joined, "pull_request")
	exemptsMainAndTags := strings.Contains(joined, "refs/heads/main") && strings.Contains(joined, "refs/tags/")
	if !prOnly && !exemptsMainAndTags {
		t.Errorf("ci.yml concurrency does not exempt main and tag refs: %s", joined)
	}
}

func topLevelBlock(lines []string, key string) []string {
	start := lineIndex(lines, key+":")
	if start < 0 {
		return nil
	}
	return indentedLines(lines[start+1:])
}

func indentedLines(lines []string) []string {
	var block []string
	for _, line := range lines {
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		block = append(block, line)
	}
	return block
}

func lineIndex(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}

func workflowJobs(t *testing.T, name, workflow string) map[string][]string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	jobs := collectJobs(lines[startOfJobs(t, name, lines)+1:])
	if len(jobs) == 0 {
		t.Fatalf("%s: no jobs parsed; the guard would pass on nothing", name)
	}
	return jobs
}

func collectJobs(lines []string) map[string][]string {
	jobs := make(map[string][]string)
	current := ""
	for _, line := range lines {
		if endsJobsSection(line) {
			break
		}
		current = appendJobLine(jobs, current, line)
	}
	return jobs
}

func appendJobLine(jobs map[string][]string, current, line string) string {
	if id, ok := jobID(line); ok {
		jobs[id] = nil
		return id
	}
	if current != "" {
		jobs[current] = append(jobs[current], line)
	}
	return current
}

func startOfJobs(t *testing.T, name string, lines []string) int {
	t.Helper()
	if i := lineIndex(lines, "jobs:"); i >= 0 {
		return i
	}
	t.Fatalf("%s: no jobs: key", name)
	return -1
}
