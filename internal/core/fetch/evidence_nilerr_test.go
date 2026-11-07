package fetchcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nilerrSession builds the minimal session a prompt dump needs: warnings are
// collected, nothing is written to a stream.
func nilerrSession() *session {
	var warnings []string
	return &session{Human: nil, Diag: nil, warnings: &warnings}
}

// TestDiffHashesUnreadablePatchRefuses pins the fail-closed posture: a patch
// path that exists but cannot be read as a file (a directory where
// full.diff belongs) is not "no diff" — the run must not record empty hashes
// and certify itself unbound.
func TestDiffHashesUnreadablePatchRefuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "full.diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, filtered, ref := diffHashes(dir, nil)
	if ref == nil {
		t.Fatalf("diffHashes = (%q, %q, nil), want a refusal", raw, filtered)
	}
	if ref.Code != CodeFetchFailed {
		t.Fatalf("refusal code = %q, want %q (%s)", ref.Code, CodeFetchFailed, ref.Message)
	}
	if raw != "" || filtered != "" {
		t.Fatalf("refusal carried hashes (%q, %q), want none", raw, filtered)
	}
}

// TestDiffHashesAbsentPatchReadsEmpty pins the contracted metadata-only case:
// a bundle without a patch records both hashes empty.
func TestDiffHashesAbsentPatchReadsEmpty(t *testing.T) {
	raw, filtered, ref := diffHashes(t.TempDir(), nil)
	if ref != nil {
		t.Fatalf("diffHashes: %v (%s)", ref, ref.Message)
	}
	if raw != "" || filtered != "" {
		t.Fatalf("hashes = (%q, %q), want both empty", raw, filtered)
	}
}

// TestPromptDumpUnreadablePatchRefuses pins that an unreadable patch refuses
// the dump instead of silently handing the model a dump without its diff,
// and that the refusal carries no patch bytes.
func TestPromptDumpUnreadablePatchRefuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "full.diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := nilerrSession()
	ctx := &fetchRunCtx{runner: runner, dir: dir}
	var sb strings.Builder
	ref := dumpFullDiffFallback(ctx, &sb, "nonce")
	if ref == nil {
		t.Fatalf("dump wrote %q; want a refusal", sb.String())
	}
	if ref.Code != CodeFetchFailed {
		t.Fatalf("refusal code = %q, want %q (%s)", ref.Code, CodeFetchFailed, ref.Message)
	}
	if sb.Len() != 0 {
		t.Fatalf("refused dump wrote %q, want nothing fenced", sb.String())
	}
}

// TestPromptDumpAbsentPatchSkips pins the legitimate absence: no full.diff
// fences nothing and refuses nothing.
func TestPromptDumpAbsentPatchSkips(t *testing.T) {
	runner := nilerrSession()
	ctx := &fetchRunCtx{runner: runner, dir: t.TempDir()}
	var sb strings.Builder
	if ref := dumpFullDiffFallback(ctx, &sb, "nonce"); ref != nil {
		t.Fatalf("dump: %v (%s)", ref, ref.Message)
	}
	if sb.Len() != 0 {
		t.Fatalf("absent patch wrote %q, want nothing", sb.String())
	}
}
