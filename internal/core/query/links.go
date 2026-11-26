package query

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

// noFollowReads are bundle entries the layer reads itself without following
// a link (a linked one reads as absent), so a link there reaches nothing.
var noFollowReads = map[string]bool{threadscore.KeyThreadsJSON: true}

// bundleLinkRefusal is the one gate between a resolved bundle and every
// reader the layer composes over it (the report service's findings, review,
// source, diff, cost, budget, run, plan and receipt reads, the inventory, the
// cache stamp). Those readers follow links, so a bundle holding any entry that
// is a link resolving outside the served root, at any depth, refuses before
// one of them runs. Threat model: committed links are refused; one swapped in
// after this walk needs a live local writer who can already read the target.
func bundleLinkRefusal(root Root, bundle, dir string) *core.RefusalError {
	walk := linkWalk{root: root.Path, seen: map[string]bool{}}
	walk.dir(filepath.FromSlash(dir), "")
	if walk.escape == "" {
		return nil
	}
	return &core.RefusalError{
		Code: config.CodeBadConfig,
		Message: core.Msgf("serve: bundle %q holds %s, a link that resolves outside this served root "+
			"— refusing to read through it", bundle, walk.escape),
	}
}

// linkWalk visits a bundle tree the way its readers do: into directories and
// into in-root directory links, once per physical directory.
type linkWalk struct {
	seen   map[string]bool
	root   string
	escape string
}

func (w *linkWalk) dir(dir, rel string) {
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil || w.seen[physical] {
		return
	}
	w.seen[physical] = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if w.escape != "" {
			return
		}
		w.entry(filepath.Join(dir, entry.Name()), path.Join(rel, entry.Name()), entry)
	}
}

func (w *linkWalk) entry(full, rel string, entry fs.DirEntry) {
	switch {
	case entry.Type()&fs.ModeSymlink != 0:
		w.link(full, rel)
	case entry.IsDir():
		w.dir(full, rel)
	}
}

// link records a link out of the root as the escape, and follows an in-root
// directory link the way the readers do.
func (w *linkWalk) link(full, rel string) {
	if noFollowReads[rel] {
		return
	}
	if !insideRoot(w.root, full) {
		w.escape = rel
		return
	}
	if info, err := os.Stat(full); err == nil && info.IsDir() {
		w.dir(full, rel)
	}
}
