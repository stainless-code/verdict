package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestCheckoutRootFindsCheckout checks the walk-up from a bin dir.
func TestCheckoutRootFindsCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"version":"0.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checkoutRoot(filepath.Join(root, "bin")); got != root {
		t.Fatalf("checkoutRoot(bin) = %q, want %q", got, root)
	}
}

// TestCheckoutRootFallsBackToBinaryDir checks the no-checkout fallback.
func TestCheckoutRootFallsBackToBinaryDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := checkoutRoot(dir); got != dir {
		t.Fatalf("checkoutRoot(%q) = %q, want fallback %q", dir, got, dir)
	}
}

func TestEnvMapUsesInjectedGetenvOverHostEnv(t *testing.T) {
	t.Setenv("HOME", "host-home")
	t.Setenv("CODEX_HOME", "host-codex")
	t.Setenv("FORGEJO_TOKEN", "host-secret")
	got := envMap(func(k string) string {
		switch k {
		case "HOME":
			return "injected-home"
		case "CODEX_HOME":
			return "injected-codex"
		default:
			return ""
		}
	})
	if got["HOME"] != "injected-home" {
		t.Errorf("HOME = %q, want injected-home", got["HOME"])
	}
	if got["CODEX_HOME"] != "injected-codex" {
		t.Errorf("CODEX_HOME = %q, want injected-codex", got["CODEX_HOME"])
	}
	if v, ok := got["FORGEJO_TOKEN"]; ok {
		t.Errorf("FORGEJO_TOKEN leaked from host env: %q", v)
	}
}

// TestEnvMapCarriesTheGlabHostVariables pins that the map every verb receives
// holds the variables post's host gate reads (GITLAB_HOST and the ones that
// redirect glab). They were missing from it, so a write saw GITLAB_HOST unset
// and refused a ported host the read side had accepted.
func TestEnvMapCarriesTheGlabHostVariables(t *testing.T) {
	want := map[string]string{
		"GITLAB_HOST": "https://git.corp.example:8443", "GITLAB_API_HOST": "git.corp.example:8443", "GITLAB_REPO": "g/app",
		"CI_SERVER_FQDN": "git.corp.example:8443", "GLAB_ENABLE_CI_AUTOLOGIN": "true", "GITLAB_CI": "true",
	}
	got := envMap(func(k string) string { return want[k] })
	for k, v := range want {
		if got[k] != v {
			t.Errorf("envMap()[%q] = %q, want %q", k, got[k], v)
		}
	}
	only := envMap(func(k string) string {
		if k == "GITLAB_HOST" {
			return "https://git.corp.example:8443"
		}
		return ""
	})
	if ref := core.GlabDefaultConflict("git.corp.example:8443", func(k string) string { return only[k] }); ref != nil {
		t.Errorf("a post on a ported host with GITLAB_HOST set must pass the host gate: %v", ref)
	}
}

type stuckReader struct{ release chan struct{} }

func (r stuckReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

func TestClosableReaderCloseUnblocksAStuckRead(t *testing.T) {
	t.Parallel()
	src := stuckReader{release: make(chan struct{})}
	t.Cleanup(func() { close(src.release) })
	reader := closableReader(src)
	readDone := make(chan error, 1)
	go func() {
		_, err := reader.Read(make([]byte, 8))
		readDone <- err
	}()
	if err := reader.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	var readErr error
	testenv.Until(t, func() bool {
		select {
		case readErr = <-readDone:
			return true
		default:
			return false
		}
	})
	if !errors.Is(readErr, io.ErrClosedPipe) {
		t.Fatalf("Read after Close = %v, want io.ErrClosedPipe", readErr)
	}
}

func TestClosableReaderForwardsDataThenEOF(t *testing.T) {
	t.Parallel()
	reader := closableReader(strings.NewReader("frame\n"))
	t.Cleanup(func() { _ = reader.Close() })
	var got []byte
	var err error
	done := make(chan struct{})
	go func() { got, err = io.ReadAll(reader); close(done) }()
	testenv.Until(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatalf("ReadAll = %v", err)
	}
	if string(got) != "frame\n" {
		t.Fatalf("ReadAll = %q, want %q", got, "frame\n")
	}
}
