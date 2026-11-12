// Package query is verdict's read model: it composes every served view from
// the same verb services the CLI calls, records the artifacts each view read,
// and returns one transport-neutral [View]. Transports render the envelope;
// the layer never does.
package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	urlpkg "net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// DefaultMaxBundles bounds a root's listing, the cache, and every tick a
	// later slice builds on it.
	DefaultMaxBundles = 500
	// rootIDLen is the visible root identity width: 12 hex characters, the
	// same shape a bundle sig prefix uses.
	rootIDLen = 12
	// defaultCheck is the gate tier a detail composes: the spec's default.
	defaultCheck = "critical"
	// slashSep is the portable separator every wire path uses.
	slashSep = "/"
)

// Source is one artifact a view was composed from: the serving root, the
// bundle it belongs to (empty for a root-level input such as an override
// fragment), the root-relative slash path, its SHA-256, and its byte count.
type Source struct {
	Root   string `json:"root"`
	Bundle string `json:"bundle"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ErrRow is one bundle failure a composed view tolerated: a corrupt store, a
// refused patch, or a feed it could not read. It never replaces a row.
type ErrRow struct {
	Root    string `json:"root"`
	Bundle  string `json:"bundle"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// View is one composed read: the envelope metadata a transport renders, the
// verb data object, the artifacts the view read, and the failures it kept as
// rows.
type View struct {
	At       string
	Next     string
	Repo     string
	Pr       string
	Warnings []string
	Data     *jq.Obj
	Sources  []Source
	Errors   []ErrRow
}

// Root is one served repository root: its visible identity and absolute path.
type Root struct {
	ID   string
	Path string
}

// Snapshot is the layer's fixed input: the roots this process serves, the
// build version a health row reports, and the runtime seam every composed
// service receives.
type Snapshot struct { //nolint:govet // construction snapshot; field order follows the spec type
	Version string
	Roots   []Root
	Runtime *core.Runtime
}

// Options are the layer's resolved serve flags.
type Options struct {
	Check      string
	MaxBundles int
}

// Layer is the in-memory read model. Its cache is a derived optimisation,
// never a source of truth: the stamp comparison rebuilds a stale entry.
type Layer struct { //nolint:govet // the mutex leads the derived-state fields for a readable grouping
	version    string
	roots      []Root
	runtime    *core.Runtime
	check      string
	maxBundles int
	mu         sync.Mutex
	cache      map[cacheKey]entry
	seq        uint64
}

// New builds the read model over a snapshot of roots.
func New(s Snapshot, o Options) *Layer {
	layer := &Layer{
		version:    s.Version,
		runtime:    s.Runtime,
		check:      o.Check,
		maxBundles: o.MaxBundles,
		cache:      map[cacheKey]entry{},
	}
	layer.applyDefaults()
	layer.roots = normalizeRoots(s.Roots)
	return layer
}

// applyDefaults fills the zero values the constructor accepts.
func (l *Layer) applyDefaults() {
	if l.version == "" {
		l.version = core.VersionUnknown
	}
	if l.runtime == nil {
		l.runtime = &core.Runtime{}
	}
	if l.check == "" {
		l.check = defaultCheck
	}
	if l.maxBundles <= 0 {
		l.maxBundles = DefaultMaxBundles
	}
}

// normalizeRoots makes every served root absolute and dedupes by identity.
func normalizeRoots(roots []Root) []Root {
	out := make([]Root, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		abs, err := filepath.Abs(root.Path)
		if err != nil {
			abs = filepath.Clean(root.Path)
		}
		id := RootID(abs)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Root{ID: id, Path: abs})
	}
	return out
}

// RootID derives the 12-hex identity a route addresses a root by. Windows
// volume letters and separators normalize first, so C:\work, c:/work, and a
// trailing separator hash to one id.
func RootID(root string) string {
	sum := sha256.Sum256([]byte(rootIdentity(root)))
	return hex.EncodeToString(sum[:])[:rootIDLen]
}

