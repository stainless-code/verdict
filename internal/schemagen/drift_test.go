package schemagen

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSchemasMatchGenerated fails when a committed schema differs from the
// generator output, naming the regeneration command and showing the diff.
func TestSchemasMatchGenerated(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	dir := filepath.Join(root, "schemas")
	generated := make(map[string]bool, len(files))
	for _, file := range files {
		generated[file.Name] = true
		checkCommitted(t, dir, file)
	}
	checkOrphans(t, dir, generated)
}

// checkCommitted compares one generated file with its committed copy.
func checkCommitted(t *testing.T, dir string, file File) {
	t.Helper()
	committed, err := os.ReadFile(filepath.Join(dir, file.Name))
	if err != nil {
		t.Fatalf("read committed %s: %v", file.Name, err)
	}
	if bytes.Equal(committed, file.Data) {
		return
	}
	t.Errorf("%s is stale; run `make schemas` from the repo root and commit the result:\n%s",
		file.Name, DiffFiles(file.Name, committed, file.Data))
}

// checkOrphans fails when schemas/ carries a file the generator never writes.
func checkOrphans(t *testing.T, dir string, generated map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read schemas dir: %v", err)
	}
	for _, orphan := range orphanSchemas(entries, generated) {
		t.Errorf("schemas/%s is not produced by the generator; remove it or add its Go type", orphan)
	}
}

// orphanSchemas returns committed schema files the generator never writes.
func orphanSchemas(entries []os.DirEntry, generated map[string]bool) []string {
	var orphans []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		if !generated[entry.Name()] {
			orphans = append(orphans, entry.Name())
		}
	}
	sort.Strings(orphans)
	return orphans
}
