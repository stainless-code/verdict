package query

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultDiscoveryDepth is the default repository walk depth.
	DefaultDiscoveryDepth = 6
	// MaxDiscoveryDepth is the largest depth accepted by serve.
	MaxDiscoveryDepth = 16
	// DefaultMaxVisits is the default number of directories visited per root.
	DefaultMaxVisits = 20000
	// DefaultMaxRepos is the default number of repositories discovered per root.
	DefaultMaxRepos   = 500
	maxIgnoreWarnings = 20
)

// Discovery describes one bounded repository scan.
type Discovery struct {
	ScannedAt                         time.Time
	Roots                             []Root
	Warnings                          []string
	Visits, Symlinks, SkippedPatterns int
	Cached, Truncated                 bool
}

// DiscoveryOptions bounds a repository scan.
type DiscoveryOptions struct {
	ReadDir                    func(*os.Root, string) ([]os.DirEntry, error)
	Now                        time.Time
	Depth, MaxVisits, MaxRepos int
}

type walkItem struct {
	rel   string
	rules []ignoreRule
	depth int
}

type discoveryWalk struct {
	rootPath string
	realRoot string
	root     *os.Root
	seen     map[string]bool
	opts     DiscoveryOptions
	result   Discovery
}

func normalizeDiscoveryOptions(opts DiscoveryOptions) DiscoveryOptions {
	if opts.Depth < 0 {
		opts.Depth = 0
	}
	if opts.MaxVisits <= 0 {
		opts.MaxVisits = DefaultMaxVisits
	}
	if opts.MaxRepos <= 0 {
		opts.MaxRepos = DefaultMaxRepos
	}
	if opts.ReadDir == nil {
		opts.ReadDir = rootReadDir
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	return opts
}

func rootReadDir(root *os.Root, name string) ([]os.DirEntry, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", name, err) //nolint:wrapcheck // adds the path while preserving the OS error.
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", name, err) //nolint:wrapcheck // adds the path while preserving the OS error.
	}
	return entries, nil
}

// Discover walks root without following directory symlinks.
func Discover(root string, opts DiscoveryOptions) (Discovery, error) {
	opts = normalizeDiscoveryOptions(opts)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Discovery{}, fmt.Errorf("resolve discovery root: %w", err) //nolint:wrapcheck // preserves the path resolution cause.
	}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return Discovery{}, fmt.Errorf("open discovery root: %w", err) //nolint:wrapcheck // preserves the root opening cause.
	}
	defer func() { _ = confined.Close() }()
	walk := discoveryWalk{
		rootPath: root, realRoot: realRoot, root: confined, opts: opts,
		result: Discovery{ScannedAt: opts.Now.UTC()}, seen: map[string]bool{},
	}
	return walk.run(), nil
}

func (w *discoveryWalk) run() Discovery {
	stack := []walkItem{{rel: "."}}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if w.visitLimitReached(item) {
			break
		}
		if w.visit(item, &stack) {
			break
		}
	}
	if w.result.SkippedPatterns > maxIgnoreWarnings {
		w.result.Warnings = append(w.result.Warnings, fmt.Sprintf(
			"… and %d more skipped patterns", w.result.SkippedPatterns-maxIgnoreWarnings,
		))
	}
	return w.result
}

func (w *discoveryWalk) visitLimitReached(item walkItem) bool {
	if w.result.Visits < w.opts.MaxVisits {
		return false
	}
	w.result.Truncated = true
	path := displayPath(w.rootPath, item.rel)
	addDiscoveryWarning(&w.result, fmt.Sprintf("serve: max visits %d reached at %s", w.opts.MaxVisits, path))
	return true
}

