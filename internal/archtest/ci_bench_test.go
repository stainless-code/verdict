package archtest

// Guard for the nightly bench lane: it must stay schedule-only, stay advisory
// in the gate (a schedule-only lane skips on every pull request), run every
// benchmark the tree declares, and store the output, so a regression in a hot
// path is visible as a number instead of waiting for someone to run one by
// hand.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// benchCondition is the only event gate the nightly lane may carry.
const benchCondition = "    if: needs.changes.outputs.full == 'true' && github.event_name == 'schedule'"

// benchUpload is the artifact action the lane uploads through: the pinned
// release the eval lanes already use, so a bump cannot leave one lane behind
// on a SHA the rest of the workflow has left.
const benchUpload = "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02"

// testdataPath is the directory `go test ./...` refuses to load, and the
// separator that marks a file inside one.
const testdataDir, testdataSep = "testdata", "/testdata/"

// benchDecl matches a benchmark's declaration line.
var benchDecl = regexp.MustCompile(`^func (Benchmark\w+)\(b \*testing\.B\) \{`)

// TestCIBenchLane pins the lane's two costs, exactly as the fuzz guard does: it
// never runs on a pull request, and it can never fail a pull request's gate
// from a distance. The artifact check is what separates this lane from fuzz: a
// nightly benchmark nobody can download is a number nobody can compare.
func TestCIBenchLane(t *testing.T) {
	t.Parallel()
	workflow := readCIWorkflow(t)
	jobs := workflowJobs(t, "ci.yml", workflow)
	block, ok := jobs["bench"]
	if !ok {
		t.Fatal("ci.yml has no bench job: nothing would run the benchmarks in the tree")
	}
	joined := strings.Join(block, "\n")
	if !strings.Contains(joined, benchCondition) {
		t.Errorf("bench lost %q: a pull request would pay for the nightly run", strings.TrimSpace(benchCondition))
	}
	if !strings.Contains(joined, "run: make bench") {
		t.Error("bench no longer runs `make bench`: the lane would drift from the target it measures")
	}
	if !strings.Contains(joined, benchUpload) {
		t.Errorf("bench does not upload through %s: the numbers would vanish with the runner", benchUpload)
	}
	//nolint:misspell // GitHub spells its own expression function `cancelled`.
	if !strings.Contains(joined, "if: ${{ !cancelled() }}") {
		t.Error("bench uploads only on a green run: the night that regressed is the one worth keeping")
	}
	gate := parseCIGate(t, workflow)
	if slices.Contains(gate.required, "bench") {
		t.Error("bench is a required lane: it skips on every pull request, so the gate would hold every PR shut")
	}
	if !slices.Contains(gate.advisory, "bench") {
		t.Errorf("bench is undeclared in ADVISORY_JOBS (advisory = %v): a failing nightly run would fail ci-complete", gate.advisory)
	}
}

// TestBenchTargetScansTheWholeTree pins `make bench` to a tree-wide scan: a
// package list would leave a new benchmark unrun, and the silence would read as
// a fast suite rather than as a missing measurement.
func TestBenchTargetScansTheWholeTree(t *testing.T) {
	t.Parallel()
	makefile := readRepoFile(t, moduleRoot(t), "Makefile")
	if !strings.Contains(makefile, "\n.PHONY: bench\n") {
		t.Error("the Makefile does not declare `bench` phony: a file named bench would shadow the target")
	}
	recipe := strings.Join(makeTargetRecipe(t, makefile, "bench"), "\n")
	for _, want := range []string{"-bench=.", "benchmem", "./...", "-run='^$$'"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the bench recipe lost %q: the nightly lane would stop measuring what the tree declares", want)
		}
	}
}

// makeTargetRecipe returns one Makefile target's tab-indented recipe lines.
func makeTargetRecipe(t *testing.T, makefile, target string) []string {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	start := slices.Index(lines, target+":")
	if start < 0 {
		t.Fatalf("the Makefile declares no %s target", target)
	}
	var recipe []string
	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, line)
	}
	if len(recipe) == 0 {
		t.Fatalf("the Makefile %s target has no recipe: it runs nothing", target)
	}
	return recipe
}

// TestNoBenchmarkUnderTestdata fails on a benchmark declared under a
// `testdata/` directory. `go test` does not load those trees, so the benchmark
// compiles nowhere and runs never — the numbers go missing rather than wrong,
// which is the harder failure to notice.
func TestNoBenchmarkUnderTestdata(t *testing.T) {
	t.Parallel()
	declared, stranded := scanBenchDecls(t, moduleRoot(t))
	if declared == 0 {
		t.Fatal("no benchmark found in the tree: the scan, not the benchmarks, is what broke")
	}
	for _, file := range stranded {
		t.Errorf("%s declares a benchmark under %s/: `go test ./...` never loads it, so `make bench` skips it",
			file, testdataDir)
	}
}

// scanBenchDecls counts the benchmarks the tree declares and lists the files
// that declare one inside testdata/.
func scanBenchDecls(t *testing.T, root string) (int, []string) {
	t.Helper()
	var tally benchTally
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		return tally.walk(root, path, d, walkErr)
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tally.declared, tally.stranded
}

// benchTally counts the benchmarks the walk found and names the files holding
// one where `make bench` cannot reach it.
type benchTally struct {
	stranded []string
	declared int
}

// walk feeds one visited path into the tally, or ends the walk early.
func (t *benchTally) walk(root, path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d.IsDir() {
		if skippingBenchWalk(root, path, d.Name()) {
			return fs.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(path, "_test.go") {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	t.record(root, path, raw)
	return nil
}

// record adds one file's benchmarks.
func (t *benchTally) record(root, path string, raw []byte) {
	found := benchDeclsIn(raw)
	t.declared += len(found)
	if len(found) == 0 || !strings.Contains(filepath.ToSlash(path), testdataSep) {
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	t.stranded = append(t.stranded, filepath.ToSlash(rel))
}

// skippingBenchWalk keeps the walk out of trees that hold no Go source.
func skippingBenchWalk(root, path, name string) bool {
	return path != root && (strings.HasPrefix(name, ".") || name == "node_modules")
}

// benchDeclsIn lists the benchmarks one file declares.
func benchDeclsIn(raw []byte) []string {
	var names []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if m := benchDecl.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}
