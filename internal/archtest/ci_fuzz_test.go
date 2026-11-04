package archtest

// Guard for the nightly fuzz lane: it must stay schedule-only, stay advisory in
// the gate (a schedule-only lane skips on every pull request), and run every
// fuzz target the tree declares, so a new parser cannot sit unfuzzed because
// someone forgot the Makefile list.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fuzzCondition is the only event gate the nightly lane may carry.
const fuzzCondition = "    if: needs.changes.outputs.full == 'true' && github.event_name == 'schedule'"

// fuzzTargetDecl matches a fuzz target's declaration line.
var fuzzTargetDecl = regexp.MustCompile(`^func (Fuzz\w+)\(f \*testing\.F\) \{`)

// fuzzEntry matches one FUZZ_TARGETS line, with or without the backslash that
// continues the variable.
var fuzzEntry = regexp.MustCompile(`^\t(\./\S+):(Fuzz\w+)(?: \\)?$`)

// TestCIFuzzLane pins the lane's two costs: it never runs on a pull request,
// and it can never fail a pull request's gate from a distance.
func TestCIFuzzLane(t *testing.T) {
	t.Parallel()
	workflow := readCIWorkflow(t)
	jobs := workflowJobs(t, "ci.yml", workflow)
	block, ok := jobs["fuzz"]
	if !ok {
		t.Fatal("ci.yml has no fuzz job: nothing would run the targets in the tree")
	}
	joined := strings.Join(block, "\n")
	if !strings.Contains(joined, fuzzCondition) {
		t.Errorf("fuzz lost %q: a pull request would pay for the nightly run", strings.TrimSpace(fuzzCondition))
	}
	if !strings.Contains(joined, "run: make fuzz") {
		t.Error("fuzz no longer runs `make fuzz`: its target list would drift from the suite")
	}
	gate := parseCIGate(t, workflow)
	if slices.Contains(gate.required, "fuzz") {
		t.Error("fuzz is a required lane: it skips on every pull request, so the gate would hold every PR shut")
	}
	if !slices.Contains(gate.advisory, "fuzz") {
		t.Errorf("fuzz is undeclared in ADVISORY_JOBS (advisory = %v): a failing nightly run would fail ci-complete", gate.advisory)
	}
}

// TestFuzzTargetsAreAllInTheLane compares the tree against FUZZ_TARGETS in both
// directions, and catches an entry pointed at the wrong package.
func TestFuzzTargetsAreAllInTheLane(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	compareFuzzTargets(t, fuzzTargetsInTree(t, root), fuzzTargetsInMakefile(t, root))
}

// compareFuzzTargets reports a target the lane never runs, an entry with no
// target behind it, and an entry naming a package that declares something else.
func compareFuzzTargets(t *testing.T, tree, lane map[string]string) {
	t.Helper()
	reportUntestedTargets(t, tree, lane)
	reportUnbackedEntries(t, tree, lane)
}

// reportUntestedTargets flags a fuzz target the lane never runs, and an entry
// that runs it in a different package.
func reportUntestedTargets(t *testing.T, tree, lane map[string]string) {
	t.Helper()
	for name, dir := range tree {
		got, ok := lane[name]
		switch {
		case !ok:
			t.Errorf("%s in %s is missing from Makefile FUZZ_TARGETS: it never fuzzes", name, dir)
		case got != dir:
			t.Errorf("Makefile runs %s in %s, but the tree declares it in %s", name, got, dir)
		}
	}
}

// reportUnbackedEntries flags a lane entry naming a target no package declares.
func reportUnbackedEntries(t *testing.T, tree, lane map[string]string) {
	t.Helper()
	for name, dir := range lane {
		if _, ok := tree[name]; !ok {
			t.Errorf("Makefile FUZZ_TARGETS names %s in %s, which the tree does not declare", name, dir)
		}
	}
}

// fuzzTargetsInTree maps every fuzz target to the module-relative package
// directory that declares it.
func fuzzTargetsInTree(t *testing.T, root string) map[string]string {
	t.Helper()
	targets := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		return recordFuzzDecls(root, path, d, walkErr, targets)
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(targets) == 0 {
		t.Fatal("no fuzz target found in the tree: the scan, not the list, is what broke")
	}
	return targets
}

// recordFuzzDecls adds the targets one walked path declares to targets.
func recordFuzzDecls(root, path string, d fs.DirEntry, walkErr error, targets map[string]string) error {
	if walkErr != nil {
		return walkErr
	}
	if d.IsDir() {
		if skipFuzzDir(root, path, d.Name()) {
			return fs.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(path, "_test.go") {
		return nil
	}
	return recordFuzzFile(root, path, targets)
}

// recordFuzzFile adds the targets one file declares to targets.
func recordFuzzFile(root, path string, targets map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pkg, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, name := range fuzzDecls(raw) {
		targets[name] = "./" + filepath.ToSlash(pkg)
	}
	return nil
}

// skipFuzzDir keeps the walk out of trees that hold no Go test files.
func skipFuzzDir(root, path, name string) bool {
	return path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata")
}

// fuzzDecls lists the fuzz targets one file declares.
func fuzzDecls(raw []byte) []string {
	var names []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if m := fuzzTargetDecl.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

// fuzzTargetsInMakefile maps every FUZZ_TARGETS entry to the package it names.
func fuzzTargetsInMakefile(t *testing.T, root string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	lane := make(map[string]string)
	for line := range strings.SplitSeq(string(raw), "\n") {
		m := fuzzEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if prev, dup := lane[m[2]]; dup {
			t.Fatalf("Makefile FUZZ_TARGETS lists %s twice (%s and %s)", m[2], prev, m[1])
		}
		lane[m[2]] = m[1]
	}
	if len(lane) == 0 {
		t.Fatal("Makefile FUZZ_TARGETS is empty or unparseable: make fuzz runs nothing")
	}
	return lane
}
