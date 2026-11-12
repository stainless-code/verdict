package pack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// RepoPacksDir is the repo layer's tracked directory: one pack root per
// child directory, read from the trusted-base snapshot by fetch.
const RepoPacksDir = ".verdict/packs"

// listSep joins the row's id lists.
const listSep = ","

// maxRepoPackEntries bounds one pack-root scan at the same 1024 entries the
// controls pack budget allows; readDirBatch is how many entries one directory
// read returns, and canceledCode is what a canceled read reports.
const (
	maxRepoPackEntries = 1024
	readDirBatch       = 256
	canceledCode       = "canceled"
)

// Request wires one pack list read. List is the only served mode.
type Request struct {
	core.ReadRequest
	SettingsPath string `json:"-"`
	List         bool
}

// Entry is one pack list row: identity, layer, source, tree hash, the ids it
// declares, and a drift verdict (null until verdict.lock lands).
//
//nolint:govet // the published row order puts identity and ids before drift
type Entry struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Layer   string   `enum:"builtin|org|repo" json:"layer"`
	Source  string   `json:"source"`
	Hash    string   `json:"hash"`
	Lenses  []string `json:"lenses"`
	Rules   []string `json:"rules"`
	Drift   *string  `json:"drift"`
}

// ListData is the pack verb's --json data profile.
type ListData struct {
	Packs []Entry `json:"packs"`
}

// Wire renders the data profile in published wire order.
func (d *ListData) Wire() *jq.Obj {
	if d == nil {
		return jq.NewObj("packs", []any{})
	}
	return jq.NewObj("packs", core.Wire(d.Packs))
}

// Response carries the envelope inputs and the human rows.
//
//nolint:govet // the envelope fields read in render order
type Response struct {
	At       string
	Next     string
	Warnings []string
	Human    []string
	Data     *ListData
}

// RunList resolves the built-in, org, and repo layers, merges them, and
// reports every pack. It writes nothing.
func RunList(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: canceledCode, Message: "canceled before pack list completed"}
	}
	if !req.List {
		return nil, &core.RefusalError{Code: "bad-flags", Message: "pack serves list; pass list"}
	}
	origins, warnings, ref := loadOrigins(ctx, req)
	if ref != nil {
		return nil, ref
	}
	set, ref := Merge(origins)
	if ref != nil {
		ref.Warnings = warnings
		return nil, ref
	}
	for _, warning := range warnings {
		req.Warn(warning)
	}
	lock, ref := loadLock(req.RepoRootPath())
	if ref != nil {
		return nil, ref
	}
	data := listData(set, lock)
	return &Response{
		At:       req.TimeUTC(),
		Next:     "add a repo pack under .verdict/packs/<name>/ or list an org pack path in settings packs",
		Warnings: warnings,
		Human:    humanRows(data),
		Data:     data,
	}, nil
}

// loadOrigins reads the three layers in precedence order.
func loadOrigins(ctx context.Context, req *Request) ([]Origin, []string, *core.RefusalError) {
	origins := []Origin{}
	if packFS := req.builtinFS(); packFS != nil {
		p, ref := Load(packFS, BuiltinRoot)
		if ref != nil {
			return nil, nil, ref
		}
		if p != nil {
			origins = append(origins, Origin{Layer: LayerBuiltin, Source: "builtin", Pack: p})
		}
	}
	org, ref := orgOrigins(req)
	if ref != nil {
		return nil, nil, ref
	}
	repo, warnings, ref := repoOrigins(ctx, req.RepoRootPath())
	if ref != nil {
		return nil, nil, ref
	}
	origins = append(origins, org...)
	return append(origins, repo...), warnings, nil
}

// builtinFS returns the injected skill FS, or nil for a caller that supplied
// no runtime (the built-in layer is then simply absent).
func (r *Request) builtinFS() fs.FS {
	if r == nil || r.Runtime == nil {
		return nil
	}
	return r.PackFS
}

// orgOrigins reads the explicit settings path list. A listed path that
// carries no pack refuses: the operator named it, so its absence is a
// configuration error, not an empty layer.
func orgOrigins(req *Request) ([]Origin, *core.RefusalError) {
	if req.SettingsPath == "" {
		return nil, nil
	}
	settings, ref := config.LoadPersonal(req.SettingsPath)
	if ref != nil {
		return nil, ref
	}
	root := req.RepoRootPath()
	out := make([]Origin, 0, len(settings.Packs))
	for _, listed := range settings.Packs {
		origin, ref := orgOrigin(root, listed)
		if ref != nil {
			return nil, ref
		}
		out = append(out, origin)
	}
	return out, nil
}

