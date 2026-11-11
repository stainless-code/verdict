package indexcore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// Store layout names.
const (
	storeDirName  = "index"
	versionName   = "version"
	treesDirName  = "trees"
	shardsDirName = "shards"
	storeVersion  = "1\n"
	storeDirPerm  = 0o755
	jsonSuffix    = ".json"
	colonSpace    = ": "
)

// treeIndex is the queryable artifact for one tree: every indexed file's
// parsed declarations and references, sorted by path. It carries no commit
// and no timestamp, so the same content always writes the same bytes.
type treeIndex struct {
	SchemaVersion string     `json:"schema_version"`
	Tree          string     `json:"tree"`
	Module        string     `json:"module,omitempty"`
	Files         []treeFile `json:"files"`
}

// treeFile is one file's contribution to a tree index.
type treeFile struct {
	Path          string        `json:"path"`
	Blob          string        `json:"blob"`
	Package       string        `json:"package,omitempty"`
	Error         string        `json:"error,omitempty"`
	Imports       []importEntry `json:"imports,omitempty"`
	Decls         []decl        `json:"decls,omitempty"`
	Refs          []ref         `json:"refs,omitempty"`
	Size          int64         `json:"size"`
	Oversize      bool          `json:"oversize,omitempty"`
	RefsTruncated bool          `json:"refs_truncated,omitempty"`
}

// storePath returns the index store under one repository root.
func storePath(root string) string { return filepath.Join(root, config.VerdictDir, storeDirName) }

// ensureStore creates the index store, adds the tracked ignore line, and
// resets a store written by another version. A version mismatch is a full
// rebuild, never a partial read.
//
//nolint:revive // the store failure matrix reads as one block
func ensureStore(root string) (string, *core.RefusalError) {
	if err := config.NewLayout(root).EnsureStateIgnore(); err != nil {
		if ref := err.Refusal(); ref != nil {
			return "", ref
		}
		return "", &core.RefusalError{Code: codeIndexFailed, Message: err.Error()}
	}
	store := storePath(root)
	for _, dir := range []string{store, filepath.Join(store, treesDirName), filepath.Join(store, shardsDirName)} {
		if err := os.MkdirAll(dir, storeDirPerm); err != nil {
			return "", &core.RefusalError{Code: codeIndexFailed, Message: "could not create " + dir + colonSpace + err.Error()}
		}
	}
	versionPath := filepath.Join(store, versionName)
	raw, err := os.ReadFile(versionPath)
	switch {
	case err == nil && string(raw) == storeVersion:
		return store, nil
	case err == nil:
		if ref := resetStore(store); ref != nil {
			return "", ref
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", &core.RefusalError{Code: codeIndexFailed, Message: "could not read " + versionPath + colonSpace + err.Error()}
	}
	if err := atomic.WriteFile(versionPath, []byte(storeVersion)); err != nil {
		return "", &core.RefusalError{Code: codeIndexFailed, Message: "could not write " + versionPath + colonSpace + err.Error()}
	}
	return store, nil
}

// resetStore removes every tree and shard from an older version.
func resetStore(store string) *core.RefusalError {
	for _, name := range []string{treesDirName, shardsDirName} {
		dir := filepath.Join(store, name)
		if err := os.RemoveAll(dir); err != nil {
			return &core.RefusalError{Code: codeIndexFailed, Message: "could not reset " + dir + colonSpace + err.Error()}
		}
		if err := os.MkdirAll(dir, storeDirPerm); err != nil {
			return &core.RefusalError{Code: codeIndexFailed, Message: "could not create " + dir + colonSpace + err.Error()}
		}
	}
	return nil
}

// treePath returns one tree file's path.
func treePath(store, tree string) string { return filepath.Join(store, treesDirName, tree+jsonSuffix) }

// shardPath returns one shard file's path.
func shardPath(store, blob string) string {
	return filepath.Join(store, shardsDirName, blob+jsonSuffix)
}

// shardStats reports the shard directory's file count and byte total.
func shardStats(store string) (int, int64) { return dirStats(filepath.Join(store, shardsDirName)) }

// dirStats reports one directory's file count and byte total.
func dirStats(dir string) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	count := 0
	var bytes int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), jsonSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		count++
		bytes += info.Size()
	}
	return count, bytes
}