func (w *discoveryWalk) visit(item walkItem, stack *[]walkItem) bool {
	entries, err := w.opts.ReadDir(w.root, item.rel)
	w.result.Visits++
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			addDiscoveryWarning(&w.result, fmt.Sprintf("serve: cannot read %s: %v", displayPath(w.rootPath, item.rel), err))
		}
		return false
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	rules := append([]ignoreRule(nil), item.rules...)
	ignorePath := relativePath(item.rel, ".gitignore")
	rules = append(rules, readIgnore(
		w.root, w.rootPath, ignorePath, filepath.ToSlash(item.rel),
		&w.result.Warnings, &w.result.SkippedPatterns,
	)...)
	if w.recordRepositories(item, entries) {
		return true
	}
	if w.result.Truncated || item.depth >= w.opts.Depth {
		return false
	}
	w.enqueueDirectories(item, entries, rules, stack)
	return false
}

func (w *discoveryWalk) recordRepositories(item walkItem, entries []os.DirEntry) bool {
	for _, entry := range entries {
		if entry.Name() != ".verdict" {
			continue
		}
		resolved, ok := w.repositoryIdentity(item)
		if !ok {
			continue
		}
		w.seen[resolved] = true
		if w.recordRepository(item) {
			return true
		}
	}
	return false
}

func (w *discoveryWalk) repositoryIdentity(item walkItem) (string, bool) {
	verdictPath := relativePath(item.rel, ".verdict")
	info, err := w.root.Stat(verdictPath)
	if err != nil || !info.IsDir() {
		return "", false
	}
	verdictRoot, err := w.root.OpenRoot(verdictPath)
	if err != nil {
		return "", false
	}
	_ = verdictRoot.Close()
	resolved, err := filepath.EvalSymlinks(filepath.Join(w.rootPath, filepath.FromSlash(verdictPath)))
	return resolved, err == nil && w.insideRoot(resolved) && !w.seen[resolved]
}

func (w *discoveryWalk) recordRepository(item walkItem) bool {
	w.result.Roots = append(w.result.Roots, Root{Path: filepath.Join(w.rootPath, item.rel)})
	if len(w.result.Roots) < w.opts.MaxRepos {
		return false
	}
	w.result.Truncated = true
	path := displayPath(w.rootPath, item.rel)
	addDiscoveryWarning(&w.result, fmt.Sprintf(
		"serve: max repos %d reached at %s", w.opts.MaxRepos, path,
	))
	return true
}

func (w *discoveryWalk) insideRoot(resolved string) bool {
	rel, err := filepath.Rel(w.realRoot, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *discoveryWalk) enqueueDirectories(item walkItem, entries []os.DirEntry, rules []ignoreRule, stack *[]walkItem) {
	for _, entry := range slices.Backward(entries) {
		w.enqueueDirectory(item, entry, rules, stack)
	}
}

func (w *discoveryWalk) enqueueDirectory(item walkItem, entry os.DirEntry, rules []ignoreRule, stack *[]walkItem) {
	rel := relativePath(item.rel, entry.Name())
	if entry.Type()&fs.ModeSymlink != 0 {
		w.result.Symlinks++
		return
	}
	entryInfo, err := entry.Info()
	if err == nil && entryInfo.Mode()&fs.ModeSymlink != 0 {
		w.result.Symlinks++
		return
	}
	if shouldSkipDiscoveryDirectory(entry.Name()) {
		return
	}
	if !w.isDirectoryCandidate(entry, rel, rules) {
		return
	}
	*stack = append(*stack, walkItem{rel: rel, depth: item.depth + 1, rules: rules})
}

func shouldSkipDiscoveryDirectory(name string) bool {
	return name == ".git" || name == ".verdict" || name == "vendor" || name == "node_modules"
}

func (w *discoveryWalk) isDirectoryCandidate(entry os.DirEntry, rel string, rules []ignoreRule) bool {
	if !entry.IsDir() {
		return false
	}
	path := strings.TrimPrefix(filepath.ToSlash(rel), "./")
	return !ignored(rules, path)
}

func relativePath(parent, name string) string {
	if parent == "." {
		return name
	}
	return filepath.Join(parent, name)
}

func displayPath(root, rel string) string {
	if rel == "." {
		return root
	}
	return filepath.Join(root, rel)
}

func addDiscoveryWarning(result *Discovery, warning string) {
	result.Warnings = append(result.Warnings, warning)
}
