package triagecore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	testSig       = "c111111111111111111111111111111111111111"
	testRationale = "This finding is a false alarm, verified against the diff."
	testWithheld  = `{"sig":"` + testSig + `","status":"open","history":[],` +
		`"withheld":{"source":"agent","reason":"operator suppressed",` +
		`"at":"2026-01-01T00:00:00Z"}}`
)

// withheldRoot writes one bundle whose single finding is policy-withheld and
// returns its root and findings path.
func withheldRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "1234")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(path, []byte("["+testWithheld+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, path
}

// withheldRequest builds one triage flip of the withheld finding with the
// given withholding posture.
func withheldRequest(root string, refuse bool) *Request {
	out := &Request{
		Sig:            testSig,
		Status:         "false-positive",
		Rationale:      testRationale,
		RefuseWithheld: refuse,
	}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Now: func() time.Time {
			return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		}},
		RepoRoot: root,
		Pr:       "1234",
	}
	return out
}

// TestRunWithheldRefused pins the opt-in posture: a withheld finding refuses
// with unknown-sig and the store is untouched.
func TestRunWithheldRefused(t *testing.T) {
	t.Parallel()
	root, path := withheldRoot(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, ref := Run(t.Context(), withheldRequest(root, true))
	if ref == nil {
		t.Fatal("Run = nil refusal, want the withheld unknown-sig refusal")
	}
	if ref.Code != "unknown-sig" {
		t.Errorf("refusal code = %q, want unknown-sig", ref.Code)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("findings = %s, want untouched", after)
	}
}

// TestRunRefusesOutdatedStore pins the shared store gate: a findings.json an
// older verdict wrote (a bare verification array) refuses bundle-outdated
// before triage mutates it, and the store is untouched.
func TestRunRefusesOutdatedStore(t *testing.T) {
	t.Parallel()
	root, path := withheldRoot(t)
	old := `[{"sig":"` + testSig + `","status":"open","history":[],"verification":[{"model":"a","verdict":"refuted"}]}]`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := Run(t.Context(), withheldRequest(root, false))
	if ref == nil || ref.Code != "bundle-outdated" {
		t.Fatalf("Run = %v, want bundle-outdated", ref)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != old {
		t.Errorf("findings = %s, want untouched", after)
	}
}

// TestRunWithheldDefaultFlips pins the CLI default: without the opt-in, the
// same withheld finding flips like any other.
func TestRunWithheldDefaultFlips(t *testing.T) {
	t.Parallel()
	root, path := withheldRoot(t)
	resp, ref := Run(t.Context(), withheldRequest(root, false))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if resp.Data.Sig != testSig || resp.Data.Status != "false-positive" {
		t.Errorf("data = %+v, want the flipped sig and status", resp.Data)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"false-positive"`) {
		t.Errorf("findings = %s, want the false-positive flip", raw)
	}
}
