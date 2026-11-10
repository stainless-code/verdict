package postcore

import (
	"context"
	"os"
	"runtime"
	"testing"
)

// TestPostSidecarsOwnerOnly proves both post stderr sidecars land owner-only:
// the bytes can carry token-adjacent forge diagnostics.
func TestPostSidecarsOwnerOnly(t *testing.T) {
	t.Parallel()
	t.Run("post", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cmdr := &retryCommander{t: t, results: []retryResult{{stderr: "HTTP 401: bad credentials\n"}}}
		if res := postOnce(context.Background(), cmdr, providerGitHub, nil, "acme/app", "7", dir, "{}"); !res.ok {
			t.Fatal("post attempt failed")
		}
		assertSidecar(t, dir+"/.post.err", "HTTP 401: bad credentials\n")
	})
	t.Run("recheck", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cmdr := &retryCommander{t: t, results: []retryResult{{
			stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`,
		}}}
		if _, _, err := postRecheck(context.Background(), cmdr, "acme/app", "7", dir); err != nil {
			t.Fatalf("recheck: %v", err)
		}
		assertSidecar(t, dir+"/.recheck.err", "")
	})
}

func assertSidecar(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Errorf("%s = %q; want %q", path, raw, want)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("%s mode = %o; want 600", path, fi.Mode().Perm())
	}
}