// readShard reads and validates one content-addressed shard. A truncated or
// invalid shard refuses: it is never read as empty.
func readShard(store, blob string) (*shard, bool, *core.RefusalError) {
	raw, err := os.ReadFile(shardPath(store, blob))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, &core.RefusalError{
			Code: codeIndexFailed, Message: "could not read shard " + blob + jsonSuffix + colonSpace + err.Error(),
		}
	}
	var s shard
	if err := json.Unmarshal(raw, &s); err != nil || s.SchemaVersion != "1" || s.Blob != blob {
		return nil, true, &core.RefusalError{
			Code: codeIndexCorrupt, Message: fmt.Sprintf("shard %s.json is truncated or invalid", blob),
		}
	}
	return &s, true, nil
}

// readTree reads and validates one tree file.
func readTree(store, tree string) (*treeIndex, *core.RefusalError) {
	raw, err := os.ReadFile(treePath(store, tree))
	if errors.Is(err, os.ErrNotExist) {
		return nil, &core.RefusalError{
			Code: codeMissingIndex, Message: "no index for tree " + tree + "; run verdict index build",
		}
	}
	if err != nil {
		return nil, &core.RefusalError{
			Code: codeIndexFailed, Message: "could not read tree " + tree + jsonSuffix + colonSpace + err.Error(),
		}
	}
	var t treeIndex
	if err := json.Unmarshal(raw, &t); err != nil || t.SchemaVersion != "1" || t.Tree != tree {
		return nil, &core.RefusalError{
			Code: codeIndexCorrupt, Message: fmt.Sprintf("tree %s.json is truncated or invalid", tree),
		}
	}
	return &t, nil
}

// isGoEntry reports a regular Go source blob.
func isGoEntry(e treeEntry) bool {
	return strings.HasSuffix(e.Path, ".go") && (e.Mode == "100644" || e.Mode == "100755")
}

// goEntries keeps the regular Go files, sorted by path.
func goEntries(entries []treeEntry) []treeEntry {
	out := make([]treeEntry, 0, len(entries))
	for _, e := range entries {
		if isGoEntry(e) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b treeEntry) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// build executes `index build`.
//
//nolint:revive,cyclop // the store failure matrix reads as one pass
func (st *state) build(ctx context.Context) (*Response, *core.RefusalError) {
	limits := st.limits()
	dir := st.sourceRoot()
	st.trust = st.readTrust(ctx, dir)
	at := st.req.At
	if at == "" {
		at = "HEAD"
	}
	tree, ref := st.resolveTree(ctx, dir, at)
	if ref != nil {
		return nil, ref
	}
	commit, ref := st.resolveCommit(ctx, dir, at)
	if ref != nil {
		return nil, ref
	}
	entries, ref := st.lsTree(ctx, dir, tree)
	if ref != nil {
		return nil, ref
	}
	files := goEntries(entries)
	if len(files) > limits.Files {
		return nil, &core.RefusalError{
			Code:    codeIndexCapped,
			Message: fmt.Sprintf("%d package files exceeds the %d-file cap", len(files), limits.Files),
		}
	}
	module := st.readModule(ctx, dir, entries)
	store, ref := ensureStore(dir)
	if ref != nil {
		return nil, ref
	}
	parsed, fresh, ref := st.collectShards(ctx, dir, store, files, limits)
	if ref != nil {
		return nil, ref
	}
	t := assembleTree(tree, module, files, limits, parsed)
	if _, _, ref := checkShardCaps(store, freshShards(parsed, fresh), limits); ref != nil {
		return nil, ref
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "could not encode the tree index: " + err.Error()}
	}
	if int64(len(raw)) > limits.TreeBytes {
		return nil, &core.RefusalError{
			Code:    codeIndexCapped,
			Message: fmt.Sprintf("tree index is %d bytes, over the %d-byte cap", len(raw), limits.TreeBytes),
		}
	}
	if ref := writeShards(store, freshShards(parsed, fresh)); ref != nil {
		return nil, ref
	}
	if err := atomic.WriteFile(treePath(store, tree), raw); err != nil {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "could not write the tree index: " + err.Error()}
	}
	return st.buildResponse(t, commit, fresh), nil
}

