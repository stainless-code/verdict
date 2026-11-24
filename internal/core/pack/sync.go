package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// SyncRequest wires one `pack sync` run: the base-owned org pin the adapter
// read from the trusted base, beside the read runtime.
//
//nolint:govet // the embedded runtime and the pin read as one call
type SyncRequest struct {
	core.ReadRequest
	OrgPin *OrgPin
}

// SyncData is the pack sync --json data profile.
type SyncData struct {
	Pack    Entry   `json:"pack"`
	Repo    string  `json:"repo"`
	Ref     string  `json:"ref"`
	Path    string  `json:"path"`
	SHA     *string `json:"sha"`
	Hash    string  `json:"hash"`
	Version string  `json:"version"`
}

// Wire renders the sync data in published wire order.
func (d *SyncData) Wire() *jq.Obj {
	if d == nil {
		return jq.NewObj("pack", nil, "repo", "", "ref", "", "path", ".", "sha", nil, "hash", "", "version", "")
	}
	return jq.NewObj(
		"pack", core.Wire(&d.Pack),
		"repo", d.Repo,
		"ref", d.Ref,
		"path", d.Path,
		"sha", d.SHA,
		"hash", d.Hash,
		"version", d.Version,
	)
}

// SyncResponse carries the envelope inputs and the human row.
//
//nolint:govet // the envelope fields read in render order
type SyncResponse struct {
	Data  *SyncData
	Human []string
	Next  string
}

// RunSync vendors the base-owned org pin under .verdict/packs/<slug>/ and
// pins it in .verdict/verdict.lock. This is the only path that touches
// the network (a git clone, with the caller's commander); every review path
// below merely re-verifies the vendored tree against the lock.
func RunSync(ctx context.Context, req *SyncRequest) (*SyncResponse, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: canceledCode, Message: "canceled before pack sync completed"}
	}
	pin := req.OrgPin
	if pin == nil {
		return nil, badPack("no org_pack pin in the trusted-base .verdict/config.json — declare one before syncing")
	}
	if ref := validatePin(pin); ref != nil {
		return nil, ref
	}
	root := req.RepoRootPath()
	src, ref := syncSource(ctx, req, pin, root)
	if ref != nil {
		return nil, ref
	}
	defer src.cleanup()
	if ref := confinePinPath(src.dir, pin); ref != nil {
		return nil, ref
	}
	pinPath := orgPinPath(pin)
	packDir := src.dir
	if pinPath != rootSelf {
		packDir = filepath.Join(src.dir, filepath.FromSlash(pinPath))
	}
	// Load through the confined root so an intermediate symlink cannot
	// escape src.dir during the walk.
	p, lock, ref := loadSyncConfined(root, src, pinPath)
	if ref != nil {
		return nil, ref
	}
	return vendorAndLock(ctx, req, root, src, pin, pinPath, packDir, p, lock)
}

func vendorAndLock(
	ctx context.Context, req *SyncRequest, root string, src *packSource, pin *OrgPin, pinPath, packDir string, p *Pack, lock *Lock,
) (*SyncResponse, *core.RefusalError) {
	if ctx.Err() != nil {
		return nil, &core.RefusalError{Code: canceledCode, Message: "canceled before pack sync completed"}
	}
	slug := packSlug(p.Name)
	entry := LockEntry{
		Name: p.Name, Source: src.label, Version: p.Version, Hash: p.Hash, OrgPath: pinPath, OrgRef: pin.Ref,
	}
	if pinPath == rootSelf {
		entry.OrgPath = ""
	}
	entry.Sig = LockSig(&entry)
	next := upsertLock(lock, &entry)
	if _, ref := encodeLock(next); ref != nil {
		return nil, ref
	}
	backup, ref := vendorPack(root, packDir, slug, p)
	if ref != nil {
		return nil, ref
	}
	warning, ref := commitAdd(root, slug, backup, next)
	if ref != nil {
		return nil, ref
	}
	if warning != "" {
		req.Warn(warning)
	}
	return syncResponse(pin, p, slug, src), nil
}

// syncSource resolves the pin's repository: an allowlisted git remote is
// cloned hermetically, a local directory (absolute or relative to the
// repository root) is read in place. An unreachable source refuses typed.
func syncSource(ctx context.Context, req *SyncRequest, pin *OrgPin, root string) (*packSource, *core.RefusalError) {
	if g, ok := parseGitSource(pin.Repo); ok {
		return cloneSource(ctx, &AddRequest{ReadRequest: req.ReadRequest}, g.url, pin.Ref, refByGitRef, orgPinPath(pin))
	}
	if looksLikeGitSource(pin.Repo) || secretShaped(pin.Repo) {
		return nil, badPackSource(fmt.Sprintf("org pack repo %q is not an allowlisted git remote", displaySource(pin.Repo)))
	}
	if strings.HasPrefix(pin.Repo, "-") {
		return nil, badPack(fmt.Sprintf("org pack repo %q starts with '-'", pin.Repo))
	}
	dir := pin.Repo
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, filepath.FromSlash(dir))
	}
	label := sourceDirPrefix + filepath.ToSlash(pin.Repo)
	if !validLockSource(label) {
		return nil, badPack(fmt.Sprintf("org pack repo %q cannot be recorded in %s", pin.Repo, LockFile))
	}
	return &packSource{dir: dir, label: label, cleanup: func() {}}, nil
}

