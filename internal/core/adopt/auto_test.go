package adoptcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const autoTestPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

const autoTestDiff = "diff --git a/n b/n\nindex 1..2 100644\n--- a/n\n+++ b/n\n@@ -1 +1 @@\n-a\n+b\n"

const autoTestOtherDiff = "diff --git a/n b/n\nindex 1..2 100644\n--- a/n\n+++ b/n\n@@ -1 +1 @@\n-a\n+c\n"

// autoBundle writes one coherent bundle carrying the canonical test patch:
// byte-bound full.diff and identity, a PR head record for targets, and a
// local source record (with the requested repository) plus findings for
// twins. Foreign twins pass repo "" to stamp a different repository.
func autoBundle(t *testing.T, bundle, repo string, findings bool) {
	t.Helper()
	autoBundleAt(t, bundle, autoTestPatch, autoTestDiff, repo, findings)
}

// autoBundleAt is autoBundle with an explicit patch and diff, for the
// changed-patch twin.
func autoBundleAt(t *testing.T, bundle, patch, diff, repo string, findings bool) {
	t.Helper()
	autoBundleFiles(t, bundle, patch, diff)
	if strings.HasPrefix(bundle, "local-") {
		autoTwinRecords(t, bundle, repo, findings)
		return
	}
	autoTargetRecord(t, bundle)
}

// autoBundleFiles writes the byte-bound full.diff and identity shared by
// targets and twins.
func autoBundleFiles(t *testing.T, bundle, patch, diff string) {
	t.Helper()
	dir := ".verdict/state/" + bundle
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(diff))
	sha := hex.EncodeToString(sum[:])
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ident := `{"schema_version":"1","patch_id":"` + patch + `","patch_id_kind":"git-patch-id",` +
		`"diff_sha256":"` + sha + `","raw_diff_sha256":"` + sha + `","head_sha":"` + head + `","base_sha":"` + base + `"}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(ident), 0o644); err != nil {
		t.Fatal(err)
	}
}

// autoTwinRecords writes the local source record and findings store that
// make a bundle an adoptable twin.
func autoTwinRecords(t *testing.T, bundle, repo string, findings bool) {
	t.Helper()
	dir := ".verdict/state/" + bundle
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	source := `{"schema_version":"1","repo":"` + repo + `","head_sha":"` + head + `","base_sha":"` + base + `"}`
	if err := os.WriteFile(filepath.Join(dir, "source.json"), []byte(source), 0o644); err != nil {
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

// autoTargetRecord writes the PR head record that makes a bundle an adopt
// target in the requested repository.
func autoTargetRecord(t *testing.T, bundle string) {
	t.Helper()
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pr := `{"state":"OPEN","headRefOid":"` + head + `","baseRefOid":"` + base + `",` +
		`"url":"https://github.com/acme/app/pull/` + bundle + `"}`
	if err := os.WriteFile(filepath.Join(".verdict/state/"+bundle, "PR.json"), []byte(pr), 0o644); err != nil {
		t.Fatal(err)
	}
}

func autoRequest(from string) *Request {
	req := &Request{From: from}
	req.Repo, req.Pr = "acme/app", "1648"
	req.Runtime = &core.Runtime{Commander: autoCommander()}
	return req
}

// autoCommander answers `git patch-id --stable` for the fixture diffs and
// fails anything else exactly like an unavailable git.
func autoCommander() core.Commander {
	return &autoStubCommander{ids: map[string]string{
		autoTestDiff:      autoTestPatch,
		autoTestOtherDiff: "0123456789abcdef0123456789abcdef01234567",
	}}
}

type autoStubCommander struct {
	ids map[string]string
}

func (c *autoStubCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 && cmd.Argv[0] == "git" && cmd.Stdin != nil {
		raw, err := io.ReadAll(cmd.Stdin)
		if err == nil {
			if id, ok := c.ids[string(raw)]; ok {
				return core.Result{Stdout: id + " 0000000000000000000000000000000000000000\n"}, nil
			}
		}
	}
	return core.Result{ExitCode: 1}, errors.New("cannot compute patch id")
}

// TestAdoptAutoMatch proves --from auto adopts exactly one twin and prints
// the mapping with the patch id.
func TestAdoptAutoMatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", "", false)
	autoBundle(t, "local-twin", "acme/app", true)
	autoBundleAt(t, "local-other", "0123456789abcdef0123456789abcdef01234567", autoTestOtherDiff, "acme/app", true)
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
	raw, err := os.ReadFile(".verdict/state/1648/findings.json")
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
	autoBundle(t, "1648", "", false)
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
	autoBundle(t, "1648", "", false)
	autoBundle(t, "local-a", "acme/app", true)
	autoBundle(t, "local-b", "acme/app", true)
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
	if err := os.MkdirAll(".verdict/state/1648", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".verdict/state/1648/full.diff", []byte("diff --git a/n b/n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	autoBundle(t, "local-twin", "acme/app", true)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
}

// TestAdoptAutoCrossRepoExcludes proves a same-patch twin from a foreign
// repository is not a twin: patch identity never crosses repo boundaries.
func TestAdoptAutoCrossRepoExcludes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", "", false)
	autoBundle(t, "local-foreign", "other/repository", true)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
}

// TestAdoptAutoStaleTwinExcludes proves a twin whose bytes no longer bind
// its identity is not selected.
func TestAdoptAutoStaleTwinExcludes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", "", false)
	autoBundle(t, "local-twin", "acme/app", true)
	if err := os.WriteFile(".verdict/state/local-twin/full.diff", []byte("replaced bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	dir, fromDir, display, ref := st.resolveAdoptDirs(context.Background())
	if ref != nil {
		t.Fatalf("resolveAdoptDirs refusal: %v", ref)
	}
	if dir != ".verdict/state/1648" || fromDir != ".verdict/state/local-auto" || display != "local-auto" {
		t.Errorf("resolve = %q %q %q", dir, fromDir, display)
	}
}

// TestAdoptAutoTamperedTwinExcludes proves a twin whose patch claim does
// not reproduce the raw input is never selected, even with coherent hashes
// and head in the requested repository.
func TestAdoptAutoTamperedTwinExcludes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", "", false)
	autoBundle(t, "local-twin", "acme/app", true)
	raw, err := os.ReadFile(".verdict/state/local-twin/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), autoTestPatch, strings.Repeat("1", 40), 1)
	if err := os.WriteFile(".verdict/state/local-twin/identity.json", []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
}

// TestAdoptAutoTamperedTargetRefuses proves a target whose patch claim does
// not reproduce its bytes refuses with the re-fetch hint.
func TestAdoptAutoTamperedTargetRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	autoBundle(t, "1648", "", false)
	raw, err := os.ReadFile(".verdict/state/1648/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), autoTestPatch, strings.Repeat("1", 40), 1)
	if err := os.WriteFile(".verdict/state/1648/identity.json", []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	autoBundle(t, "local-twin", "acme/app", true)
	_, ref := Run(t.Context(), autoRequest("auto"))
	if ref == nil || ref.Code != "no-patch-match" {
		t.Fatalf("refusal = %v, want no-patch-match", ref)
	}
	if !strings.Contains(ref.Message, "re-fetch") {
		t.Errorf("message misses the re-fetch hint: %q", ref.Message)
	}
}
