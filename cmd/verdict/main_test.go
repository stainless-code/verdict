package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	select {
	case err := <-readDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Read after Close = %v, want io.ErrClosedPipe", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock a read parked on the source")
	}
}

func TestClosableReaderForwardsDataThenEOF(t *testing.T) {
	t.Parallel()
	got, err := io.ReadAll(closableReader(strings.NewReader("frame\n")))
	if err != nil {
		t.Fatalf("ReadAll = %v", err)
	}
	if string(got) != "frame\n" {
		t.Fatalf("ReadAll = %q, want %q", got, "frame\n")
	}
}
