package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	registryTTL       = 24 * time.Hour
	registryReadCap   = 8 << 20
	registryDirMode   = 0o700
	registryFileMode  = 0o600
	registrySchemaVer = "1"
	registryWriteMask = 0o022
)

var (
	errUnsafeRegistryFile      = errors.New("unsafe registry file")
	errUnsafeRegistryDirectory = errors.New("unsafe registry directory")
	errInvalidRegistryPath     = errors.New("invalid registry path")
)

type registry struct {
	Version string         `json:"schema_version"`
	Roots   []registryRoot `json:"roots"`
}

type registryRoot struct {
	ScannedAt       time.Time `json:"scanned_at"`
	Path            string    `json:"path"`
	Repos           []string  `json:"repos"`
	Warnings        []string  `json:"warnings"`
	Depth           int       `json:"depth"`
	MaxVisits       int       `json:"max_visits"`
	MaxRepos        int       `json:"max_repos"`
	Visits          int       `json:"visits"`
	Symlinks        int       `json:"symlinks"`
	SkippedPatterns int       `json:"skipped_patterns"`
	Truncated       bool      `json:"truncated"`
}

// DiscoverCached loads a fresh walk or scans and atomically replaces its
// registry entry.
func DiscoverCached(root string, opts DiscoveryOptions, registryPath string, force, disabled bool) (Discovery, error) {
	opts = normalizeDiscoveryOptions(opts)
	saved, warning := loadRegistry(registryPath, disabled)
	if !disabled && !force {
		if discovery, ok, err := findCached(root, opts, saved); ok || err != nil {
			return discovery, err
		}
	}
	return rebuildDiscovery(root, opts, registryPath, saved, warning, disabled)
}

func rebuildDiscovery(root string, opts DiscoveryOptions, path string, saved registry, warning string, disabled bool) (Discovery, error) {
	result, err := Discover(root, opts)
	if err != nil {
		return result, err
	}
	scanWarnings := append([]string(nil), result.Warnings...)
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if !disabled {
		result.Warnings = persistRegistry(root, opts, path, saved, &result, scanWarnings)
	}
	return result, nil
}

func loadRegistry(path string, disabled bool) (registry, string) {
	if disabled {
		return registry{}, ""
	}
	var saved registry
	raw, err := readRegistry(path)
	if err == nil {
		err = json.Unmarshal(raw, &saved)
		if err == nil && saved.Version != registrySchemaVer {
			err = errors.New("wrong registry version")
		}
	}
	if err != nil {
		return registry{}, "serve: registry unavailable or corrupt; rebuilding"
	}
	return saved, ""
}

func readRegistry(path string) ([]byte, error) {
	root, name, err := openRegistryParent(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := openSafeRegistry(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := jq.Capped(f, registryReadCap)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err) //nolint:wrapcheck // keeps the capped-reader cause available to callers.
	}
	return raw, nil
}

func openSafeRegistry(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil || !safeRegistryFile(before) {
		return nil, errUnsafeRegistryFile //nolint:wrapcheck // callers map unsafe leaves to a cache miss.
	}
	f, err := readcommon.OpenNoBlockIn(root, name)
	if err != nil {
		return nil, fmt.Errorf("open registry file: %w", err) //nolint:wrapcheck // preserves the descriptor-open cause.
	}
	opened, err := f.Stat()
	if err != nil || !safeRegistryFile(opened) {
		_ = f.Close()
		return nil, errUnsafeRegistryFile //nolint:wrapcheck // callers map unsafe leaves to a cache miss.
	}
	after, err := root.Lstat(name)
	if err != nil || !safeRegistryFile(after) || !os.SameFile(opened, after) {
		_ = f.Close()
		return nil, errUnsafeRegistryFile //nolint:wrapcheck // callers map unsafe leaves to a cache miss.
	}
	return f, nil
}

func safeRegistryFile(info os.FileInfo) bool {
	return info != nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && !unsafeRegistryPermissions(info.Mode())
}

func unsafeRegistryPermissions(mode os.FileMode) bool {
	return runtime.GOOS != "windows" && mode.Perm()&registryWriteMask != 0
}

func openRegistryParent(path string) (*os.Root, string, error) {
	dir, name := filepath.Dir(path), filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return nil, "", errInvalidRegistryPath //nolint:wrapcheck // loadRegistry converts invalid cache paths to a rebuild.
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, "", fmt.Errorf("inspect registry directory: %w", err) //nolint:wrapcheck // keeps the cause for diagnostics.
	}
	if !safeRegistryDirectory(info) {
		return nil, "", errUnsafeRegistryDirectory //nolint:wrapcheck // unsafe cache directories are reported as unavailable.
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("open registry directory: %w", err) //nolint:wrapcheck // keeps the root-open cause for diagnostics.
	}
	if !sameRegistryDirectory(root, info) {
		_ = root.Close()
		return nil, "", errUnsafeRegistryDirectory //nolint:wrapcheck // unsafe cache directories are reported as unavailable.
	}
	return root, name, nil
}

