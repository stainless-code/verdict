package lsp

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core/query"
)

const (
	findingsFile = "findings.json"
	localPrefix  = "local-"
	corpusPrefix = "corpus-"
)

// bundleState is one root's composed review: the selected bundle, the digest
// of the artifacts it was composed from, and the joined findings keyed by
// path. It is rebuilt only when the digest moves, so a save that changed
// nothing in the bundle costs one cached Detail and no diff parse.
type bundleState struct {
	byPath    map[string][]int
	paths     map[string]string
	bundle    string
	digest    string
	items     []finding
	truncated bool
	absent    bool
}

// pathsOrNil is the state's root-relative paths with findings.
func (b *bundleState) pathsOrNil() map[string]string {
	if b == nil {
		return nil
	}
	return b.paths
}

// findingsFor returns the joined findings for one root-relative path key.
func (b *bundleState) findingsFor(key string) []finding {
	if b == nil {
		return nil
	}
	idx := b.byPath[key]
	out := make([]finding, 0, len(idx))
	for _, i := range idx {
		out = append(out, b.items[i])
	}
	return out
}

// job is one composition's inputs, snapshotted under stateMu so the slow
// bundle reads run without it: the read model, the client's pinned bundle,
// and the root.
type job struct {
	s      *Server
	layer  ReadModel
	pinned string
	root   servedRoot
}

// newJob snapshots a composition's inputs. The caller holds stateMu.
func (s *Server) newJob(root *servedRoot) *job {
	return &job{s: s, layer: s.layer, pinned: s.initOpts.Bundle, root: *root}
}

