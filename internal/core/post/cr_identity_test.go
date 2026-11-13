package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestCRPostPreservesVerifiedPatchIdentity(t *testing.T) {
	t.Chdir(t.TempDir())
	postBundle(t, postReview)
	id := identity.Build(t.Context(), nil, []byte(patchGuardDiff), []byte(patchGuardDiff), liveGuardHead, patchGuardBase)
	if err := writeTestFile(".verdict/state/7/identity.json", id.Wire().MarshalPretty()); err != nil {
		t.Fatal(err)
	}
	var warnings, human []string
	req := postTestReq(postCommander(t), core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	raw, err := jq.CappedFile(".verdict/state/7/post.json", jq.MaxPRBytes)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.Decode(raw)
	m, ok := doc.(map[string]any)
	if err != nil || !ok || m["patch_id"] != "5f607e8e74a3f4598898f553301b0c4a3c8f14b46a56bb2208ed964550c6a58f" || m["patch_id_kind"] != "diff-sha256" {
		t.Fatalf("patch provenance lost: %v, %v", doc, err)
	}
}
