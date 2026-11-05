package corpuscore

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestSeededDiffApplies proves each full.diff applies to its snapshots.
func TestSeededDiffApplies(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable: cannot prove full.diff applies")
	}
	for _, dir := range caseDirs(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			before, after := snapshots(t, dir)
			diff := readSnapshot(t, filepath.Join(dir, "full.diff"))
			_, touched := parseDiff(t, diff)
			if len(touched) == 0 {
				t.Fatalf("full.diff touches no file")
			}
			repo := applyDiff(t, before, diff)
			checkPatchedTree(t, repo, after, touched)
		})
	}
}

func applyDiff(t *testing.T, before map[string]string, diff string) string {
	t.Helper()
	repo := t.TempDir()
	for path, body := range before {
		writeSnapshot(t, filepath.Join(repo, path), body)
	}
	runGit(t, repo, nil, "init", "-q")
	runGit(t, repo, nil, "add", "-A")
	runGit(t, repo, nil, "commit", "-qm", "seed")
	runGit(t, repo, strings.NewReader(diff), "apply", "--check", "-")
	runGit(t, repo, strings.NewReader(diff), "apply", "-")
	return repo
}

func checkPatchedTree(t *testing.T, repo string, after map[string]string, touched []string) {
	t.Helper()
	for path, want := range after {
		if !slices.Contains(touched, path) {
			t.Errorf("%s has an after snapshot but full.diff never touches it", path)
			continue
		}
		if got := readSnapshot(t, filepath.Join(repo, path)); got != want {
			t.Errorf("%s after the patch does not equal the checked-in after snapshot", path)
		}
	}
}

// TestGroundTruthLinesAreAdded proves each bug line is added by its diff.
func TestGroundTruthLinesAreAdded(t *testing.T) {
	for _, dir := range caseDirs(t) {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			c, ref := loadCase(t, filepath.Join(dir, "case.json"))
			if ref != nil {
				t.Fatalf("%s: %v", name, ref)
			}
			rows, _ := parseDiff(t, readSnapshot(t, filepath.Join(dir, "full.diff")))
			checkGroundTruth(t, c.Kind, c.Bugs, rows)
		})
	}
}

func checkGroundTruth(t *testing.T, kind string, bugs []core.CorpusBug, rows []diffRow) {
	t.Helper()
	for i := range bugs {
		checkBug(t, kind, &bugs[i], rows)
	}
}

func checkBug(t *testing.T, kind string, bug *core.CorpusBug, rows []diffRow) {
	t.Helper()
	if !shownOnNewSide(rows, bug.Path, bug.Line) {
		t.Errorf("%s:%d (%s) is not a new-side line inside a hunk", bug.Path, bug.Line, bug.Kind)
		return
	}
	if kind == "seeded" && !shownAdded(rows, bug.Path, bug.Line) {
		t.Errorf("%s:%d (%s) is not an added line: a seeded defect must be introduced by the change",
			bug.Path, bug.Line, bug.Kind)
	}
}

type diffRow struct {
	path    string
	kind    byte
	newLine int
}

type diffState struct {
	newLine  int
	oldWant  int
	newWant  int
	oldCount int
	newCount int
	pathIdx  int
	inHunk   bool
}

func parseDiff(t *testing.T, diff string) ([]diffRow, []string) {
	t.Helper()
	st := diffState{pathIdx: -1}
	var rows []diffRow
	var files []string
	for line := range strings.SplitSeq(strings.TrimSuffix(diff, "\n"), "\n") {
		consumeLine(t, line, &st, &rows, &files)
	}
	flushHunk(t, &st, files)
	return rows, files
}

func consumeLine(t *testing.T, line string, st *diffState, rows *[]diffRow, files *[]string) {
	t.Helper()
	if strings.HasPrefix(line, "diff --git ") {
		flushHunk(t, st, *files)
		st.pathIdx = -1
		return
	}
	if strings.HasPrefix(line, "@@ ") {
		flushHunk(t, st, *files)
		openHunk(t, st, line)
		return
	}
	if !st.inHunk {
		consumeFile(line, st, files)
		return
	}
	if row, ok := bodyRow(line, st, *files); ok {
		*rows = append(*rows, row)
	}
}

func consumeFile(line string, st *diffState, files *[]string) {
	target, ok := strings.CutPrefix(line, "+++ b/")
	if !ok {
		return
	}
	st.pathIdx = len(*files)
	*files = append(*files, target)
}