// compose rebuilds one root's state. reselect re-runs bundle selection
// (startup, refresh, and a watch change can all add or remove bundles); a
// save keeps the selected bundle and only re-reads its cached detail. The
// returned flag reports whether the state's content moved.
//
// The caller holds composeMu, never stateMu: the inputs are snapshotted and
// the result committed under stateMu, but the reads in between run without
// it, so a slow composition never stalls the read loop.
//
// A failure keeps the last good state: a canceled request, a listing that
// failed, or a bundle that could not be read for a moment (a store mid-write,
// a corrupt file, an unreadable diff) logs a warning and changes nothing, so
// an unreadable bundle never reads as "no findings". The published set
// clears only when the selection really changes or the bundle is gone.
func (s *Server) compose(ctx context.Context, root *servedRoot, reselect bool) bool {
	s.stateMu.Lock()
	j := s.newJob(root)
	prior := s.states[root.ID]
	s.stateMu.Unlock()
	name, ok := j.chosenBundle(ctx, &j.root, prior, reselect)
	if !ok {
		return false
	}
	next, ok := j.build(ctx, &j.root, name, prior)
	if !ok || ctx.Err() != nil {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if !s.serves(root.ID) {
		return false
	}
	s.states[root.ID] = next
	return prior == nil || prior.bundle != next.bundle || prior.digest != next.digest
}

// serves reports whether a root is still served. The caller holds stateMu.
func (s *Server) serves(id string) bool {
	for i := range s.roots {
		if s.roots[i].ID == id {
			return true
		}
	}
	return false
}

// rootsSnapshot copies the served roots. The caller holds stateMu.
func (s *Server) rootsSnapshot() []servedRoot {
	return append([]servedRoot(nil), s.roots...)
}

// selectBundle picks the bundle one root publishes: the operator's --bundle,
// then the client's initializationOptions.bundle, then the newest bundle
// carrying a findings store — `local-` bundles before PR bundles, newest
// findings.json first, name ascending to break ties. Corpus bundles are
// never auto-selected. An empty name means the root has none.
func (j *job) selectBundle(ctx context.Context, root *servedRoot) (string, error) {
	if j.s.cfg.Bundle != "" {
		return j.s.cfg.Bundle, nil
	}
	if j.pinned != "" {
		return j.pinned, nil
	}
	names, err := j.listedBundles(ctx, root)
	if err != nil {
		return "", err
	}
	var candidates []candidate
	for _, name := range names {
		if c, ok := j.candidateFor(root, name); ok {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	slices.SortFunc(candidates, compareCandidates)
	return candidates[0].name, nil
}

// listedBundles names the root's non-corpus bundles the layer lists.
func (j *job) listedBundles(ctx context.Context, root *servedRoot) ([]string, error) {
	view, err := j.layer.Bundles(ctx, "")
	if err != nil {
		return nil, &ioError{op: "list bundles", err: err}
	}
	var listing struct {
		Rows []struct {
			Root   string `json:"root"`
			Bundle string `json:"bundle"`
		} `json:"rows"`
	}
	if err := decodeView(view, &listing); err != nil {
		return nil, err
	}
	var names []string
	for _, row := range listing.Rows {
		if row.Root == root.ID && !strings.HasPrefix(row.Bundle, corpusPrefix) {
			names = append(names, row.Bundle)
		}
	}
	return names, nil
}

// decodeView decodes a composed view's data object into a typed subset.
func decodeView(view *query.View, into any) error {
	if view == nil || view.Data == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(view.Data.MarshalCompact()), into); err != nil {
		return &ioError{op: "decode view", err: err}
	}
	return nil
}

// candidate is one auto-selectable bundle and the keys it ranks by.
type candidate struct {
	mtime time.Time
	name  string
	local bool
}

func compareCandidates(a, b candidate) int {
	if a.local != b.local {
		if a.local {
			return -1
		}
		return 1
	}
	if c := b.mtime.Compare(a.mtime); c != 0 {
		return c
	}
	return cmp.Compare(a.name, b.name)
}

// candidateFor resolves one listed bundle through the layer's confinement
// and keeps it only when it carries a findings store.
func (j *job) candidateFor(root *servedRoot, name string) (candidate, bool) {
	_, dir, err := j.layer.ResolveBundle(root.ID, name)
	if err != nil {
		return candidate{}, false
	}
	info, err := os.Stat(filepath.Join(dir, findingsFile))
	if err != nil || !info.Mode().IsRegular() {
		return candidate{}, false
	}
	return candidate{name: name, mtime: info.ModTime(), local: strings.HasPrefix(name, localPrefix)}, true
}

// chosenBundle keeps the prior selection on a plain save and reselects
// otherwise. ok is false when nothing can be chosen: the request was
// canceled, or the listing failed with no prior selection to keep.
func (j *job) chosenBundle(ctx context.Context, root *servedRoot, prior *bundleState, reselect bool) (string, bool) {
	if prior != nil && !reselect {
		return prior.bundle, true
	}
	name, err := j.selectBundle(ctx, root)
	if ctx.Err() != nil {
		return "", false
	}
	if err != nil {
		j.s.logf(msgWarning, "verdict: bundle selection under %s failed, keeping the current diagnostics: %v",
			root.Path, err)
		if prior == nil {
			return "", false
		}
		return prior.bundle, true
	}
	return name, true
}

// build composes one bundle into a state, reusing prior when the bundle and
// its artifact digest are unchanged. ok is false when the bundle could not
// be read and the caller must keep prior.
func (j *job) build(ctx context.Context, root *servedRoot, name string, prior *bundleState) (*bundleState, bool) {
	if name == "" {
		return j.noBundle(root, prior), true
	}
	detail, err := j.layer.Detail(ctx, root.ID, name)
	if errors.Is(err, query.ErrNotFound) {
		return j.absent(root, name, prior), true
	}
	if err != nil {
		return nil, j.keepGood(ctx, root, name, err)
	}
	digest := sourcesDigest(detail.Sources)
	if prior != nil && prior.bundle == name && prior.digest == digest {
		return prior, true
	}
	return j.joinDetail(ctx, root, name, digest, detail)
}

// absent is the state of a root where the chosen bundle does not exist. It
// is said once per root, on the transition: "gone" when the root published
// it before, "not under" when it never had it (a --bundle pinned to another
// root), and silence while it stays absent.
func (j *job) absent(root *servedRoot, name string, prior *bundleState) *bundleState {
	if prior != nil && prior.bundle == name && prior.absent {
		return prior
	}
	if prior != nil && prior.bundle == name {
		j.s.logf(msgWarning, "verdict: bundle %s is gone from %s; its diagnostics are cleared", name, root.Path)
	} else {
		j.s.logf(msgInfo, "verdict: bundle %s is not under %s; nothing is published there", name, root.Path)
	}
	return &bundleState{bundle: name, absent: true}
}

// keepGood logs why a bundle read failed and that the last good diagnostics
// stay; a canceled request logs nothing. It always reports false.
func (j *job) keepGood(ctx context.Context, root *servedRoot, name string, err error) bool {
	if ctx.Err() == nil {
		j.s.logf(msgWarning, "verdict: bundle %s under %s could not be read, keeping the last good diagnostics: %v",
			name, root.Path, err)
	}
	return false
}

// noBundle is the empty state of a root with no bundle. The remedy is
// logged once, when the root first comes up empty.
func (j *job) noBundle(root *servedRoot, prior *bundleState) *bundleState {
	if prior == nil || prior.bundle != "" {
		j.s.logf(msgInfo, "verdict: no review bundle under %s — run verdict fetch-local <owner/repo> "+
			"<slug>, review it, record it, then run %s", root.Path, CommandRefresh)
	}
	return &bundleState{}
}

// joinDetail decodes a composed detail's report items and joins them
// against the bundle's diff. ok is false when either could not be read.
func (j *job) joinDetail(ctx context.Context, root *servedRoot, name, digest string, detail *query.View) (*bundleState, bool) {
	var wire struct {
		Report struct {
			Items []record `json:"items"`
		} `json:"report"`
	}
	if err := decodeView(detail, &wire); err != nil {
		return nil, j.keepGood(ctx, root, name, err)
	}
	files, err := j.diffFiles(ctx, root, name)
	if err != nil {
		return nil, j.keepGood(ctx, root, name, err)
	}
	state, dropped := joinState(root, name, digest, wire.Report.Items, files, j.s.lim.findings)
	if dropped > 0 {
		j.s.logf(msgWarning, "verdict: %d finding(s) in bundle %s name a path outside %s; never published",
			dropped, name, root.Path)
	}
	if state.truncated {
		j.s.logf(msgWarning, "verdict: bundle %s under %s has %d findings; only the %d highest-ranked are published",
			name, root.Path, len(wire.Report.Items), j.s.lim.findings)
	}
	return state, true
}

// joinState joins the report items against the diff and indexes them by
// path key, keeping at most limit findings in rank order. dropped counts
// records whose path could not be confined.
func joinState(root *servedRoot, name, digest string, items []record, files map[string]diffFile, limit int) (*bundleState, int) {
	state := &bundleState{bundle: name, digest: digest, byPath: map[string][]int{}, paths: map[string]string{}}
	items, state.truncated = capByRank(items, limit)
	dropped := 0
	for i := range items {
		f, ok := joinFinding(&items[i], files)
		if !ok {
			dropped += unconfined(&items[i])
			continue
		}
		f.truncated = state.truncated
		state.add(root, &f)
	}
	return state, dropped
}

// capByRank keeps the limit highest-ranked items; cut reports a cut.
func capByRank(items []record, limit int) (kept []record, cut bool) {
	if len(items) <= limit {
		return items, false
	}
	slices.SortStableFunc(items, func(a, b record) int { return cmp.Compare(rankOf(&a), rankOf(&b)) })
	return items[:limit], true
}

// unconfined is 1 for a record whose path names something outside the root.
func unconfined(r *record) int {
	if r.Path != "" && confineRel(r.Path) != nil {
		return 1
	}
	return 0
}

// add indexes one joined finding under its path key.
func (b *bundleState) add(root *servedRoot, f *finding) {
	key := root.pathKey(f.path)
	b.byPath[key] = append(b.byPath[key], len(b.items))
	b.paths[key] = f.path
	b.items = append(b.items, *f)
}

// rankOf orders report items: their rank, with an unranked item last.
func rankOf(r *record) int {
	if r.Rank <= 0 {
		return math.MaxInt
	}
	return r.Rank
}

// diffFiles reads the bundle's parsed diff, indexing each file's NEW side
// once. A bundle without a full.diff still publishes (every finding falls
// back to its stored line); a diff read that fails is an error the caller
// answers by keeping its last good state.
func (j *job) diffFiles(ctx context.Context, root *servedRoot, name string) (map[string]diffFile, error) {
	view, err := j.layer.Diff(ctx, root.ID, name)
	if err != nil {
		return nil, &ioError{op: "read bundle diff", err: err}
	}
	var wire struct {
		Files []diffFile `json:"files"`
	}
	if err := decodeView(view, &wire); err != nil {
		return nil, err
	}
	out := make(map[string]diffFile, len(wire.Files))
	for i := range wire.Files {
		f := wire.Files[i]
		f.indexNewSide()
		out[f.Path] = f
	}
	return out, nil
}

// sourcesDigest hashes the artifacts a view was composed from: it moves
// exactly when record, fetch-local, or triage rewrites a bundle file.
func sourcesDigest(sources []query.Source) string {
	rows := make([]string, 0, len(sources))
	for _, src := range sources {
		rows = append(rows, src.Path+"\x00"+src.SHA256+"\n")
	}
	slices.Sort(rows)
	sum := sha256.Sum256([]byte(strings.Join(rows, "")))
	return hex.EncodeToString(sum[:])
}