// orgOrigin loads one explicitly listed org pack directory.
func orgOrigin(root, listed string) (Origin, *core.RefusalError) {
	if ref := CheckOrgEntry(root, listed); ref != nil {
		return Origin{}, ref
	}
	full := listed
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, filepath.FromSlash(listed))
	}
	p, ref := LoadDir(full)
	if ref != nil {
		return Origin{}, ref
	}
	if p == nil {
		return Origin{}, badPack(fmt.Sprintf("org pack path %q carries no %s", listed, ManifestFile))
	}
	return Origin{Layer: LayerOrg, Source: "dir:" + filepath.ToSlash(listed), Pack: p}, nil
}

// repoOrigins reads one pack per child directory of .verdict/packs. A file at
// that level is not a pack: the walk names it in one warning and reads on.
func repoOrigins(ctx context.Context, root string) ([]Origin, []string, *core.RefusalError) {
	dir := filepath.Join(root, RepoPacksDir)
	if ref := checkPacksRoot(dir); ref != nil {
		return nil, nil, ref
	}
	entries, ref := readPackRoot(ctx, dir)
	if ref != nil {
		return nil, nil, ref
	}
	if entries == nil {
		return nil, nil, nil
	}
	return repoEntries(ctx, dir, entries)
}

// readPackRoot reads the pack root's children in bounded batches: the
// directory is never listed in one unbounded allocation, and a root holding
// more than maxRepoPackEntries children refuses instead of keeping them all.
func readPackRoot(ctx context.Context, dir string) ([]fs.DirEntry, *core.RefusalError) {
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, badPack(fmt.Sprintf("read %s: %v", RepoPacksDir, err))
	}
	defer func() { _ = f.Close() }()
	entries, ref := readPackEntries(ctx, f)
	if ref != nil {
		return nil, ref
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// readPackEntries reads one directory handle to EOF in batches, checking the
// caller's context before each batch and refusing past the entry cap.
func readPackEntries(ctx context.Context, f *os.File) ([]fs.DirEntry, *core.RefusalError) {
	var entries []fs.DirEntry
	for {
		batch, done, ref := readPackBatch(ctx, f, len(entries))
		if ref != nil {
			return nil, ref
		}
		entries = append(entries, batch...)
		if done {
			return entries, nil
		}
	}
}

// readPackBatch reads one bounded batch and reports whether it ended the
// listing; listed is the count the cap is charged against.
func readPackBatch(ctx context.Context, f *os.File, listed int) ([]fs.DirEntry, bool, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, false, &core.RefusalError{Code: canceledCode, Message: "canceled while scanning " + RepoPacksDir}
	}
	batch, rerr := f.ReadDir(readDirBatch)
	if listed += len(batch); listed > maxRepoPackEntries {
		return nil, false, packRootTooLarge()
	}
	if errors.Is(rerr, io.EOF) {
		return batch, true, nil
	}
	if rerr != nil {
		return nil, false, badPack(fmt.Sprintf("read %s: %v", RepoPacksDir, rerr))
	}
	return batch, false, nil
}

// packRootTooLarge refuses a pack root past the entry cap.
func packRootTooLarge() *core.RefusalError {
	return tooLarge(fmt.Sprintf("%s holds more than %d entries — trim it", RepoPacksDir, maxRepoPackEntries))
}

// checkPacksRoot refuses a pack root that is not a real directory: following a
// symlink would list an out-of-tree pack the controls read refuses. A missing
// root is an absent layer, not a failure.
func checkPacksRoot(dir string) *core.RefusalError {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return badPack(fmt.Sprintf("inspect %s: %v", RepoPacksDir, err))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return unsafePath(dir)
	}
	return nil
}

// repoEntries reads each child of the pack root, so a cancel mid-scan stops
// before the next pack is loaded.
func repoEntries(ctx context.Context, dir string, entries []fs.DirEntry) ([]Origin, []string, *core.RefusalError) {
	out := make([]Origin, 0, len(entries))
	var warnings []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, &core.RefusalError{Code: canceledCode, Message: "canceled while scanning " + RepoPacksDir}
		}
		origin, warning, ref := repoOriginEntry(dir, entry)
		if ref != nil {
			ref.Warnings = warnings
			return nil, nil, ref
		}
		if warning != "" {
			warnings = append(warnings, warning)
			continue
		}
		out = append(out, origin)
	}
	return out, warnings, nil
}

