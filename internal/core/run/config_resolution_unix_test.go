//go:build darwin || linux

package run

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewConfigHomesAgreeThroughALinkedWorkingDirectory(t *testing.T) {
	requireReviewSandbox(t)
	base := mustResolve(t, visibleTempDir(t))
	actual := filepath.Join(base, "elsewhere", "real")
	mustMkdir(t, actual)
	link := filepath.Join(base, "link")
	mustSymlink(t, actual, link)
	t.Chdir(link)
	t.Setenv("PWD", link)
	kernel := filepath.Join(base, "elsewhere", "cfg")
	lexical := filepath.Join(base, "cfg")
	for _, dir := range []string{kernel, lexical} {
		writeUnder(t, dir, ".credentials.json", "credential")
		writeUnder(t, dir, "gh/hosts.yml", "credential")
	}
	script := "#!/bin/sh\n" +
		"printf 'claude=%s\\nxdg=%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$XDG_CONFIG_HOME\"\n" +
		"if /bin/cat \"$CLAUDE_CONFIG_DIR/.credentials.json\" >/dev/null 2>&1; then echo claude=read; else echo claude=hidden; fi\n" +
		"if /bin/cat \"$XDG_CONFIG_HOME/gh/hosts.yml\" >/dev/null 2>&1; then echo gh=read; else echo gh=hidden; fi\n" +
		"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"
	_, ref, log := sandboxedRunAs(t, "pi", confinedRoot(t), t.TempDir(), script, "1",
		"CLAUDE_CONFIG_DIR=../cfg", "XDG_CONFIG_HOME=../cfg")
	if ref != nil {
		t.Fatal(ref)
	}
	for _, want := range []string{"claude=" + kernel, "xdg=" + kernel, "claude=hidden", "gh=hidden"} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("launch lacks %q:\n%s", want, log)
		}
	}
}
