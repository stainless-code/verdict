package query

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
// or bundle outside the served set.
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
	return root.Path, dir, nil
}

// Invalidate drops every cached composed view: a write just moved the
// stores the views were composed from.
func (l *Layer) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = map[cacheKey]entry{}
}
