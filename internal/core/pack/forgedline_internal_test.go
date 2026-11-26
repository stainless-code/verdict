package pack

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A lock entry name comes from the repository's packs lock file.
func TestLockNameCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	lock := &Lock{Packs: []LockEntry{{Name: "acme/" + forgedline.Value}}}
	forgedline.AssertOneErrorLine(t, lockSlugConflict(lock, "other/pack", packSlug("acme/"+forgedline.Value)),
		"would vendor over acme/"+forgedline.Named)
}