// collectShards reads every reusable shard and parses only missing blobs,
// returning each blob's parse result and the set freshly parsed.
//
//nolint:revive // the store failure matrix reads as one block
func (st *state) collectShards(
	ctx context.Context, dir, store string, files []treeEntry, limits Limits,
) (map[string]*shard, map[string]bool, *core.RefusalError) {
	parsed := make(map[string]*shard)
	fresh := make(map[string]bool)
	var missing []string
	for _, e := range files {
		if e.Size > limits.FileBytes {
			continue
		}
		if _, done := parsed[e.Blob]; done {
			continue
		}
		s, found, ref := readShard(store, e.Blob)
		if ref != nil {
			return nil, nil, ref
		}
		if found {
			parsed[e.Blob] = s
			continue
		}
		missing = append(missing, e.Blob)
	}
	objects, ref := st.gitBatch(ctx, dir, missing)
	if ref != nil {
		return nil, nil, ref
	}
	for _, blob := range missing {
		src, ok := objects[blob]
		if !ok {
			return nil, nil, &core.RefusalError{Code: codeIndexFailed, Message: "git cat-file did not return blob " + blob}
		}
		s := parseShard(blob, src, limits.RefsPerFile)
		parsed[blob] = &s
		fresh[blob] = true
	}
	return parsed, fresh, nil
}

// assembleTree builds the tree file from the parsed shards.
func assembleTree(tree, module string, files []treeEntry, limits Limits, parsed map[string]*shard) *treeIndex {
	t := &treeIndex{SchemaVersion: "1", Tree: tree, Module: module, Files: make([]treeFile, 0, len(files))}
	for _, e := range files {
		file := treeFile{Path: filepath.ToSlash(e.Path), Blob: e.Blob, Size: e.Size}
		if e.Size > limits.FileBytes {
			file.Oversize = true
			t.Files = append(t.Files, file)
			continue
		}
		s := parsed[e.Blob]
		file.Package = s.Package
		file.Imports = s.Imports
		file.Decls = s.Decls
		file.Refs = s.Refs
		file.Error = s.Error
		file.RefsTruncated = s.RefsTruncated
		t.Files = append(t.Files, file)
	}
	return t
}

// freshShards selects the shards that still need writing.
func freshShards(parsed map[string]*shard, fresh map[string]bool) map[string]*shard {
	out := make(map[string]*shard, len(fresh))
	for blob := range fresh {
		out[blob] = parsed[blob]
	}
	return out
}

// checkShardCaps measures the shard directory plus the pending writes before
// anything lands.
func checkShardCaps(store string, fresh map[string]*shard, limits Limits) (int, int64, *core.RefusalError) {
	count, bytes := shardStats(store)
	for blob, s := range fresh {
		raw, err := json.Marshal(s)
		if err != nil {
			return 0, 0, &core.RefusalError{Code: codeIndexFailed, Message: "could not encode shard " + blob + colonSpace + err.Error()}
		}
		count++
		bytes += int64(len(raw))
	}
	if bytes > limits.ShardsBytes {
		return count, bytes, &core.RefusalError{
			Code:    codeIndexCapped,
			Message: fmt.Sprintf("shard cache is %d bytes across %d shards, over the %d-byte cap", bytes, count, limits.ShardsBytes),
		}
	}
	return count, bytes, nil
}

// writeShards writes every freshly parsed shard atomically.
func writeShards(store string, fresh map[string]*shard) *core.RefusalError {
	for blob, s := range fresh {
		raw, err := json.Marshal(s)
		if err != nil {
			return &core.RefusalError{Code: codeIndexFailed, Message: "could not encode shard " + blob + colonSpace + err.Error()}
		}
		if err := atomic.WriteFile(shardPath(store, blob), raw); err != nil {
			return &core.RefusalError{Code: codeIndexFailed, Message: "could not write shard " + blob + colonSpace + err.Error()}
		}
	}
	return nil
}

