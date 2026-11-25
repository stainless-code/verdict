package initcore

import (
	"os"
	"path/filepath"
	"slices"
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
// directories the lockfiles at root imply.
func proposedShares(root string) []string {
	var dirs []string
	for _, p := range shareProposals {
		info, err := os.Lstat(filepath.Join(root, p.lockfile))
		if err != nil || !info.Mode().IsRegular() || slices.Contains(dirs, p.dir) {
			continue
		}
		dirs = append(dirs, p.dir)
	}
	return dirs
}
