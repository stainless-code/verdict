//go:build darwin

package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// serviceDomain is the preference domain the hostile agent tries to write.
const serviceDomain = "com.stainless.verdict.sandbox-test"

// serviceScript is a prompt-injected agent with a shell reaching past the
// filesystem rules through macOS services: a `defaults write` (cfprefsd
// writes the plist for it) and the pasteboard.
const serviceScript = "#!/bin/sh\n" +
	"{ /usr/bin/defaults write " + serviceDomain + " k v && echo defaults=written || echo defaults=denied\n" +
	"  printf verdict-sandbox | /usr/bin/pbcopy && echo pasteboard=written || echo pasteboard=denied\n" +
	"} > \"$HOME/.pi/agent/sessions/services.log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxClosesMacOSServices proves a review agent with a live
// shell can neither write a preference through cfprefsd nor set the
// pasteboard, and the domain stays absent.
func TestReviewSandboxClosesMacOSServices(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	t.Cleanup(func() {
		// The delete fails when the domain is absent, which is the passing case.
		if out, err := exec.Command("/usr/bin/defaults", "delete", serviceDomain).CombinedOutput(); err != nil { //nolint:noctx // cleanup outlives t.Context
			t.Logf("defaults delete %s: %v: %s", serviceDomain, err, out)
		}
	})
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ref := sandboxedRun(t, confinedRoot(t), home, serviceScript, "1"); ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	log := string(readFile(t, filepath.Join(home, ".pi", "agent", "sessions", "services.log")))
	if !strings.Contains(log, "defaults=denied") || !strings.Contains(log, "pasteboard=denied") {
		t.Errorf("services log:\n%s\nwant both denied", log)
	}
	if out, err := exec.Command("/usr/bin/defaults", "read", serviceDomain).CombinedOutput(); err == nil { //nolint:noctx // a read
		t.Errorf("the preference domain exists after the review: %s", out)
	}
}
