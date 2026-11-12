package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	registryTTL       = 24 * time.Hour
	registryReadCap   = 8 << 20
	registryDirMode   = 0o700
	registryFileMode  = 0o600
	registrySchemaVer = "1"
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
	raw, err := jq.CappedFile(path, registryReadCap)
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
		err = atomic.WriteFileMode(path, raw, registryFileMode)
	}
	if err != nil {
		return append(result.Warnings, "serve: registry write failed: "+err.Error())
	}
	return result.Warnings
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
