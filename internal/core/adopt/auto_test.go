package adoptcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const autoTestPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// autoBundle writes one bundle carrying the given patch identity; local
// bundles also get a findings store so they adopt.
func autoBundle(t *testing.T, bundle, patch string, findings bool) {
	t.Helper()
	dir := ".review/" + bundle
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ident := `{"schema_version":"1","patch_id":"` + patch + `","patch_id_kind":"git-patch-id",` +
		`"diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(ident), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"),
		[]byte("diff --git a/n b/n\nindex 1..2 100644\n--- a/n\n+++ b/n\n@@ -1 +1 @@\n-a\n+b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if findings {
		entry := `{"kind":"inline","path":"n","side":"RIGHT","line":1,"anchored":true,"body":"B.",` +
			`"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1",` +
			`"firstSeen":"a","lastSeen":"a","status":"open","rationale":"","history":[]}`
		if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte("["+entry+"]"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func autoRequest(from string) *Request {
	req := &Request{From: from}
	req.Repo, req.Pr = "acme/app", "1648"
	return req
}

// TestAdoptAutoMatch proves --from auto adopts exactly one twin and prints
// the mapping with the patch id.
func TestAdoptAutoMatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", autoTestPatch, false)
	autoBundle(t, "local-twin", autoTestPatch, true)
	autoBundle(t, "local-other", "0123456789abcdef0123456789abcdef0123456789", true)
	resp, ref := Run(t.Context(), autoRequest("auto"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.From != "auto -> local-twin" {
		t.Errorf("data.from = %q, want the auto mapping", resp.Data.From)
	}
	if !strings.Contains(resp.Human, "auto -> local-twin") || !strings.Contains(resp.Human, autoTestPatch) {
		t.Errorf("human line misses the mapping: %q", resp.Human)
	}
	raw, err := os.ReadFile(".review/1648/findings.json")
	if err != nil {
		t.Fatalf("adopted findings: %v", err)
	}
	if !strings.Contains(string(raw), `"note": "from auto -> local-twin"`) {
		t.Errorf("handoff stamp misses the mapping:\n%s", raw)
	}
}

// TestAdoptAutoNoTwin proves zero twins refuse no-patch-match naming the
// target patch id.
func TestAdoptAutoNoTwin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", autoTestPatch, false)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
	if !strings.Contains(ref.Message, autoTestPatch) {
		t.Errorf("message misses the patch id: %q", ref.Message)
	}
}

// TestAdoptAutoAmbiguous proves many twins refuse ambiguous-patch-match
// naming the candidates.
func TestAdoptAutoAmbiguous(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", autoTestPatch, false)
	autoBundle(t, "local-a", autoTestPatch, true)
	autoBundle(t, "local-b", autoTestPatch, true)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "ambiguous-patch-match" {
		t.Fatalf("refusal = %v, want ambiguous-patch-match", ref)
	}
	if !strings.Contains(ref.Message, "local-a") || !strings.Contains(ref.Message, "local-b") {
		t.Errorf("message misses the candidates: %q", ref.Message)
	}
}

// TestAdoptAutoNoTargetIdentity proves a target without identity refuses
// no-patch-match with the re-fetch hint.
func TestAdoptAutoNoTargetIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(".review/1648", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".review/1648/full.diff", []byte("diff --git a/n b/n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	autoBundle(t, "local-twin", autoTestPatch, true)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
}

// TestAdoptLocalAutoSentinel proves a local bundle literally named
// local-auto stays reachable as --from local-auto (the sentinel is matched
// before the local- prefix rule).
func TestAdoptLocalAutoSentinel(t *testing.T) {
	st := &state{req: autoRequest("local-auto")}
	dir, fromDir, display, ref := st.resolveAdoptDirs()
	if ref != nil {
		t.Fatalf("resolveAdoptDirs refusal: %v", ref)
	}
	if dir != ".review/1648" || fromDir != ".review/local-auto" || display != "local-auto" {
		t.Errorf("resolve = %q %q %q", dir, fromDir, display)
	}
}
