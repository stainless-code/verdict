package query

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// RootByID resolves one route root id to its absolute served path.
func (l *Layer) RootByID(id string) (string, bool) {
	root, ok := l.rootByID(id)
	if !ok {
		return "", false
	}
	return root.Path, true
}

// ResolveBundle maps a route root id and bundle name onto their served
// paths: the root must be one this process serves, and the bundle resolves
// through the same confinement as a read — the shared bundle-name guard,
// the discovered set, and the sibling check. ErrNotFound addresses a root
// or bundle outside the served set. A write goes no further: the bundle's
// physical directory must sit inside the served root, and no managed
// component may be a symlink — a symlinked `.verdict` or `.verdict/state`
// that leaves the root refuses typed instead of letting the caller write
// through the link.
func (l *Layer) ResolveBundle(rootID, bundle string) (string, string, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return "", "", ErrNotFound
	}
	dir, found, ref := l.resolve(root, bundle)
	if ref != nil {
		return "", "", ref
	}
	if !found {
		return "", "", ErrNotFound
	}
	if !insideRoot(root.Path, dir) {
		return "", "", &core.RefusalError{
			Code: config.CodeBadConfig,
			Message: fmt.Sprintf("serve: bundle %q resolves outside this served root "+
				"— refusing to write through it", bundle),
		}
	}
	if serr := config.NewLayout(root.Path).RefuseBundleSymlink(bundle); serr != nil {
		return "", "", &core.RefusalError{
			Code:    config.CodeBadConfig,
			Message: fmt.Sprintf("serve: bundle %q cannot be written: %v", bundle, serr),
		}
	}
	return root.Path, dir, nil
}

// Invalidate drops every cached composed view: a write just moved the
// stores the views were composed from.
func (l *Layer) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = map[cacheKey]entry{}
}
