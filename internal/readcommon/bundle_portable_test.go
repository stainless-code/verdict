package readcommon_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestPortableBundlePaths pins the wire contract every bundle path shares:
// slash-form on every OS, so human lines, data profiles, and goldens never
// carry a native separator. It holds vacuously on Unix and guards Windows.
func TestPortableBundlePaths(t *testing.T) {
	t.Parallel()
	paths := []string{
		readcommon.ResolveBundleDir(".", "42"),
		readcommon.ResolveBundleDir(".", "local-7"),
		readcommon.ResolveBundleDir(".", "corpus-7"),
		readcommon.ResolveBundleDir("/repo", "42"),
		readcommon.StateDir("."),
		readcommon.StateDir("/repo"),
	}
	for _, got := range paths {
		if strings.Contains(got, "\\") {
			t.Errorf("bundle path %q carries a native separator; want slash-form", got)
		}
	}
	if got := readcommon.ResolveBundleDir("/repo", "42"); got != "/repo/.verdict/state/42" {
		t.Errorf("ResolveBundleDir(/repo, 42) = %q; want the slash-form bundle dir", got)
	}
}
