package indexcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// Impact hand-off messages. Every one is a rule fetch's manual entry repeats,
// so the refusal and warning text stays one literal.
// impactHeadRef and impactCanceled name the revision a directory hand-off
// reads and the refusal code a canceled context emits; the build and blast
// paths carry the same literals, and this file's uses go through the constants
// so the package keeps one occurrence of each.
const (
	impactHeadRef         = "HEAD"
	impactCanceled        = "canceled"
	impactCanceledMessage = "canceled before index completed"
)

const (
	impactNoIndex    = "impact wants an index: --index <ref|dir>"
	impactNotAStore  = "impact: %s carries no index store (no %s directory)"
	impactStoreTrees = "trees"
	impactHeadStale  = "» impact: the index answers for tree %s, the bundle head is %s; impact rows describe the named index"
	impactUnresolved = "» impact: the bundle head %s is not in this clone; impact rows describe %s, not the reviewed head"
)

// ImpactRequest is one impact hand-off: the diff a bundle carries, the index
// the operator named, and the per-list row cap.
//
// The index is handed over explicitly and never discovered: RefOrDir is a
// revision or a directory the operator already built an index in, and nothing
// is crawled, built, or refreshed here.
type ImpactRequest struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	// Index is the named hand-off: a revision resolvable in the reviewed
	// repository, or a directory holding an index store.
	Index string
	// Diff is the bundle's unified diff, the only change input.
	Diff string
	// Head is the bundle's head commit, when it read one. It never selects a
	// tree: it decides whether the named index is stale.
	Head string
	// Limit caps each published list ([DefaultLimit] when zero).
	Limit    int `max:"200" min:"1"`
	Warnings []string
}

// Impact is the hand-off answer: the blast-radius data profile, the index the
// rows came from, and the warnings the resolution raised.
type Impact struct {
	Data     *Data
	Index    string
	Warnings []string
}

// ImpactOf computes the blast radius a fetched bundle hands off to a review:
// the changed declarations of Diff, their callers to the depth cap, and the
// candidate tests, against the index the operator named.
//
// A named index that is absent, unresolvable, or corrupt refuses typed; an
// index that answers for a tree other than the bundle head warns and proceeds,
// because the operator asked for that index and the rows still describe it.
// When the head cannot be resolved at all the same warning names what the
// rows describe, so a remote bundle reviewed from a base checkout is never
// silently mislabelled.
func ImpactOf(ctx context.Context, req *ImpactRequest) (*Impact, *core.RefusalError) {
	st := &state{req: &Request{ReadRequest: req.ReadRequest, Warnings: req.Warnings}}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: impactCanceled, Message: impactCanceledMessage}
	}
	if strings.TrimSpace(req.Index) == "" {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: impactNoIndex}
	}
	root, store, at, ref := impactStore(req.Index, st.sourceRoot())
	if ref != nil {
		return nil, ref
	}
	st.trust = st.readTrust(ctx, root)
	tree, commit, ref := st.queryRefAt(ctx, root, at)
	if ref != nil {
		return nil, ref
	}
	t, ref := readTree(store, tree)
	if ref != nil {
		return nil, ref
	}
	headTree, headKnown := st.impactHead(ctx, root, req.Head)
	stale := st.impactStale(tree, req.Head, headTree, headKnown)
	changes := blastChanges(req.Diff)
	pkgs := packageMap(t)
	symbols := blastSymbols(pkgs, t, changes)
	callers, unresolved := blastCallers(pkgs, t, blastRoots(symbols))
	tests, depthTruncated := blastTests(t, symbols)
	blast := &Blast{
		Input:   BlastInput{Head: req.Head, Paths: changedPaths(changes)},
		Indexed: true, Symbols: symbolRows(symbols), Callers: callers,
		Tests: tests, unresolved: unresolved, DepthTruncated: depthTruncated,
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	data := impactData(t, tree, commit, stale, blast, limit)
	return &Impact{Data: data, Index: impactSource(tree), Warnings: st.warnings}, nil
}

// impactSource renders the provenance label a bundle records for the index its
// impact rows came from.
func impactSource(tree string) string { return "index:" + tree }

// impactStore resolves the named hand-off into the git root to resolve in, the
// index store to read, and the revision the tree comes from. A directory that
// holds a store is a checkout; anything else is a revision in the reviewed
// repository.
func impactStore(index, sourceRoot string) (root, store, at string, ref *core.RefusalError) {
	info, err := os.Stat(index)
	if errors.Is(err, os.ErrNotExist) {
		root = sourceRoot
		return root, storePath(root), index, nil
	}
	if err != nil {
		return "", "", "", &core.RefusalError{
			Code: codeIndexFailed, Message: "impact: cannot read --index " + index + colonSpace + err.Error(),
		}
	}
	if !info.IsDir() {
		root = sourceRoot
		return root, storePath(root), index, nil
	}
	dir, absErr := filepath.Abs(index)
	if absErr != nil {
		return "", "", "", &core.RefusalError{
			Code: codeIndexFailed, Message: "impact: --index " + index + " is not addressable: " + absErr.Error(),
		}
	}
	store = storePath(dir)
	if !isDir(filepath.Join(store, impactStoreTrees)) {
		return "", "", "", &core.RefusalError{
			Code:    codeBadFlags,
			Message: fmt.Sprintf(impactNotAStore, dir, config.VerdictDir+"/"+storeDirName),
		}
	}
	return dir, store, impactHeadRef, nil
}

// isDir reports a readable directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// impactHead resolves the bundle head's tree, reporting ok=false when the head
// is empty, is not a commit id, or is not in this clone.
func (st *state) impactHead(ctx context.Context, root, head string) (string, bool) {
	if !shaRe.MatchString(head) {
		return "", false
	}
	tree, ref := st.resolveTree(ctx, root, head)
	if ref != nil {
		return "", false
	}
	return tree, true
}

// impactStale warns when the named index does not answer for the bundle head,
// naming which tree the rows describe, and reports whether it does not.
func (st *state) impactStale(tree, head, headTree string, headKnown bool) bool {
	switch {
	case head == "":
		return false
	case !headKnown:
		st.warn(fmt.Sprintf(impactUnresolved, head, tree))
	case headTree != tree:
		st.warn(fmt.Sprintf(impactHeadStale, tree, headTree))
	default:
		return false
	}
	return true
}

// changedPaths lists the diff's changed paths in diff order, deduplicated.
func changedPaths(changes []blastChange) []string {
	out := make([]string, 0, len(changes))
	for _, ch := range changes {
		if !slices.Contains(out, ch.path) {
			out = append(out, ch.path)
		}
	}
	return out
}

// impactData assembles the published profile: the blast-radius data the index
// verb already renders, with the hand-off's own limit and input.
func impactData(t *treeIndex, tree, commit string, stale bool, blast *Blast, limit int) *Data {
	blast.Counts = blastCounts(blast, t)
	blast.Truncated = blastTruncated(blast, limit)
	cov := coverageOf(t)
	return &Data{
		Kind: QueryBlastRadius, Tree: tree, Commit: commit, Stale: stale,
		Limit: limit, Blast: blast, Files: cov.files, Skipped: cov.skipped,
	}
}
