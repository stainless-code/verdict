package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/provenance"
)

const recordedPromptsHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestPostPromptsHashFromBundleReceipt exercises both post response paths and
// the persisted receipt against one bundle receipt, then checks the legacy
// fallback. All three surfaces must name the Procedure the agent got.
func TestPostPromptsHashFromBundleReceipt(t *testing.T) {
	if len(recordedPromptsHash) != 64 || strings.Trim(recordedPromptsHash, "0123456789abcdef") != "" {
		t.Fatal("test fixture is not a 64-hex hash")
	}

	t.Run("recorded bundle", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		postBundle(t, postReview)
		writeProvenanceFixture(t)
		writeBundleProvenanceHash(t, recordedPromptsHash)

		assertDryRunPromptsHash(t, recordedPromptsHash, "recorded bundle")
		assertSuccessPromptsHash(t, recordedPromptsHash, "recorded bundle")
	})

	t.Run("legacy bundle", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		postBundle(t, postReview)
		writeProvenanceFixture(t)

		assertDryRunPromptsHash(t, provenance.PromptsHash(), "legacy bundle")
		assertSuccessPromptsHash(t, provenance.PromptsHash(), "legacy bundle")
	})
}

func writeBundleProvenanceHash(t *testing.T, hash string) {
	t.Helper()
	if err := writeTestFile(".verdict/state/7/provenance.json",
		`{"schema_version":"1","prompts_hash":"`+hash+`"}`+"\n"); err != nil {
		t.Fatal(err)
	}
}

func assertDryRunPromptsHash(t *testing.T, want, label string) {
	t.Helper()
	resp, _, _ := runPostDryRun(t, false, true)
	if got := resp.Data.Provenance.PromptsHash; got != want {
		t.Errorf("%s dry-run response: prompts_hash = %q, want %q", label, got, want)
	}
}

func assertSuccessPromptsHash(t *testing.T, want, label string) {
	t.Helper()
	var warnings, human []string
	req := postTestReq(postCommander(t),
		Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("%s success refused: %s: %s", label, ref.Code, ref.Message)
	}
	if got := resp.Data.Provenance.PromptsHash; got != want {
		t.Errorf("%s success response: prompts_hash = %q, want %q", label, got, want)
	}
	receipt := decodePostedPayload(t, readTestFile(t, ".verdict/state/7/post.json"))
	if got := receipt["prompts_hash"]; got != want {
		t.Errorf("%s post.json: prompts_hash = %v, want %q", label, got, want)
	}
}
