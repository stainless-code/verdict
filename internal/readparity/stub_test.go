package readparity_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteStubResolvesOnHost pins the per-case gh stub to a launchable
// executable on the host. Windows splits PATH on ';' and resolves only
// PATHEXT names, so the old ':' join plus the extensionless script fell
// through to the runner's real gh: every live ready/calibrate leg then
// retried the GH_TOKEN automation refusal for about 20 seconds.
func TestWriteStubResolvesOnHost(t *testing.T) {
	env := writeStub(t, "#!/usr/bin/env bash\nprintf '%s' stub-ok\n", nil)
	t.Setenv("PATH", env["PATH"])

	dir, _, _ := strings.Cut(env["PATH"], string(os.PathListSeparator))
	found, err := exec.LookPath("gh")
	if err != nil {
		t.Fatalf("lookpath gh: %v", err)
	}
	if filepath.Dir(found) != dir {
		t.Fatalf("resolved %q, want a gh under %q", found, dir)
	}
	out, err := exec.CommandContext(t.Context(), "gh").Output()
	if err != nil {
		t.Fatalf("run gh: %v", err)
	}
	if string(out) != "stub-ok" {
		t.Fatalf("gh output = %q, want stub-ok", out)
	}
}
