package initcore

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/contenttree"
)

// The dependency directories init can propose.
const (
	nodeModules = "node_modules"
	venv        = ".venv"
)

// shareProposals maps a lockfile at the repository root to the dependency
// directory its installer fills: the one directory a verify snapshot would
// otherwise have to install on every run.
var shareProposals = []struct{ lockfile, dir string }{
	{"package-lock.json", nodeModules},
	{"pnpm-lock.yaml", nodeModules},
	{"yarn.lock", nodeModules},
	{"uv.lock", venv},
	{"poetry.lock", venv},
}

// proposedShares lists, in table order and once each, the dependency
// directories the lockfiles at the repository root imply and verify would
// accept: git ignores the directory and tracks nothing in it. A directory
// .gitignore does not cover is left out, so init never writes a share whose
// first verify refuses; git is asked about it as a directory (a trailing
// slash), since a fresh clone may not have installed it yet.
func proposedShares(ctx context.Context, g contenttree.Git) []string {
	var dirs []string
	for _, p := range shareProposals {
		info, err := os.Lstat(filepath.Join(g.Root, p.lockfile))
		if err != nil || !info.Mode().IsRegular() || slices.Contains(dirs, p.dir) {
			continue
		}
		if acceptedShare(ctx, g, p.dir) {
			dirs = append(dirs, p.dir)
		}
	}
	return dirs
}

func acceptedShare(ctx context.Context, g contenttree.Git, dir string) bool {
	ignored, err := g.Ignored(ctx, dir+"/")
	if err != nil || !ignored {
		return false
	}
	tracked, err := g.Tracked(ctx, dir)
	return err == nil && !tracked
}
