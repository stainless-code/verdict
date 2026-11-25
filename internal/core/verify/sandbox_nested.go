package verifycore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// sandboxNestedMaxDirs bounds the nested-repository walk. A tree past it
// refuses rather than leave the rest unsearched.
const sandboxNestedMaxDirs = 200_000

// sandboxNested is what the reviewed root holds besides its own `.git`
// that git would enter on its own:
//
//   - dotgits: every nested `.git` (a directory, or a `gitdir:` file) — a
//     submodule checked out with its own git directory, a nested clone, a
//     vendored repository. Git honors it when it runs inside that tree, and
//     `git status` in the superproject runs git inside each populated
//     submodule, so a hook or `core.fsmonitor` planted there runs on the
//     operator's next git command.
//   - gitlinks: submodule paths (gitlink entries in the root's index) that
//     are not checked out yet. A `.git` the command created there would make
//     the submodule look populated to that same `git status`.
//
// `.gitmodules` adds nothing: git enters a submodule by its gitlink, and a
// `.gitmodules` entry without one names nothing it runs in.
type sandboxNested struct {
	dotgits  []string
	gitlinks []string
}

// sandboxFindNested walks the root for nested `.git` entries, never
// descending into a git directory, and reads the gitlinks from the root's
// index in gitdir. Every refusal fails closed: a symlinked nested `.git`, a
// directory the walk cannot read, a tree past [sandboxNestedMaxDirs], an
// index it cannot read, and a gitlink path that is missing or a symlink.
func sandboxFindNested(root sandboxGitRoot, gitdir string, maxDirs int) (sandboxNested, *core.RefusalError) {
	var nested sandboxNested
	walk := sandboxNestedWalk{root: root, nested: &nested, maxDirs: maxDirs}
	if ref := walk.run(); ref != nil {
		return sandboxNested{}, ref
	}
	gitlinks, ref := sandboxUnpopulatedGitlinks(root, gitdir)
	nested.gitlinks = gitlinks
	return nested, ref
}

// sandboxNestedWalk is the state of one nested-repository walk.
type sandboxNestedWalk struct {
	nested  *sandboxNested
	root    sandboxGitRoot
	dirs    int
	maxDirs int
}

// run walks the root, never descending into a `.git` directory; the
// first refusal stops the walk.
func (w *sandboxNestedWalk) run() *core.RefusalError {
	err := filepath.WalkDir(w.root.repo, func(path string, d fs.DirEntry, err error) error {
		if ref := w.visit(path, d, err); ref != nil {
			return ref
		}
		if d != nil && d.IsDir() && d.Name() == sandboxGitName {
			return filepath.SkipDir
		}
		return nil
	})
	var ref *core.RefusalError
	if err != nil && !errors.As(err, &ref) {
		ref = sandboxGitInspectRefusal(w.root.repo, err)
	}
	return ref
}

// visit records one walked entry.
func (w *sandboxNestedWalk) visit(path string, d fs.DirEntry, err error) *core.RefusalError {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return sandboxGitInspectRefusal(path, err)
	case d.IsDir():
		if w.dirs++; w.dirs > w.maxDirs {
			return sandboxUnavailable(fmt.Sprintf("the reviewed root %s holds more than %d directories, too many to "+
				"search for nested git repositories — verify a smaller checkout, or run the review in a throwaway "+
				"VM or container", w.root.repo, w.maxDirs))
		}
	}
	if d.Name() != sandboxGitName || path == filepath.Join(w.root.repo, sandboxGitName) {
		return nil
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return sandboxGitSymlinkedDotRefusal(path)
	}
	w.nested.dotgits = append(w.nested.dotgits, path)
	return nil
}

// sandboxUnpopulatedGitlinks returns the root's gitlink paths that hold no
// `.git` yet, refusing one whose directory is missing or is a symlink:
// neither can be held read-only, and either could become a populated
// submodule.
func sandboxUnpopulatedGitlinks(root sandboxGitRoot, gitdir string) ([]string, *core.RefusalError) {
	index := filepath.Join(gitdir, "index")
	links, present, reason := sandboxGitlinks(index)
	switch {
	case !present:
		return nil, nil
	case reason != "":
		return nil, sandboxUnavailable(fmt.Sprintf("could not read the git index %s (%s), so its submodule paths "+
			"cannot be protected", index, reason))
	}
	var unpopulated []string
	for _, link := range links {
		dir := filepath.Join(root.repo, filepath.FromSlash(link))
		if _, err := os.Lstat(filepath.Join(dir, sandboxGitName)); err == nil {
			continue // populated: the walk found its .git
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return nil, sandboxUnavailable(fmt.Sprintf("the submodule path %s is missing or not a directory, so the "+
				"command could create it as a submodule git would enter — check the submodule out (`git submodule "+
				"update --init`) or remove it, or verify a fresh clone", dir))
		}
		unpopulated = append(unpopulated, dir)
	}
	return unpopulated, nil
}

// sandboxNestedPins plans the bwrap mounts that hold one path read-only for
// good: an `--ro-bind` of the path itself, after a read-write self-bind of
// every directory between the root and it. A bind mount cannot be renamed
// or removed (EBUSY), so the command can neither swap the read-only path
// out nor move an ancestor aside and recreate the path beneath a new
// directory. A path under a top-level entry that is already pinned
// read-only needs nothing more.
func sandboxNestedPins(path string, root sandboxGitRoot, pinned map[string]bool) (anchors []string, ok bool) {
	rel, inside := sandboxRelInside(root.repo, path)
	if !inside {
		return nil, false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if pinned[filepath.Join(root.repo, parts[0])] {
		return nil, false
	}
	for i := 1; i < len(parts); i++ {
		anchors = append(anchors, filepath.Join(append([]string{root.repo}, parts[:i]...)...))
	}
	return anchors, true
}