// rootIdentity normalizes one root path spelling before hashing.
func rootIdentity(root string) string {
	clean := strings.ReplaceAll(root, `\`, slashSep)
	clean = path.Clean(clean)
	if len(clean) >= 2 && clean[1] == ':' {
		clean = strings.ToUpper(clean[:1]) + clean[1:]
	}
	return clean
}

// now reads the injected clock, defaulting to the wall clock.
func (l *Layer) now() time.Time {
	if l.runtime != nil && l.runtime.Now != nil {
		return l.runtime.Now()
	}
	return time.Now()
}

// runtimeFor clones the injection seam so one compose cannot mutate another.
func (l *Layer) runtimeFor() *core.Runtime {
	rt := &core.Runtime{}
	if l.runtime != nil {
		*rt = *l.runtime
	}
	rt.Progress = nil
	return rt
}

// newView starts a view at the injected clock.
func (l *Layer) newView() *View {
	return &View{At: core.NowUTC(l.now())}
}

// rootByID resolves one route id.
func (l *Layer) rootByID(id string) (Root, bool) {
	for _, root := range l.roots {
		if root.ID == id {
			return root, true
		}
	}
	return Root{}, false
}

// resolve maps one bundle name onto its discovered directory. The shared
// bundle-name guard decides what a name may be, so the route and the CLI
// refuse the same spellings; a name outside the discovered set is not found.
// A name whose ResolveBundleDir answer points at a sibling is refused
// bundle-ambiguous instead of reading that sibling.
func (l *Layer) resolve(root Root, bundle string) (string, bool, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(root.Path, bundle)
	if ref != nil {
		return "", false, ref
	}
	names, _, _ := l.listBundles(root)
	if !slices.Contains(names, bundle) {
		return "", false, nil
	}
	want := filepath.ToSlash(filepath.Join(readcommon.StateDir(root.Path), bundle))
	if dir != want {
		return "", false, &core.RefusalError{
			Code: "bundle-ambiguous",
			Message: fmt.Sprintf("serve: bundle %q resolves to %s, not %s "+
				"(address the local bundle as local-%s)", bundle, dir, want, bundle),
		}
	}
	return dir, true, nil
}

// compose runs the report service with the offline posture the spec fixes.
func (l *Layer) compose(
	ctx context.Context, root Root, bundle, dir string, flags *core.ReportFlags,
) (*core.ReportResponse, *core.RefusalError) {
	read := core.ReadRequest{
		Runtime:  l.runtimeFor(),
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		RepoRoot: root.Path,
	}
	req := &core.ReportRequest{
		ReadRequest:  read,
		Flags:        *flags,
		OverridesDir: filepath.Join(root.Path, config.VerdictDir, "overrides"),
	}
	return reportcore.Run(ctx, req)
}

// inboxFlags compose one inbox row: every status and kind, the offline live
// posture, and the cost rollup.
func inboxFlags() core.ReportFlags {
	return core.ReportFlags{Status: "all", Kind: "all", Live: "skip", Cost: true}
}

// detailFlags compose one detail: the inbox flags plus the requested tier.
func (l *Layer) detailFlags() core.ReportFlags {
	flags := inboxFlags()
	flags.Check = l.check
	return flags
}

// errRow builds one tolerated failure row.
func errRow(root Root, bundle, code, message string) ErrRow {
	return ErrRow{Root: root.ID, Bundle: bundle, Code: code, Message: message}
}

// listBundles reads one root's state directory: sorted directory names that
// are addressable bundle slugs, truncated at the bundle cap.
func (l *Layer) listBundles(root Root) ([]string, bool, []ErrRow) {
	entries, err := os.ReadDir(readcommon.StateDir(root.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, []ErrRow{errRow(root, "", "corrupt-store",
			"bundle directory unreadable: "+err.Error())}
	}
	names, truncated := l.capBundles(bundleNames(root, entries))
	return names, truncated, nil
}

// bundleNames keeps the addressable bundle directories in readdir order,
// asking the shared guard the same question every verb asks.
func bundleNames(root Root, entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ref := config.BundleDir(root.Path, entry.Name()); ref != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

// capBundles applies the bundle cap and reports whether it truncated.
func (l *Layer) capBundles(names []string) ([]string, bool) {
	if len(names) <= l.maxBundles {
		return names, false
	}
	return names[:l.maxBundles], true
}

// bundleRepo names the repository a bundle belongs to: a local source.json's
// repo, else the review URL's project path. GitHub and Forgejo put owner/repo
// before /pull/, /pulls/, or /issues/; GitLab puts every subgroup before /-/.
func bundleRepo(dir string) string {
	if repo := readcommon.JSONStr(dir+"/source.json", "repo"); repo != "" {
		return repo
	}
	url := readcommon.OpenBundle(dir).StrField("url")
	if url == "" {
		return ""
	}
	parsed, err := urlpkg.Parse(url)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, slashSep), slashSep)
	if len(parts) < 2 {
		return ""
	}
	for i := 2; i < len(parts); i++ {
		if parts[i] == "-" && i+1 < len(parts) && parts[i+1] == "merge_requests" {
			return path.Join(parts[:i]...)
		}
		if repoRouteSegment(parts[i]) {
			if _, err := strconv.Atoi(parts[i+1]); err == nil {
				return path.Join(parts[:i]...)
			}
		}
	}
	return path.Join(parts[:2]...)
}

// repoRouteSegment reports one host route segment that ends the project path.
func repoRouteSegment(segment string) bool {
	switch segment {
	case "pull", "pulls", "issues", "merge_requests":
		return true
	default:
		return false
	}
}

// numericPr reads a bundle name as the envelope's numeric pr, null when the
// name is not a number (a local or corpus slug).
func numericPr(bundle string) any {
	if n, err := strconv.Atoi(bundle); err == nil && n >= 0 {
		return n
	}
	return nil
}

// bundleDir is the root-relative bundle path a row and report carry.
func bundleDir(bundle string) string {
	return filepath.ToSlash(filepath.Join(config.VerdictDir, config.StateDirName, bundle))
}

// postModel reads the agent model triple's model name from post.json, null
// when the receipt is absent or carries none.
func postModel(dir string) any {
	record := readcommon.PostRecord(dir)
	if record == nil {
		return nil
	}
	if model, ok := record["model"].(string); ok && model != "" {
		return model
	}
	return nil
}

// nullableString renders a nullable string field.
func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// normalizeReportData renders report data and makes dir root-relative: the
// CLI run from the repository root prints the same path, so the served data
// stays byte-identical.
func normalizeReportData(root string, data *core.ReportData) *jq.Obj {
	wire := data.Wire()
	dir, ok := wire.Get("dir")
	if !ok {
		return wire
	}
	text, ok := dir.(string)
	if !ok {
		return wire
	}
	rel, err := filepath.Rel(root, filepath.FromSlash(text))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return wire
	}
	wire.Set("dir", filepath.ToSlash(rel))
	return wire
}

// sourceWire renders one source in the published key order.
func sourceWire(source Source) *jq.Obj {
	return jq.NewObj(
		"root", source.Root,
		"bundle", source.Bundle,
		"path", source.Path,
		"sha256", source.SHA256,
		"bytes", source.Bytes,
	)
}

// sourcesWire renders sources in the published array shape.
func sourcesWire(sources []Source) []any {
	out := make([]any, 0, len(sources))
	for _, source := range sources {
		out = append(out, sourceWire(source))
	}
	return out
}

// errorsWire renders tolerated failures in the published array shape.
func errorsWire(rows []ErrRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(
			"root", row.Root,
			"bundle", row.Bundle,
			"code", row.Code,
			"message", row.Message,
		))
	}
	return out
}

// mergeSources dedupes two source lists by path and sorts the result.
func mergeSources(left, right []Source) []Source {
	seen := make(map[string]bool, len(left)+len(right))
	out := make([]Source, 0, len(left)+len(right))
	for _, source := range append(append([]Source{}, left...), right...) {
		if seen[source.Path] {
			continue
		}
		seen[source.Path] = true
		out = append(out, source)
	}
	slices.SortFunc(out, func(a, b Source) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// threadView reads one bundle's thread feed. A missing feed is absence, not
// an error; an unreadable or wrong-version feed is a tolerated error row.
func (l *Layer) threadView(root Root, bundle, dir string) (any, *ErrRow) {
	feedPath := dir + slashSep + threadscore.KeyThreadsJSON
	if !readcommon.IsFile(feedPath) {
		return nil, nil
	}
	feed, err := threadscore.ReadFile(feedPath)
	if err != nil {
		row := errRow(root, bundle, "corrupt-store", err.Error())
		return nil, &row
	}
	return feed.Wire(), nil
}

// artifactNames is the bundle inventory the evidence ledger reports, in the
// order a reader expects: the store first, then the receipts, then the raw
// inputs.
var artifactNames = []string{
	"findings.json",
	"PR.json",
	"source.json",
	"post.json",
	"record.json",
	"patch.json",
	"panel",
	"cost.json",
	"controls.json",
	"run.json",
	"threads.json",
	"review.json",
	"full.diff",
	"chunks/manifest.json",
}

// artifacts renders the ledger: every inventoried name with its presence and,
// when present, the hash and size the source list already carries.
func artifacts(bundle string, sources []Source) []any {
	byPath := make(map[string]Source, len(sources))
	for _, source := range sources {
		byPath[source.Path] = source
	}
	out := make([]any, 0, len(artifactNames))
	for _, name := range artifactNames {
		rel := path.Join(bundleDir(bundle), name)
		source, present := byPath[rel]
		var digest, size any
		if present {
			digest = source.SHA256
			size = source.Bytes
		}
		out = append(out, jq.NewObj(
			"path", filepath.ToSlash(name),
			"present", present,
			"sha256", digest,
			"bytes", size,
		))
	}
	return out
}

// digestBytes hashes one capped read. It never allocates a second copy of the
// bytes: the hash is the only thing a Source keeps.
func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
