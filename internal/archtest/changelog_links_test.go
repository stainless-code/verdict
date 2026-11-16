package archtest

// GitHub Release bodies become the docs site's /changelog pages, where a
// repo-relative link resolves under /changelog/ and breaks. CHANGELOG.md's
// current release section is the release body, and a pending changeset becomes
// the next one, so both must use absolute targets.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

var markdownLinkTarget = regexp.MustCompile(`\]\(([^)]*)\)`)

func TestReleaseNotesUseAbsoluteLinks(t *testing.T) {
	t.Parallel()
	hits := relativeReleaseLinks(t, moduleRoot(t))
	if len(hits) > 0 {
		t.Fatalf("%d relative markdown link target(s) in release notes; use https://, "+
			"mailto:, or a pure #anchor:\n%s", len(hits), strings.Join(hits, "\n"))
	}
}

func TestRelativeReleaseLinkScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CHANGELOG.md", strings.Join([]string{
		"[absolute](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#anchor)",
		"[plain](http://example.com/page)",
		"[mail](mailto:someone@example.com)",
		"[anchor](#anchor)",
		"[relative](docs/manual.md#x)",
		"[parent](../docs/manual.md)",
		"[root](/docs/manual.md)",
		"[scheme](ftp://example.com/page)",
		"[empty](#)",
		"",
	}, "\n"))
	write(".changeset/pending.md", strings.Join([]string{
		"---",
		`"@stainless-code/verdict": patch`,
		"---",
		"",
		"[pending](CONTRIBUTING.md#releases)",
	}, "\n"))

	want := []string{
		".changeset/pending.md:5: [pending](CONTRIBUTING.md#releases)",
		"CHANGELOG.md:5: [relative](docs/manual.md#x)",
		"CHANGELOG.md:6: [parent](../docs/manual.md)",
		"CHANGELOG.md:7: [root](/docs/manual.md)",
		"CHANGELOG.md:8: [scheme](ftp://example.com/page)",
		"CHANGELOG.md:9: [empty](#)",
	}
	if hits := relativeReleaseLinks(t, root); !slices.Equal(hits, want) {
		t.Fatalf("hits = %v; want %v", hits, want)
	}
}

func relativeReleaseLinks(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	hits = append(hits, scanReleaseLinksFile(t, root, "CHANGELOG.md")...)
	dir := filepath.Join(root, ".changeset")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(".changeset", entry.Name()))
		hits = append(hits, scanReleaseLinksFile(t, root, rel)...)
	}
	slices.Sort(hits)
	return hits
}

func scanReleaseLinksFile(t *testing.T, root, rel string) []string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if !utf8.Valid(raw) {
		return nil
	}
	var hits []string
	for i, line := range strings.Split(string(raw), "\n") {
		if hasRelativeReleaseLink(line) {
			hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+line)
		}
	}
	return hits
}

func hasRelativeReleaseLink(line string) bool {
	for _, match := range markdownLinkTarget.FindAllStringSubmatch(line, -1) {
		if !absoluteLinkTarget(match[1]) {
			return true
		}
	}
	return false
}

func absoluteLinkTarget(target string) bool {
	lower := strings.ToLower(strings.TrimSpace(target))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") {
		return true
	}
	return len(target) > 1 && target[0] == '#' && !strings.ContainsAny(target, " \t")
}
