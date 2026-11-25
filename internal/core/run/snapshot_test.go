package run

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const auxArtifactMiB = 32

// Not parallel: the allocation count is process-wide, so a parallel sibling's
// allocations would be charged to the digest.
func TestSnapshotCheckStreamsPhaseArtifacts(t *testing.T) {
	dir, phase, big := bigPhaseArtifact(t)
	snapshot := executionSnapshot{}
	if ref := snapshot.bindPhase(dir, phase); ref != nil {
		t.Fatal(ref)
	}
	if got := checkAllocation(t, snapshot, dir); got > 4<<20 {
		t.Fatalf("checking a %d MiB artifact allocated %d bytes; the digest has to stream", auxArtifactMiB, got)
	}

	changed := append([]byte("b"), big[1:]...)
	if err := os.WriteFile(filepath.Join(dir, phase, "aux.txt"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := snapshot.check(dir)
	if ref == nil || ref.Code != "stale-review-artifact" || ref.Message != "execution snapshot changed: x-finder-1/aux.txt" {
		t.Fatalf("changed artifact = %v", ref)
	}
}

func bigPhaseArtifact(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	phase := "x-finder-1"
	if err := os.MkdirAll(filepath.Join(dir, phase), 0o700); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("a"), auxArtifactMiB<<20)
	if err := os.WriteFile(filepath.Join(dir, phase, "aux.txt"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, phase, big
}

func checkAllocation(t *testing.T, snapshot executionSnapshot, dir string) uint64 {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if ref := snapshot.check(dir); ref != nil {
		t.Fatal(ref)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
