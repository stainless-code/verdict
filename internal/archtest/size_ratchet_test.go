package archtest

// Repository guard for the size and dependency ratchet: `make size`
// (scripts/size.sh) compares every shipped binary and the module graph against
// scripts/size-baseline.txt. The budget must name exactly the platforms the
// release contract ships — a missing line leaves a shipped binary unmeasured,
// a stale one keeps a ceiling for nothing — and the ratchet must stay wired
// into the Makefile and the Linux CI lane, or nothing measures the payload
// every installer hands out.

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/release"
)

// depsCeiling is the budget line holding the module-graph dependency ceiling.
const depsCeiling = "deps"

// sizeBudget is the parsed scripts/size-baseline.txt: "goos/goarch" ceilings
// plus the "deps" ceiling.
type sizeBudget map[string]int64

// TestSizeBudgetNamesEveryShippedPlatform fails when the budget and the
// release contract disagree about which binaries ship.
func TestSizeBudgetNamesEveryShippedPlatform(t *testing.T) {
	budget := readSizeBudget(t, moduleRoot(t))
	shipped := shippedPlatforms(t)
	for _, name := range missingCeilings(budget, shipped) {
		t.Errorf("scripts/size-baseline.txt has no ceiling for %s: a shipped binary would go unmeasured", name)
	}
	for _, name := range unshippedCeilings(budget, shipped) {
		t.Errorf("scripts/size-baseline.txt budgets %s, which the release contract does not ship", name)
	}
	if _, ok := budget[depsCeiling]; !ok {
		t.Error("scripts/size-baseline.txt has no deps line: the first third-party import would pass unremarked")
	}
}

// shippedPlatforms lists "goos/goarch" for every binary the release contract
// hands out, refusing an empty contract outright.
func shippedPlatforms(t *testing.T) []string {
	t.Helper()
	platforms, err := release.Platforms()
	if err != nil {
		t.Fatalf("read the release contract platforms: %v", err)
	}
	if len(platforms) == 0 {
		t.Fatal("the release contract ships no platform: the guard would pass on nothing")
	}
	shipped := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		shipped = append(shipped, platform.GOOS+"/"+platform.GOARCH)
	}
	return shipped
}

// missingCeilings lists the shipped platforms the budget leaves unmeasured.
func missingCeilings(budget sizeBudget, shipped []string) []string {
	var missing []string
	for _, name := range shipped {
		if _, ok := budget[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// unshippedCeilings lists the budget rows naming a platform nothing ships.
func unshippedCeilings(budget sizeBudget, shipped []string) []string {
	var stale []string
	for _, name := range slices.Sorted(maps.Keys(budget)) {
		if name != depsCeiling && !slices.Contains(shipped, name) {
			stale = append(stale, name)
		}
	}
	return stale
}

// TestSizeRatchetWiring fails when the ratchet drops out of `make size`, out of
// the CI lane that runs it, or off the release build's own flags.
func TestSizeRatchetWiring(t *testing.T) {
	root := moduleRoot(t)
	makefile := readRepoFile(t, root, "Makefile")
	for _, want := range []string{".PHONY: size", "\nsize:\n", "sh scripts/size.sh"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("the Makefile lost %q: `make size` would no longer ratchet", want)
		}
	}
	script := readRepoFile(t, root, "scripts/size.sh")
	for _, want := range []string{
		"scripts/size-baseline.txt",
		"go run ./cmd/crossplatforms",
		"-trimpath",
		"-s -w",
		"go mod graph",
		"go tool nm -size",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/size.sh lost %q", want)
		}
	}
	jobs := workflowJobs(t, "ci.yml", readCIWorkflow(t))
	if !strings.Contains(strings.Join(jobs["cross"], "\n"), "run: make size") {
		t.Error("the ci.yml cross lane no longer runs `make size`: no lane measures the shipped binaries")
	}
}

// readSizeBudget parses the budget file: comment lines, `<goos>/<goarch>
// <bytes>` rows, and one `deps <count>` row.
func readSizeBudget(t *testing.T, root string) sizeBudget {
	t.Helper()
	budget := sizeBudget{}
	for line := range strings.SplitSeq(readRepoFile(t, root, "scripts/size-baseline.txt"), "\n") {
		if name, value, ok := parseBudgetLine(t, line); ok {
			budget[name] = value
		}
	}
	return budget
}

// parseBudgetLine reads one `<name> <bytes>` row, skipping blank lines and
// comments, and reports ok=false for a row it cannot take.
func parseBudgetLine(t *testing.T, line string) (name string, value int64, ok bool) {
	t.Helper()
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", 0, false
	}
	name, raw, cut := strings.Cut(line, " ")
	if !cut {
		t.Errorf("scripts/size-baseline.txt: %q is not `<name> <bytes>`", line)
		return "", 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		t.Errorf("scripts/size-baseline.txt: %q does not carry a non-negative byte count", line)
		return "", 0, false
	}
	return name, value, true
}

// readRepoFile returns one tracked file as text.
func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