func openHunk(t *testing.T, st *diffState, header string) {
	t.Helper()
	_, st.oldWant = hunkRange(t, header, 1)
	st.newLine, st.newWant = hunkRange(t, header, 2)
	st.oldCount, st.newCount, st.inHunk = 0, 0, true
}

func bodyRow(line string, st *diffState, files []string) (diffRow, bool) {
	switch {
	case strings.HasPrefix(line, "\\"):
		return diffRow{}, false
	case strings.HasPrefix(line, "+"):
		row := diffRow{path: pathAt(st, files), kind: '+', newLine: st.newLine}
		st.newLine++
		st.newCount++
		return row, true
	case strings.HasPrefix(line, "-"):
		st.oldCount++
		return diffRow{}, false
	default:
		row := diffRow{path: pathAt(st, files), kind: ' ', newLine: st.newLine}
		st.newLine++
		st.oldCount++
		st.newCount++
		return row, true
	}
}

func flushHunk(t *testing.T, st *diffState, files []string) {
	t.Helper()
	if st.inHunk && (st.oldCount != st.oldWant || st.newCount != st.newWant) {
		t.Errorf("%s: hunk header declares %d old/%d new lines, body has %d/%d",
			pathAt(st, files), st.oldWant, st.newWant, st.oldCount, st.newCount)
	}
	st.inHunk = false
}

func pathAt(st *diffState, files []string) string {
	if st.pathIdx < 0 {
		return ""
	}
	return files[st.pathIdx]
}

func hunkRange(t *testing.T, header string, field int) (int, int) {
	t.Helper()
	fields := strings.Fields(header)
	if len(fields) < 3 {
		t.Fatalf("hunk header %q carries no ranges", header)
	}
	startText, countText, hasCount := strings.Cut(strings.TrimLeft(fields[field], "-+"), ",")
	start, err := strconv.Atoi(startText)
	if err != nil {
		t.Fatalf("hunk header %q: bad range start %q", header, startText)
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countText)
		if err != nil {
			t.Fatalf("hunk header %q: bad range count %q", header, countText)
		}
	}
	return start, count
}

func shownOnNewSide(rows []diffRow, path string, line int) bool {
	return slices.ContainsFunc(rows, func(row diffRow) bool {
		return row.path == path && row.newLine == line && (row.kind == '+' || row.kind == ' ')
	})
}

func shownAdded(rows []diffRow, path string, line int) bool {
	return slices.ContainsFunc(rows, func(row diffRow) bool {
		return row.path == path && row.newLine == line && row.kind == '+'
	})
}

func caseDirs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "bench", "corpus")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root: %v", err)
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "case.json")); err != nil {
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		t.Fatal("corpus holds no case.json")
	}
	return dirs
}

func snapshots(t *testing.T, dir string) (map[string]string, map[string]string) {
	t.Helper()
	before, after := snapshotTrees(t, filepath.Join(dir, "snapshots"))
	if len(before) == 0 {
		t.Fatalf("%s: no snapshots/<path>.before tree", dir)
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Fatalf("%s: %s has a before snapshot and no after snapshot", dir, path)
		}
	}
	if len(before) != len(after) {
		t.Fatalf("%s: %d before snapshots, %d after snapshots", dir, len(before), len(after))
	}
	return before, after
}

func snapshotTrees(t *testing.T, root string) (map[string]string, map[string]string) {
	t.Helper()
	before, after := map[string]string{}, map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		return recordSnapshot(t, root, path, entry, err, before, after)
	})
	if err != nil {
		t.Fatalf("%s: read snapshots: %v", root, err)
	}
	return before, after
}

func recordSnapshot(t *testing.T, root, path string, entry fs.DirEntry, err error, before, after map[string]string) error {
	t.Helper()
	if err != nil {
		return err
	}
	if entry.IsDir() {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(rel, ".before"):
		before[strings.TrimSuffix(rel, ".before")] = readSnapshot(t, path)
	case strings.HasSuffix(rel, ".after"):
		after[strings.TrimSuffix(rel, ".after")] = readSnapshot(t, path)
	}
	return nil
}

func runGit(t *testing.T, dir string, stdin io.Reader, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Minute
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func readSnapshot(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(raw)
}

func writeSnapshot(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
