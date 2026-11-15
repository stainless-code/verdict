package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// parallelAppendWait is the wait the parallel-append test gives the store lock.
// Sixteen waiters can starve one past the operator's five seconds on a loaded
// machine, and the test grades the rows kept, not the budget.
const parallelAppendWait = time.Minute

// TestAppendRowKeepsEveryParallelAppend proves the append serializes on the
// store's lock: sixteen appends started together each keep their row, which the
// unlocked read-modify-write dropped to one or two. Every append also brings
// `.verdict/.gitignore` to the contract while it holds the lock, so the sixteen
// writers leave one copy of each lock line rather than racing each other's
// atomic rewrite of that one file.
func TestAppendRowKeepsEveryParallelAppend(t *testing.T) {
	restore := lockWait
	lockWait = parallelAppendWait
	t.Cleanup(func() { lockWait = restore })
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	const appends = 16
	appendInParallel(t, dir, appends)
	want := make([]string, 0, appends)
	for i := range appends {
		want = append(want, fmt.Sprintf(`{"id":"rec-%02d","status":"active"}`, i))
	}
	if got := storedLines(t, dir); !slices.Equal(got, want) {
		t.Fatalf("parallel appends kept %d rows; want %d\n%s", len(got), appends, strings.Join(got, "\n"))
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".verdict", ".gitignore"))
	if err != nil {
		t.Fatalf("read ignore: %v", err)
	}
	if got := strings.Count(string(ignore), "kb.jsonl.lock\n"); got != 1 {
		t.Fatalf("ignore carries the lock line %d times; want 1\n%s", got, ignore)
	}
}

// appendInParallel starts appends distinct rows at once and fails on any error.
func appendInParallel(t *testing.T, dir string, appends int) {
	t.Helper()
	errs := make([]error, appends)
	var wg sync.WaitGroup
	for i := range appends {
		wg.Go(func() {
			errs[i] = AppendRow(dir, fmt.Sprintf(`{"id":"rec-%02d","status":"active"}`+"\n", i))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

// storedLines reads the store back as its sorted row lines.
func storedLines(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "kb.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	slices.Sort(lines)
	return lines
}
