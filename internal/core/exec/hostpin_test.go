//go:build !windows

// Platform note: the stub glab is a POSIX shell script; the pin and the
// environment check are OS-independent and covered on every OS by the core
// package's tests.

package exec_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// stubGlab puts a network-dead glab first on this process's PATH (the runner
// resolves the binary through it, not through the child's env) and returns
// the file it appends each call's argv to.
func stubGlab(t *testing.T) string {
	t.Helper()
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "glab.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "glab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestPinnedGlabWriteRefusesAnotherAPIHost proves the runner checks the
// environment glab will inherit: a pinned review POST whose GITLAB_API_HOST
// (or CI auto-login server) names another server refuses host-mismatch before
// any spawn, because glab would send it, with the pinned host's token, there.
// With the variable unset or naming the target, the call runs, pinned.
func TestPinnedGlabWriteRefusesAnotherAPIHost(t *testing.T) {
	log := stubGlab(t)
	post := []string{
		"glab", "api", "projects/acme%2Fapp/merge_requests/7/notes",
		"--method", "POST", "--header", "Content-Type: application/json", "--input", "-",
	}
	for _, tc := range []struct {
		name    string
		env     []string
		refuses bool
	}{
		{"another API host", []string{"GITLAB_API_HOST=gitlab.corp.example"}, true},
		{"CI auto-login server elsewhere", []string{
			"GLAB_ENABLE_CI_AUTOLOGIN=true", "GITLAB_CI=true", "CI_SERVER_FQDN=gitlab.corp.example",
		}, true},
		{"API host is the target", []string{"GITLAB_API_HOST=https://gitlab.com"}, false},
		{"no API host", nil, false},
	} {
		checkPinnedGlab(t, post, log, tc.env, tc.refuses, tc.name)
	}
}

// checkPinnedGlab runs one pinned glab POST under extra env against the stub
// and checks it either refused before any spawn or reached the stub pinned.
func checkPinnedGlab(t *testing.T, post []string, log string, extra []string, refuses bool, name string) {
	t.Helper()
	resetLog(t, log)
	// An explicit env, so the developer's own GITLAB_API_HOST cannot decide a case.
	env := append([]string{"HOME=" + t.TempDir()}, extra...)
	ctx := core.WithHost(t.Context(), "gitlab.com")
	res, err := coreexec.Runner{}.Run(ctx, &core.Command{Argv: post, Env: env, Stdin: strings.NewReader("{}")})
	calls := readLog(t, log)
	if refuses {
		var ref *core.RefusalError
		if !errors.As(err, &ref) || ref.Code != core.CodeHostMismatch || calls != "" {
			t.Errorf("%s: err = %v, glab calls %q; want a host-mismatch refusal and no spawn", name, err, calls)
		}
		return
	}
	want := "api --hostname gitlab.com projects/acme%2Fapp/merge_requests/7/notes"
	if err != nil || res.ExitCode != 0 || !strings.HasPrefix(calls, want) {
		t.Errorf("%s: err = %v exit %d, glab calls %q; want one pinned call starting %q", name, err, res.ExitCode, calls, want)
	}
}

func resetLog(t *testing.T, log string) {
	t.Helper()
	if err := os.Remove(log); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

// readLog is the stub's call log, "" when it was never called.
func readLog(t *testing.T, log string) string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(raw)
}

// TestPortedGlabHostRunsUnpinnedOnlyAsGlabsDefault proves the runner reads the
// environment glab will inherit for a target host with a port: with GITLAB_HOST
// naming exactly that authority the stub receives the call with no --hostname
// and the bare group/project; with it unset, another port, or another host,
// or with GITLAB_API_HOST elsewhere, the call refuses before any spawn.
func TestPortedGlabHostRunsUnpinnedOnlyAsGlabsDefault(t *testing.T) {
	log := stubGlab(t)
	const host = "gitlab.corp.example:8443"
	lookup := []string{"glab", "mr", "list", "-R", "https://" + host + "/g/app", "--source-branch=fix-x"}
	for _, tc := range []struct {
		name    string
		env     []string
		refuses bool
	}{
		{"GITLAB_HOST is the target", []string{"GITLAB_HOST=https://gitlab.corp.example:8443"}, false},
		{"GITLAB_HOST and API host are the target", []string{"GITLAB_HOST=" + host, "GITLAB_API_HOST=" + host}, false},
		{"GITLAB_HOST unset", nil, true},
		{"GITLAB_HOST another port", []string{"GITLAB_HOST=gitlab.corp.example:9443"}, true},
		{"GITLAB_HOST another host", []string{"GITLAB_HOST=gitlab.other.example:8443"}, true},
		{"API host elsewhere", []string{"GITLAB_HOST=" + host, "GITLAB_API_HOST=gitlab.other.example"}, true},
		{"CI auto-login server elsewhere", []string{
			"GITLAB_HOST=" + host, "GLAB_ENABLE_CI_AUTOLOGIN=true", "GITLAB_CI=true", "CI_SERVER_FQDN=gitlab.other.example",
		}, true},
	} {
		checkPortedGlab(t, lookup, log, tc.env, tc.refuses, tc.name)
	}
}

// checkPortedGlab runs one lookup against the ported host under extra env and
// checks it refused before any spawn, or reached the stub with no host pin.
func checkPortedGlab(t *testing.T, lookup []string, log string, extra []string, refuses bool, name string) {
	t.Helper()
	resetLog(t, log)
	env := append([]string{"HOME=" + t.TempDir()}, extra...)
	res, err := coreexec.Runner{}.Run(core.WithHost(t.Context(), "gitlab.corp.example:8443"), &core.Command{Argv: lookup, Env: env})
	calls := readLog(t, log)
	if refuses {
		var ref *core.RefusalError
		if !errors.As(err, &ref) || ref.Code != core.CodeHostMismatch || calls != "" {
			t.Errorf("%s: err = %v, glab calls %q; want a host-mismatch refusal and no spawn", name, err, calls)
		}
		return
	}
	if want := "mr list -R g/app --source-branch=fix-x\n"; err != nil || res.ExitCode != 0 || calls != want {
		t.Errorf("%s: err = %v exit %d, glab calls %q; want exactly %q", name, err, res.ExitCode, calls, want)
	}
}