// buildCounts derives the coverage numbers from the assembled tree.
func buildCounts(t *treeIndex, fresh map[string]bool) (indexed, skipped, reused int) {
	for i := range t.Files {
		f := &t.Files[i]
		switch {
		case f.Oversize, f.Error != "", f.RefsTruncated:
			skipped++
		default:
			indexed++
			if !fresh[f.Blob] {
				reused++
			}
		}
	}
	return indexed, skipped, reused
}

// buildResponse renders the build outcome.
func (st *state) buildResponse(t *treeIndex, commit string, fresh map[string]bool) *Response {
	indexed, skipped, reused := buildCounts(t, fresh)
	count, bytes := shardStats(storePath(st.sourceRoot()))
	treeBytes := int64(0)
	if raw, err := json.Marshal(t); err == nil {
		treeBytes = int64(len(raw))
	}
	st.line(fmt.Sprintf("index: %s at %s (%d files indexed, %d skipped, %d parsed, %d reused)",
		t.Tree, short(commit), indexed, skipped, len(fresh), reused))
	return &Response{
		At:   st.req.TimeUTC(),
		Next: "query it with verdict index callers <symbol>",
		Data: &Data{
			Kind: QueryBuild, Tree: t.Tree, Commit: commit, Built: true,
			Files: indexed, Skipped: skipped, Parsed: len(fresh), Reused: reused,
			TreeBytes: treeBytes, ShardCount: count, ShardBytes: bytes,
		},
	}
}

// status executes `index status`.
//
//nolint:revive // the store failure matrix reads as one block
func (st *state) status(ctx context.Context) (*Response, *core.RefusalError) {
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	commit, ref := st.resolveCommit(ctx, root, "HEAD")
	if ref != nil {
		return nil, ref
	}
	head, ref := st.resolveTree(ctx, root, "HEAD")
	if ref != nil {
		return nil, ref
	}
	store := storePath(root)
	rows, ref := st.treeRows(store)
	if ref != nil {
		return nil, ref
	}
	built := false
	for _, row := range rows {
		if row.Tree == head {
			built = true
		}
	}
	if !storeVersionOK(store) {
		rows = nil
		built = false
	}
	count, bytes := shardStats(store)
	st.line(fmt.Sprintf("index: %d tree(s), %d shard(s) (%d bytes)", len(rows), count, bytes))
	if built {
		st.line("index: HEAD " + head + " is indexed")
	} else {
		st.line("index: no tree for HEAD " + head + " — run verdict index build")
	}
	return &Response{
		At:   st.req.TimeUTC(),
		Next: "build it with verdict index build",
		Data: &Data{
			Kind: QueryStatus, Tree: head, Commit: commit, Stale: !built, Built: built,
			Trees: rows, ShardCount: count, ShardBytes: bytes,
		},
	}, nil
}

// storeVersionOK reports whether the store carries this build's version.
func storeVersionOK(store string) bool {
	raw, err := os.ReadFile(filepath.Join(store, versionName))
	return err == nil && string(raw) == storeVersion
}

// treeRows reads every stored tree, refusing a corrupt one.
//
//nolint:revive // the store failure matrix reads as one block
func (st *state) treeRows(store string) ([]TreeRow, *core.RefusalError) {
	entries, err := os.ReadDir(filepath.Join(store, treesDirName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "could not list the tree store: " + err.Error()}
	}
	rows := make([]TreeRow, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), jsonSuffix) {
			continue
		}
		tree := strings.TrimSuffix(e.Name(), jsonSuffix)
		t, ref := readTree(store, tree)
		if ref != nil {
			return nil, ref
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rows = append(rows, TreeRow{Tree: t.Tree, Files: len(t.Files), Bytes: info.Size()})
	}
	slices.SortFunc(rows, func(a, b TreeRow) int { return strings.Compare(a.Tree, b.Tree) })
	return rows, nil
}

