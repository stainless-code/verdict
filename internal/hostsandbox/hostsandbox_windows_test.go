package hostsandbox

import "testing"

func TestResolvePathRefusesMissingTailWithForwardSlashes(t *testing.T) {
	t.Parallel()
	assertRefused(t, t.TempDir()+"/a/../b")
}

func TestRawDirPreservesUnresolvedParentWithForwardSlashes(t *testing.T) {
	t.Parallel()
	const path = `C:\base/a/../db`
	const want = `C:\base/a/..`
	if got := rawDir(path); got != want {
		t.Errorf("rawDir(%q) = %q, want %q", path, got, want)
	}
}
