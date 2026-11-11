package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before pack list completed"}
	}
	if !req.List {
		return nil, &core.RefusalError{Code: "bad-flags", Message: "pack serves list; pass list"}
	}
	origins, warnings, ref := loadOrigins(req)
	if ref != nil {
		return nil, ref
	}
	set, ref := Merge(origins)
	if ref != nil {
		return nil, ref
	}
	for _, warning := range warnings {
		req.Warn(warning)
	}
	data := listData(set)
	return &Response{
		At:       req.TimeUTC(),
		Next:     "add a repo pack under .verdict/packs/<name>/ or list an org pack path in settings packs",
		Warnings: warnings,
		Human:    humanRows(data),
		Data:     data,
	}, nil
}

// loadOrigins reads the three layers in precedence order.
func loadOrigins(req *Request) ([]Origin, []string, *core.RefusalError) {
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
	repo, warnings, ref := repoOrigins(req.RepoRootPath())
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
func repoOrigins(root string) ([]Origin, []string, *core.RefusalError) {
	dir := filepath.Join(root, RepoPacksDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, badPack(fmt.Sprintf("read %s: %v", RepoPacksDir, err))
	}
	out := make([]Origin, 0, len(entries))
	var warnings []string
	for _, entry := range entries {
		if !entry.IsDir() {
			warnings = append(warnings, fmt.Sprintf("%s/%s is not a pack directory — ignored",
				RepoPacksDir, entry.Name()))
			continue
		}
		origin, ref := repoOrigin(dir, entry.Name())
		if ref != nil {
			return nil, nil, ref
		}
		out = append(out, origin)
	}
	return out, warnings, nil
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

// listData renders one row per loaded pack in layer order.
func listData(set *Set) *ListData {
	data := &ListData{Packs: make([]Entry, 0, len(set.Packs))}
	for _, origin := range set.Packs {
		if origin.Pack == nil {
			continue
		}
		entry := Entry{
			Name: origin.Pack.Name, Version: origin.Pack.Version,
			Layer: string(origin.Layer), Source: origin.Source, Hash: origin.Pack.Hash,
			Lenses: make([]string, 0, len(origin.Pack.Lenses)),
			Rules:  make([]string, 0, len(origin.Pack.Rules)),
		}
		for i := range origin.Pack.Lenses {
			entry.Lenses = append(entry.Lenses, origin.Pack.Lenses[i].ID)
		}
		for i := range origin.Pack.Rules {
			entry.Rules = append(entry.Rules, origin.Pack.Rules[i].ID)
		}
		data.Packs = append(data.Packs, entry)
	}
	return data
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
		rows = append(rows, fmt.Sprintf("%-7s  %s@%s  source=%s  sha256:%s  lenses=%s  rules=%s",
			entry.Layer, entry.Name, entry.Version, entry.Source, shortHash(entry.Hash), lenses, rules))
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
