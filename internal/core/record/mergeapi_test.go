package recordcore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func sparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func openFDCount(t *testing.T) (int, bool) {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}

type agentFileCase struct {
	name    string
	path    string
	wantLen int
	wantRef bool
	wantErr bool
}

func checkAgentFileCase(t *testing.T, tc agentFileCase) {
	t.Helper()
	raw, err := readAgentFile(tc.path)
	var ref *core.RefusalError
	if isRef := errors.As(err, &ref); isRef != tc.wantRef {
		t.Fatalf("err = %v, want typed refusal = %v", err, tc.wantRef)
	}
	if tc.wantRef {
		if ref.Code != codeBadReviewInput {
			t.Errorf("refusal code = %q, want %q", ref.Code, codeBadReviewInput)
		}
		return
	}
	if (err != nil) != tc.wantErr {
		t.Fatalf("err = %v, want any error = %v", err, tc.wantErr)
	}
	if !tc.wantErr && len(raw) != tc.wantLen {
		t.Errorf("len(raw) = %d, want %d", len(raw), tc.wantLen)
	}
}

// TestReadAgentFileCap is the size boundary table: a missing file reports the
// sealed read error, exactly-cap input is accepted, and over-cap input
// reports the existing bad-review-input refusal. Every call must close its
// handle.
func TestReadAgentFileCap(t *testing.T) {
	dir := t.TempDir()
	exact := filepath.Join(dir, "exact.json")
	sparseFile(t, exact, maxAgentBytes)
	over := filepath.Join(dir, "over.json")
	sparseFile(t, over, maxAgentBytes+1)
	missing := filepath.Join(dir, "missing.json")

	before, fdOK := openFDCount(t)
	cases := []agentFileCase{
		{name: "missing", path: missing, wantErr: true},
		{name: "exact-cap", path: exact, wantLen: maxAgentBytes},
		{name: "over-cap", path: over, wantRef: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkAgentFileCase(t, tc) })
	}
	if fdOK {
		after, ok := openFDCount(t)
		if ok && after > before {
			t.Errorf("open fds grew from %d to %d: readAgentFile leaks handles", before, after)
		}
	}
}

// TestReadAgentFileOverCapDoesNotAllocateTheFile proves the cap is enforced
// while reading, not after: a 256 MiB file refuses with the 32 MiB cap's
// allocation profile instead of materializing the whole file.
func TestReadAgentFileOverCapDoesNotAllocateTheFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sparse-file creation is not portable to the Windows test runner")
	}
	path := filepath.Join(t.TempDir(), "huge.json")
	sparseFile(t, path, 256<<20)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := readAgentFile(path)
	runtime.ReadMemStats(&after)

	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != codeBadReviewInput {
		t.Fatalf("err = %v, want bad-review-input", err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 160<<20 {
		t.Errorf("read allocated %d bytes, want <= 160 MiB (the file is 256 MiB)", grown)
	}
}

// TestLoadReviewRefusesOverCap proves the caller keeps the typed refusal and
// never reaches the decoder.
func TestLoadReviewRefusesOverCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	sparseFile(t, path, maxAgentBytes+1)
	_, err := LoadReview(path)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != codeBadReviewInput {
		t.Fatalf("err = %v, want bad-review-input", err)
	}
}
