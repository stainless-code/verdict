package run

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// auxArtifactMiB is the size of the auxiliary finder artifact the streaming
// test binds: no phase file is size-capped, so the checkpoint has to hash one
// this large without holding it.
const auxArtifactMiB = 32

// TestSnapshotCheckStreamsPhaseArtifacts proves the checkpoint hashes a phase
// artifact instead of loading it, and still notices a changed byte in it.
func TestSnapshotCheckStreamsPhaseArtifacts(t *testing.T) {
	dir := t.TempDir()
	phase := "x-finder-1"
	if err := os.MkdirAll(filepath.Join(dir, phase), 0o700); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("a"), auxArtifactMiB<<20)
	if err := os.WriteFile(filepath.Join(dir, phase, "aux.txt"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := executionSnapshot{}
	if ref := snapshot.bindPhase(dir, phase); ref != nil {
		t.Fatal(ref)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if ref := snapshot.check(dir); ref != nil {
		t.Fatal(ref)
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 4<<20 {
		t.Fatalf("checking a %d MiB artifact allocated %d bytes; the digest has to stream", auxArtifactMiB, got)
	}

	changed := append([]byte("b"), big[1:]...)
	if err := os.WriteFile(filepath.Join(dir, phase, "aux.txt"), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := snapshot.check(dir)
	if ref == nil || ref.Code != "stale-review-artifact" || ref.Message != "execution snapshot changed: "+filepath.Join(phase, "aux.txt") {
		t.Fatalf("changed artifact = %v", ref)
	}
}
