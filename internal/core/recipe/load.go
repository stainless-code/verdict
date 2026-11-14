package recipe

import (
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// lensFile is the bundle artifact fetch writes the layered lens bodies into,
	// one labeled header per lens in load order.
	lensFile = "lenses.txt"
	// maxLensesBytes bounds that artifact's read. A bundle over it loads no
	// lens: the lens set is context, so its absence warns instead of refusing.
	maxLensesBytes = 8 << 20
)

// Load resolves one recipe for a run bundle: the recipe control from the base
// snapshot (never the worktree), the bundle's loaded lens ids, and the built-in
// fallback. A bundle with no recipe control refuses nothing — an absent control
// is absent, not an error.
func Load(bundleDir, name string) (*Plan, *core.RefusalError) {
	snap, found, ref := controls.GateRecordedPath(bundleDir, controls.RecipesPath)
	if ref != nil {
		return nil, ref
	}
	var repo *Document
	if found {
		raw, ok := snap.ReadFile(controls.RecipesPath)
		if !ok {
			return nil, &core.RefusalError{
				Code:    controls.CodeUnavailable,
				Message: "the bundle's " + controls.RecipesPath + " snapshot is unreadable — re-fetch the bundle",
			}
		}
		if repo, ref = Parse(raw); ref != nil {
			return nil, ref
		}
	}
	return Resolve(repo, name, loadedLenses(bundleDir))
}

// loadedLenses reads the bundle's lens ids from lenses.txt in file order.
func loadedLenses(bundleDir string) []string {
	raw, err := jq.CappedFile(filepath.Join(bundleDir, lensFile), maxLensesBytes)
	if err != nil {
		return nil
	}
	var ids []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if id, ok := lensLabel(line); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// lensLabel reads one lens body header (`# lens <id> — sha256:<hex>`). The pack
// header (`# lens pack …`) carries the pack identity, not a lens.
func lensLabel(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "# lens ")
	if !ok || strings.HasPrefix(rest, "pack ") {
		return "", false
	}
	id, _, _ := strings.Cut(rest, " ")
	if !lensIDRE.MatchString(id) {
		return "", false
	}
	return id, true
}