// prune executes `index prune`.
//
//nolint:revive // the store failure matrix reads as one block
func (st *state) prune(ctx context.Context) (*Response, *core.RefusalError) {
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	store := storePath(root)
	limits := st.limits()
	old, err := os.ReadDir(filepath.Join(store, treesDirName))
	if errors.Is(err, os.ErrNotExist) {
		return st.pruneResponse(nil, 0, 0), nil
	}
	if err != nil {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "could not list the tree store: " + err.Error()}
	}
	trees := treeCandidates(old)
	keep := trees
	var drop []treeCandidate
	if len(trees) > limits.KeepTrees {
		keep = trees[:limits.KeepTrees]
		drop = trees[limits.KeepTrees:]
	}
	referenced := map[string]bool{}
	kept := make([]string, 0, len(keep))
	for _, candidate := range keep {
		t, ref := readTree(store, candidate.name)
		if ref != nil {
			return nil, ref
		}
		kept = append(kept, candidate.name)
		for i := range t.Files {
			referenced[t.Files[i].Blob] = true
		}
	}
	for _, candidate := range drop {
		if err := os.Remove(treePath(store, candidate.name)); err != nil {
			return nil, &core.RefusalError{
				Code: codeIndexFailed, Message: "could not remove tree " + candidate.name + colonSpace + err.Error(),
			}
		}
	}
	gone, ref := removeUnreferencedShards(store, referenced)
	if ref != nil {
		return nil, ref
	}
	slices.Sort(kept)
	return st.pruneResponse(kept, len(drop), gone), nil
}

// treeCandidate is one stored tree with its modification time, which orders
// prunes: the store carries no timestamps in its bytes.
type treeCandidate struct {
	name string
	mod  int64
}

// treeCandidates lists tree files newest first, name-breaking ties.
//
//nolint:revive // the store failure matrix reads as one block
func treeCandidates(entries []os.DirEntry) []treeCandidate {
	candidates := make([]treeCandidate, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), jsonSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, treeCandidate{
			name: strings.TrimSuffix(e.Name(), jsonSuffix), mod: info.ModTime().UnixNano(),
		})
	}
	slices.SortFunc(candidates, func(a, b treeCandidate) int {
		if a.mod != b.mod {
			return cmp.Compare(b.mod, a.mod)
		}
		return strings.Compare(a.name, b.name)
	})
	return candidates
}

// removeUnreferencedShards deletes every shard no kept tree names.
//
//nolint:revive // the store failure matrix reads as one block
func removeUnreferencedShards(store string, referenced map[string]bool) (int, *core.RefusalError) {
	entries, err := os.ReadDir(filepath.Join(store, shardsDirName))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, &core.RefusalError{Code: codeIndexFailed, Message: "could not list the shard store: " + err.Error()}
	}
	gone := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), jsonSuffix) {
			continue
		}
		blob := strings.TrimSuffix(e.Name(), jsonSuffix)
		if referenced[blob] {
			continue
		}
		if err := os.Remove(shardPath(store, blob)); err != nil {
			return gone, &core.RefusalError{Code: codeIndexFailed, Message: "could not remove shard " + blob + colonSpace + err.Error()}
		}
		gone++
	}
	return gone, nil
}

// pruneResponse renders the prune outcome.
func (st *state) pruneResponse(kept []string, removedTrees, removedShards int) *Response {
	count, bytes := shardStats(storePath(st.sourceRoot()))
	st.line(fmt.Sprintf("index: kept %d tree(s), removed %d tree(s) and %d shard(s)",
		len(kept), removedTrees, removedShards))
	return &Response{
		At:   st.req.TimeUTC(),
		Next: "run verdict index build to index the current HEAD",
		Data: &Data{
			Kind: QueryPrune, Kept: kept, RemovedTree: removedTrees, GoneShards: removedShards,
			ShardCount: count, ShardBytes: bytes,
		},
	}
}
