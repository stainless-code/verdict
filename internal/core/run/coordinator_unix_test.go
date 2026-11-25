//go:build !windows

package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestCoordinatorOfflineLauncherSkipsEmptyCrossBatches(t *testing.T) {
	t.Parallel()
	req, commander := coordinatorFixture(t)
	count := filepath.Join(t.TempDir(), "launches")
	script := "#!/bin/sh\nset -eu\n" +

		"case \"$*\" in\n" +
		" *x-finder-1*) output=" + shellLiteral(filepath.Join(commander.dir, "x-finder-1", "review.json")) + "; marker=first ;;\n" +
		" *x-finder-2*) output=" + shellLiteral(filepath.Join(commander.dir, "x-finder-2", "review.json")) + "; marker=second ;;\n" +
		" *) exit 91 ;;\nesac\n" +
		"printf '%s\\n' \"$marker\" >> " + shellLiteral(count) + "\n" +
		"printf '%s' " + shellLiteral(`{"schema_version":"1","findings":[],"summary":"All changed hunks reviewed.",`+fixtureReceipts+`}`) + " > \"$output\"\n"
	bin := plantStub(t, "claude", script)
	req.Commander = stubCommander{inner: coreexec.Watchdog{}, name: "claude", bin: bin}
	req.LookPath = func(string) (string, error) { return bin, nil }
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	raw, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	// The two finders run in parallel, so the shared counter records who started
	// first, not a fixed order: both must appear exactly once.
	launches := strings.Fields(string(raw))
	slices.Sort(launches)
	if !slices.Equal(launches, []string{"first", "second"}) {
		t.Fatalf("launches = %q", raw)
	}
	assertNoCrossOutput(t, commander.dir)
	manifest := readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "complete" {
		t.Fatal("empty execution incomplete")
	}
}

func assertNoCrossOutput(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"x-cross-1", "x-cross-2"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", name, err)
		}
	}
}
