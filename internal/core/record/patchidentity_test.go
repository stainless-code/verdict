package recordcore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

const recordTestPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

const recordTestDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// stampIdentity writes the byte-bound identity for the record fixture
// diff, so the run stamps a verified patch id.
func stampIdentity(t *testing.T, slug string) {
	t.Helper()
	sum := sha256.Sum256([]byte(recordTestDiff))
	sha := hex.EncodeToString(sum[:])
	writeFile(t, ".verdict/state/"+slug+"/identity.json",
		`{"schema_version":"1","patch_id":"`+recordTestPatch+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"`+sha+`","raw_diff_sha256":"`+sha+`",`+
			`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
}

// recordStubCommander answers `git patch-id --stable` for the fixture diff
// and fails anything else exactly like an unavailable git.
type recordStubCommander struct{}

func (c *recordStubCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 && cmd.Argv[0] == "git" && cmd.Stdin != nil {
		raw, err := io.ReadAll(cmd.Stdin)
		if err == nil && string(raw) == recordTestDiff {
			return core.Result{Stdout: recordTestPatch + " 0000000000000000000000000000000000000000\n"}, nil
		}
	}
	return core.Result{ExitCode: 1}, errors.New("cannot compute patch id")
}

// runRecordPatch runs record over the named bundle and returns its
// record.json.
func runRecordPatch(t *testing.T, slug string) map[string]any {
	t.Helper()
	c := &collector{}
	req := recordRequest(t, c, slug)
	req.Commander = &recordStubCommander{}
	if _, ref := recordcore.Run(context.Background(), req); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return readRecord(t, slug)
}

// TestRunRecordStampsPatchIdentity proves record.json carries the bundle
// patch identity when it verifies against the bundle bytes.
func TestRunRecordStampsPatchIdentity(t *testing.T) {
	bundle(t, "local-id", reviewJSON(""))
	stampIdentity(t, "local-id")
	rec := runRecordPatch(t, "local-id")
	if rec["patch_id"] != recordTestPatch {
		t.Errorf("record.json patch_id = %v, want the bundle patch", rec["patch_id"])
	}
	if rec["patch_id_kind"] != "git-patch-id" {
		t.Errorf("record.json patch_id_kind = %v, want git-patch-id", rec["patch_id_kind"])
	}
}

// TestRunRecordOmitsPatchIdentityLegacy proves a bundle without identity
// records neither patch key.
func TestRunRecordOmitsPatchIdentityLegacy(t *testing.T) {
	bundle(t, "local-legacy", reviewJSON(""))
	legacy := runRecordPatch(t, "local-legacy")
	if _, ok := legacy["patch_id"]; ok {
		t.Errorf("legacy record.json carries patch_id: %v", legacy["patch_id"])
	}
	if _, ok := legacy["patch_id_kind"]; ok {
		t.Errorf("legacy record.json carries patch_id_kind: %v", legacy["patch_id_kind"])
	}
}

// TestRunRecordOmitsPatchIdentityStale proves a stale identity never stamps
// record.json.
func TestRunRecordOmitsPatchIdentityStale(t *testing.T) {
	bundle(t, "local-stale", reviewJSON(""))
	stampIdentity(t, "local-stale")
	writeFile(t, ".verdict/state/local-stale/identity.json",
		`{"schema_version":"1","patch_id":"`+recordTestPatch+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"0000000000000000000000000000000000000000000000000000000000000000",`+
			`"raw_diff_sha256":"0000000000000000000000000000000000000000000000000000000000000000",`+
			`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	stale := runRecordPatch(t, "local-stale")
	if _, ok := stale["patch_id"]; ok {
		t.Errorf("stale record.json carries patch_id: %v", stale["patch_id"])
	}
}

// TestRunRecordOmitsTamperedPatchIdentity proves a hex-valid patch claim
// that does not reproduce the raw input is never stamped into record.json.
func TestRunRecordOmitsTamperedPatchIdentity(t *testing.T) {
	bundle(t, "local-tampered", reviewJSON(""))
	stampIdentity(t, "local-tampered")
	raw, err := os.ReadFile(".verdict/state/local-tampered/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), recordTestPatch, strings.Repeat("1", 40), 1)
	writeFile(t, ".verdict/state/local-tampered/identity.json", tampered)
	rec := runRecordPatch(t, "local-tampered")
	if _, ok := rec["patch_id"]; ok {
		t.Errorf("tampered record.json carries patch_id: %v", rec["patch_id"])
	}
}