func safeRegistryDirectory(info os.FileInfo) bool {
	return info != nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() && !unsafeRegistryPermissions(info.Mode())
}

func sameRegistryDirectory(root *os.Root, info os.FileInfo) bool {
	opened, err := root.Stat(".")
	return err == nil && os.SameFile(info, opened)
}

func findCached(root string, opts DiscoveryOptions, saved registry) (Discovery, bool, error) {
	for i := range saved.Roots {
		item := &saved.Roots[i]
		if !registryMatches(item, root, opts) || opts.Now.Before(item.ScannedAt) || opts.Now.Sub(item.ScannedAt) >= registryTTL {
			continue
		}
		result, err := cachedDiscovery(root, item)
		return result, true, err
	}
	return Discovery{}, false, nil
}

func persistRegistry(root string, opts DiscoveryOptions, path string, saved registry, result *Discovery, scanWarnings []string) []string {
	item := registryEntry(root, opts, result, scanWarnings)
	saved.Version = registrySchemaVer
	saved.Roots = replaceRegistryRoot(saved.Roots, &item)
	raw, err := json.Marshal(saved)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), registryDirMode)
	}
	if err == nil {
		err = writeRegistry(path, raw)
	}
	if err != nil {
		return append(result.Warnings, "serve: registry write failed: "+err.Error())
	}
	return result.Warnings
}

func writeRegistry(path string, raw []byte) error {
	root, name, err := openRegistryParent(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := atomic.WriteFileModeIn(root, name, raw, registryFileMode); err != nil {
		return fmt.Errorf("write registry: %w", err) //nolint:wrapcheck // persistRegistry reports cache-write failures as warnings.
	}
	return nil
}

func registryEntry(root string, opts DiscoveryOptions, result *Discovery, warnings []string) registryRoot {
	item := registryRoot{
		Path: root, Depth: opts.Depth, MaxVisits: opts.MaxVisits, MaxRepos: opts.MaxRepos,
		ScannedAt: result.ScannedAt, Truncated: result.Truncated, Visits: result.Visits,
		Symlinks: result.Symlinks, SkippedPatterns: result.SkippedPatterns,
		Warnings: warnings,
	}
	for _, repo := range result.Roots {
		item.Repos = append(item.Repos, repo.Path)
	}
	return item
}

func replaceRegistryRoot(roots []registryRoot, item *registryRoot) []registryRoot {
	kept := make([]registryRoot, 0, len(roots)+1)
	for i := range roots {
		old := &roots[i]
		if old.Path == item.Path && old.Depth == item.Depth && old.MaxVisits == item.MaxVisits && old.MaxRepos == item.MaxRepos {
			continue
		}
		kept = append(kept, *old)
	}
	return append(kept, *item)
}

func registryMatches(item *registryRoot, root string, opts DiscoveryOptions) bool {
	return item.Path == root && item.Depth == opts.Depth && item.MaxVisits == opts.MaxVisits &&
		item.MaxRepos == opts.MaxRepos && !item.ScannedAt.IsZero()
}

func cachedDiscovery(root string, item *registryRoot) (Discovery, error) {
	confined, err := os.OpenRoot(root)
	if err != nil {
		return Discovery{}, fmt.Errorf("open cached discovery root: %w", err) //nolint:wrapcheck // preserves the root opening cause.
	}
	defer func() { _ = confined.Close() }()
	result := Discovery{
		Cached: true, ScannedAt: item.ScannedAt, Truncated: item.Truncated,
		Visits: item.Visits, Symlinks: item.Symlinks, SkippedPatterns: item.SkippedPatterns,
		Warnings: append([]string(nil), item.Warnings...),
	}
	for _, repo := range item.Repos {
		if cachedRepoIsPresent(confined, root, repo) {
			result.Roots = append(result.Roots, Root{Path: repo})
		}
	}
	return result, nil
}

func cachedRepoIsPresent(root *os.Root, rootPath, repo string) bool {
	rel, err := filepath.Rel(rootPath, repo)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	verdictPath := config.VerdictDir
	if rel != "." {
		verdictPath = filepath.Join(rel, config.VerdictDir)
	}
	info, err := root.Stat(verdictPath)
	if err != nil || !info.IsDir() {
		return false
	}
	verdictRoot, err := root.OpenRoot(verdictPath)
	if err != nil {
		return false
	}
	_ = verdictRoot.Close()
	return true
}
