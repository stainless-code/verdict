package skillfs_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// shimEnv is the hermetic child environment: no ambient VERDICT_* knobs.
func shimEnv(t *testing.T, path string, extra map[string]string) []string {
	t.Helper()
	env := []string{
		"HOME=" + t.TempDir(),
		"PATH=" + path,
		"TMPDIR=" + t.TempDir(),
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

// skillCopy materializes a skill dir holding only the shim, like a registry
// copy before bootstrap.
func skillCopy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body, err := os.ReadFile(filepath.Join("skills", "verdict", "scripts", "verdict"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "verdict"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeScript writes one executable POSIX shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runShim runs one skill copy's shim, returning exit code and streams.
func runShim(t *testing.T, dir string, env []string, args ...string) (int, string, string) {
	t.Helper()
	argv := append([]string{filepath.Join(dir, "scripts", "verdict")}, args...)
	cmd := exec.CommandContext(t.Context(), "sh", argv...)
	cmd.Dir = t.TempDir()
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatalf("run shim: %v", err)
	}
	return code, stdout.String(), stderr.String()
}

// TestShimResolutionOrder proves VERDICT_BIN -> bundled bin -> PATH, and the
// typed miss that never fetches.
//
//nolint:cyclop,gocognit,revive // the subtests are the resolution matrix
func TestShimResolutionOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX shim is not the Windows route")
	}
	pathBin := t.TempDir()
	writeScript(t, filepath.Join(pathBin, "verdict"), `printf 'path-bin %s\n' "$*"`)
	customBin := filepath.Join(t.TempDir(), "custom-verdict")
	writeScript(t, customBin, `printf 'custom-bin %s\n' "$*"`)
	basePath := pathBin + ":/usr/bin:/bin"

	t.Run("path", func(t *testing.T) {
		dir := skillCopy(t)
		code, out, derr := runShim(t, dir, shimEnv(t, basePath, nil), "--version")
		if code != 0 || out != "path-bin --version\n" || derr != "" {
			t.Fatalf("path resolution = %d %q %q", code, out, derr)
		}
	})
	t.Run("verdict-bin", func(t *testing.T) {
		dir := skillCopy(t)
		code, out, _ := runShim(t, dir, shimEnv(t, basePath, map[string]string{"VERDICT_BIN": customBin}), "--version")
		if code != 0 || out != "custom-bin --version\n" {
			t.Fatalf("VERDICT_BIN resolution = %d %q", code, out)
		}
	})
	t.Run("bundled-wins-over-path", func(t *testing.T) {
		dir := skillCopy(t)
		writeScript(t, filepath.Join(dir, "bin", "verdict"), `printf 'bundled-bin %s\n' "$*"`)
		code, out, _ := runShim(t, dir, shimEnv(t, basePath, nil), "files")
		if code != 0 || out != "bundled-bin files\n" {
			t.Fatalf("bundled resolution = %d %q", code, out)
		}
	})
	t.Run("typed-miss-never-fetches", func(t *testing.T) {
		dir := skillCopy(t)
		miss := t.TempDir()
		curlLog := filepath.Join(miss, "curl.log")
		writeScript(t, filepath.Join(miss, "curl"), fmt.Sprintf(`printf '%%s\n' "$*" >> %q; exit 1`, curlLog))
		code, out, derr := runShim(t, dir, shimEnv(t, miss+":/usr/bin:/bin", nil), "fetch", "acme/app", "1")
		if code != 1 || out != "" {
			t.Fatalf("miss = exit %d stdout %q", code, out)
		}
		if !strings.Contains(derr, "missing-go-binary") || !strings.Contains(derr, "scripts/verdict bootstrap") {
			t.Fatalf("miss stderr = %q", derr)
		}
		if log, err := os.ReadFile(curlLog); err == nil && len(log) > 0 {
			t.Fatalf("review-time miss fetched: %s", log)
		}
	})
	t.Run("bad-verdict-bin", func(t *testing.T) {
		dir := skillCopy(t)
		notExec := filepath.Join(t.TempDir(), "not-exec")
		if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, derr := runShim(t, dir, shimEnv(t, basePath, map[string]string{"VERDICT_BIN": notExec}), "--version")
		if code != 1 || !strings.Contains(derr, "bad-verdict-bin") {
			t.Fatalf("bad VERDICT_BIN = exit %d stderr %q", code, derr)
		}
	})
}

// TestShimBootstrapReusesInstaller proves the one-time bootstrap fetches the
// pinned release's install.sh (the ticket-10 verifier), installs the binary,
// is idempotent, and leaves no partial binary on a verification failure.
//
//nolint:cyclop,gocognit,revive // the sequence is the bootstrap contract
func TestShimBootstrapReusesInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX shim is not the Windows route")
	}
	for _, tool := range []string{"sh", "curl", "tar", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("bootstrap test needs %s: %v", tool, err)
		}
	}
	bin, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	release := t.TempDir()
	writeRelease(t, release, "1.2.3", "fake-verdict 1.2.3", false, bin)
	writeRelease(t, release, "2.0.0", "fake-verdict 2.0.0", true, bin)

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.FileServer(http.Dir(release)).ServeHTTP(w, r)
	}))
	defer srv.Close()

	toolbin := t.TempDir()
	writeScript(t, filepath.Join(toolbin, "uname"), `case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exec command -p uname "$@" ;;
esac`)
	writeScript(t, filepath.Join(toolbin, "sysctl"), `printf '0\n'`)
	writeScript(t, filepath.Join(toolbin, "cosign"), `exit 0`)
	path := toolbin + ":/usr/bin:/bin:/usr/sbin:/sbin"
	if curl, err := exec.LookPath("curl"); err == nil {
		path = toolbin + ":" + filepath.Dir(curl) + ":/usr/bin:/bin:/usr/sbin:/sbin"
	}

	dir := skillCopy(t)
	writeReceipt(t, dir, "1.2.3")
	env := shimEnv(t, path, map[string]string{
		"VERDICT_DOWNLOAD_URL": srv.URL,
		"VERDICT_COSIGN":       filepath.Join(toolbin, "cosign"),
	})
	code, _, derr := runShim(t, dir, env, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit = %d stderr %s", code, derr)
	}
	if requests.Load() == 0 {
		t.Fatal("bootstrap made no request")
	}
	installed, err := os.ReadFile(filepath.Join(dir, "bin", "verdict"))
	if err != nil {
		t.Fatalf("bootstrap did not install bin/verdict: %v", err)
	}
	if !strings.Contains(string(installed), "fake-verdict 1.2.3") {
		t.Fatalf("installed binary = %q", installed)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin", "verdict")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %v", err)
	}
	receipt, err := os.ReadFile(filepath.Join(dir, "bin", ".verdict-installer"))
	if err != nil || !strings.Contains(string(receipt), `"version":"1.2.3"`) {
		t.Fatalf("installer receipt = %q, %v", receipt, err)
	}

	requests.Store(0)
	code, out, _ := runShim(t, dir, env, "bootstrap")
	if code != 0 || !strings.Contains(out, "already bootstrapped") || requests.Load() != 0 {
		t.Fatalf("idempotent bootstrap = exit %d out %q requests %d", code, out, requests.Load())
	}

	if err := os.RemoveAll(filepath.Join(dir, "bin")); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, "2.0.0")
	code, _, derr = runShim(t, dir, env, "bootstrap")
	if code == 0 || !strings.Contains(derr, "bootstrap-failed") {
		t.Fatalf("bad bootstrap = exit %d stderr %q", code, derr)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "verdict")); !os.IsNotExist(err) {
		t.Fatal("failed bootstrap left a partial binary")
	}
}

