//go:build !windows

package postcore

import (
	"context"
	"testing"
)

// TestSiblingBundleNameCannotForgeErrorLine covers the one stranger value in
// the same-patch refusal: the sibling's directory name, which the receipt
// checks never see. Windows cannot create a name that carries a newline.
func TestSiblingBundleNameCannotForgeErrorLine(t *testing.T) {
	t.Chdir(t.TempDir())
	patchGuardBundleAt(t, forged, patchGuardPatch, "git-patch-id",
		"cccccccccccccccccccccccccccccccccccccccc", patchGuardDiff, "https://github.com/acme/app/pull/1647", true)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	var warnings []string
	st := patchGuardState(Flags{}, &warnings)
	assertOneErrorLine(t, st.patchGuard(context.Background()), "state/x error: forged at head")
}