// repoOriginEntry reads one child of the pack root: a regular file is a
// warning naming it, a symlink or special file refuses, and a directory is a
// pack root.
func repoOriginEntry(dir string, entry fs.DirEntry) (Origin, string, *core.RefusalError) {
	if strings.HasPrefix(entry.Name(), stagingDot) {
		return Origin{}, "", nil
	}
	if entry.IsDir() {
		origin, ref := repoOrigin(dir, entry.Name())
		return origin, "", ref
	}
	info, err := entry.Info()
	if err != nil {
		return Origin{}, "", badPack(fmt.Sprintf("inspect %s/%s: %v", RepoPacksDir, entry.Name(), err))
	}
	if !info.Mode().IsRegular() {
		return Origin{}, "", unsafePath(filepath.Join(dir, entry.Name()))
	}
	return Origin{}, fmt.Sprintf("%s/%s is not a pack directory — ignored",
		RepoPacksDir, entry.Name()), nil
}

// repoOrigin loads one .verdict/packs child directory.
func repoOrigin(dir, name string) (Origin, *core.RefusalError) {
	p, ref := LoadDir(filepath.Join(dir, name))
	if ref != nil {
		return Origin{}, ref
	}
	if p == nil {
		return Origin{}, badPack(fmt.Sprintf("%s/%s carries no %s", RepoPacksDir, name, ManifestFile))
	}
	return Origin{Layer: LayerRepo, Source: RepoPacksDir + "/" + name, Pack: p}, nil
}

// listData renders one row per loaded pack in layer order; a repo row carries
// its lock verdict, while built-in and org rows are never locked.
func listData(set *Set, lock *Lock) *ListData {
	data := &ListData{Packs: make([]Entry, 0, len(set.Packs))}
	listed := map[string]bool{}
	for _, origin := range set.Packs {
		if origin.Pack == nil {
			continue
		}
		entry := Entry{
			Name: origin.Pack.Name, Version: origin.Pack.Version,
			Layer: string(origin.Layer), Source: redactURL(origin.Source), Hash: origin.Pack.Hash,
			Lenses: lensIDsOf(origin.Pack), Rules: ruleIDsOf(origin.Pack),
		}
		if origin.Layer == LayerRepo {
			entry.Drift = entryDrift(lock, origin.Pack, origin.Source)
			listed[origin.Pack.Name] = true
		}
		data.Packs = append(data.Packs, entry)
	}
	return appendMissingLocked(data, lock, listed)
}

// appendMissingLocked adds one drift row for a lock entry whose pack directory
// did not load, so a deleted vendored pack stays visible beside the refusal.
func appendMissingLocked(data *ListData, lock *Lock, listed map[string]bool) *ListData {
	if lock == nil {
		return data
	}
	for i := range lock.Packs {
		entry := &lock.Packs[i]
		if listed[entry.Name] {
			continue
		}
		code := CodeLockDrift
		data.Packs = append(data.Packs, Entry{
			Name: entry.Name, Version: entry.Version, Layer: string(LayerRepo),
			Source: redactURL(entry.Source), Hash: entry.Hash,
			Lenses: []string{}, Rules: []string{}, Drift: &code,
		})
	}
	return data
}

// entryDrift returns the refusal code a repo row would hit, or nil when the
// lock entry matches the loaded tree at its canonical slug.
func entryDrift(lock *Lock, p *Pack, source string) *string {
	if source != RepoPacksDir+"/"+packSlug(p.Name) {
		code := CodeLockDrift
		return &code
	}
	entry := lockEntry(lock, p.Name)
	if entry == nil {
		code := CodeLockMissing
		return &code
	}
	if entry.Sig != LockSig(entry) || entry.Hash != p.Hash {
		code := CodeLockDrift
		return &code
	}
	return nil
}

// humanRows renders one stable line per pack.
func humanRows(data *ListData) []string {
	if len(data.Packs) == 0 {
		return []string{"no lens packs"}
	}
	rows := make([]string, 0, len(data.Packs))
	for i := range data.Packs {
		entry := &data.Packs[i]
		lenses := strings.Join(entry.Lenses, listSep)
		rules := strings.Join(entry.Rules, listSep)
		if rules == "" {
			rules = "-"
		}
		row := fmt.Sprintf("%-7s  %s@%s  source=%s  sha256:%s  lenses=%s  rules=%s",
			entry.Layer, entry.Name, entry.Version, entry.Source, shortHash(entry.Hash), lenses, rules)
		if entry.Drift != nil {
			row += "  drift=" + *entry.Drift
		}
		rows = append(rows, row)
	}
	return rows
}

// shortHashLen is the human row's abbreviated tree hash width.
const shortHashLen = 12

// shortHash abbreviates a tree hash for the human row.
func shortHash(hash string) string {
	if len(hash) <= shortHashLen {
		return hash
	}
	return hash[:shortHashLen]
}