// writeRelease lays out one release dir: install.sh, the contract archive,
// checksums, and a fixture signature bundle. corruptZeroes the archive hash.
func writeRelease(t *testing.T, root, version, binary string, corrupt bool, installer []byte) {
	t.Helper()
	dir := filepath.Join(root, "v"+version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), installer, 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("verdict_%s_linux_amd64.tar.gz", version)
	archive := filepath.Join(dir, name)
	// install.sh smoke-checks `--version` from the installed path, so the
	// payload has to be a script that answers it.
	writeTarGz(t, archive, map[string]string{
		"verdict":   fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", binary),
		"LICENSE":   "MIT\n",
		"README.md": "verdict\n",
		"NOTICE.md": "notices\n",
	})
	sum := sha256.Sum256(mustReadFile(t, archive))
	hash := hex.EncodeToString(sum[:])
	if corrupt {
		hash = strings.Repeat("0", 64)
	}
	checksums := fmt.Sprintf("%s  %s\n", hash, name)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt.sigstore.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeTarGz writes a tar.gz with the given members in sorted order.
//
//nolint:revive // the archive writer is linear; the error checks are the algorithm
func writeTarGz(t *testing.T, path string, members map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(members) {
		body := []byte(members[name])
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// sortedKeys returns map keys in ascending order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// writeReceipt pins a skill copy's version for bootstrap.
func writeReceipt(t *testing.T, dir, version string) {
	t.Helper()
	body := fmt.Sprintf("{\"tool\":\"verdict\",\"version\":%q,\"installed_at\":\"2026-11-02T00:00:00Z\"}\n", version)
	if err := os.WriteFile(filepath.Join(dir, ".verdict-managed"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustReadFile reads path or fails the test.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