// loadSyncConfined loads the central pack through a confined root at
// src.dir, so an intermediate symlink in the pin path cannot escape.
func loadSyncConfined(root string, src *packSource, pinPath string) (*Pack, *Lock, *core.RefusalError) {
	// Same-tree check from pack add: refuse when the source is already
	// inside the vendored tree.
	joined := filepath.Join(root, filepath.FromSlash(RepoPacksDir))
	if sameTree(joined, src.dir) {
		return nil, nil, badPack(fmt.Sprintf(
			"pack source %q is inside %s — it is already vendored", displaySource(src.label), RepoPacksDir))
	}
	p, ref := LoadAt(src.dir, pinPath)
	if ref != nil {
		return nil, nil, ref
	}
	if p == nil {
		return nil, nil, badPack(fmt.Sprintf("org pack source %q carries no %s at path %q",
			pinLabel(src), ManifestFile, pinPath))
	}
	lock, ref := loadLock(root)
	if ref != nil {
		return nil, nil, ref
	}
	if ref := checkSlugOwner(root, lock, p.Name, packSlug(p.Name)); ref != nil {
		return nil, nil, ref
	}
	return p, lock, nil
}

const symlinkMode = fs.ModeSymlink

func splitSlash(p string) []string {
	return strings.Split(p, "/")
}

// confinePinPath refuses an intermediate symlink inside src.dir along the
// pin path and a symlinked source directory itself.
//
//nolint:revive // intermediate symlink walk necessarily iterates pin segments
func confinePinPath(srcDir string, pin *OrgPin) *core.RefusalError {
	if info, err := os.Lstat(srcDir); err == nil {
		if info.Mode()&symlinkMode != 0 {
			return unsafePath(srcDir)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return badPack(fmt.Sprintf("inspect %q: %v", srcDir, err))
	}
	pinPath := orgPinPath(pin)
	if pinPath == rootSelf {
		return nil
	}
	if !confinedRoot(pinPath) {
		return badPack(fmt.Sprintf("org pack path %q escapes the central repository", pin.Path))
	}
	cur := srcDir
	for _, seg := range splitSlash(pinPath) {
		cur = filepath.Join(cur, filepath.FromSlash(seg))
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Missing intermediate is not a symlink escape; LoadAt will
				// report the missing pack later.
				break
			}
			return badPack(fmt.Sprintf("inspect %q: %v", cur, err))
		}
		if info.Mode()&symlinkMode != 0 {
			return unsafePath(cur)
		}
	}
	return nil
}

// pinLabel renders the sync source label for refusal messages.
func pinLabel(src *packSource) string {
	return displaySource(src.label)
}

// resolvedSHA extracts the resolved commit from a git lock label.
func resolvedSHA(label string) *string {
	rest, ok := strings.CutPrefix(label, sourceGitPrefix)
	if !ok {
		return nil
	}
	at := strings.LastIndex(rest, refSep)
	if at < 0 || !hex40(rest[at+1:]) {
		return nil
	}
	sha := rest[at+1:]
	return &sha
}

// syncResponse renders the synced row, its human line, and the next step.
func syncResponse(pin *OrgPin, p *Pack, slug string, src *packSource) *SyncResponse {
	sha := resolvedSHA(src.label)
	data := &SyncData{
		Pack: Entry{
			Name: p.Name, Version: p.Version, Layer: string(LayerRepo),
			Source: path.Join(RepoPacksDir, slug), Hash: p.Hash,
			Lenses: lensIDsOf(p), Rules: ruleIDsOf(p),
		},
		Repo: pin.Repo, Ref: pin.Ref, Path: orgPinPath(pin),
		SHA: sha, Hash: p.Hash, Version: p.Version,
	}
	line := fmt.Sprintf("synced %s@%s repo=%s ref=%s path=%s sha256:%s",
		p.Name, p.Version, pin.Repo, pin.Ref, data.Path, p.Hash)
	return &SyncResponse{
		Next:  "commit .verdict/verdict.lock with the vendored pack, then run 'verdict pack list'",
		Human: []string{line},
		Data:  data,
	}
}
