package archtest

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

// legacyReviewRef is the pre-`.verdict/state` bundle path. The shipped binary
// writes `.verdict/state/<pr>/` (and `.verdict/state/local-<name>/`) and
// resolves relative bundle paths there, so a current-state `.review/` mention
// sends an operator to a directory no verb reads. Only the historical notes in
// docs/config.md and docs/manual.md are allowed to keep the old spelling, and
// neither file is scanned here.
const legacyReviewRef = ".review/"

// legacyReviewPath matches a legacy path token, not every substring: the
// component must start at a boundary that a hostname cannot supply (so a URL
// such as https://x.review/ is not a hit), and may be followed by a slash or
// punctuation (so a bare `.review` directory is still reported).
var legacyReviewPath = regexp.MustCompile(`(^|[^[:alnum:].-])\.review($|[^[:alnum:]_-])`)

// docsPathRoots are the current-state surfaces a stale bundle path must never
// reach: the docs site's content and hand-written home pages, every example,
// and the root README. apps/docs/dist and apps/docs/.blume-verify are generated
// and ignored, so they are absent from this list by construction.
var docsPathRoots = []string{
	filepath.Join("apps", "docs", "content"),
	filepath.Join("apps", "docs", "pages"),
	"examples",
	"README.md",
}

// docsPathSkipDirs are directories whose contents are build residue or
// third-party code, never a shipped reference.
var docsPathSkipDirs = map[string]bool{
	"__pycache__":  true,
	"node_modules": true,
	".git":         true,
}

// TestShippedDocsUseTheStateBundlePath fails when a current-state surface
// still names `.review/`. The failure carries file:line so the fix is a jump,
// and it names every hit in one run so a half-finished rename cannot pass.
func TestShippedDocsUseTheStateBundlePath(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	hits := scanForLegacyReviewRef(t, root)
	if len(hits) > 0 {
		t.Fatalf("current-state files name %q; the binary writes .verdict/state/<pr>/ "+
			"(or .verdict/state/local-<name>/). Fix every line below:\n%s",
			legacyReviewRef, strings.Join(hits, "\n"))
	}
}

// TestScanForLegacyReviewRef pins the scanner itself: a file that gains the
// legacy path must be reported with its line number, and a corrected file must
// report nothing. Without this, a scanner that quietly stopped reading would
// keep the tree-facing test green for the wrong reason.
func TestScanForLegacyReviewRef(t *testing.T) {
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
	write("apps/docs/content/stale.mdx", "line one\nbundle at .review/123/\n")
	write("apps/docs/content/bare.mdx", "bundle at .review\n")
	write("apps/docs/content/url.mdx", "see https://x.review/ for the host\n")
	write("apps/docs/content/fixed.mdx", "bundle at .verdict/state/123/\n")
	write("apps/docs/pages/stale.astro", "bundle at .review/456/\n")
	write("examples/stale.yml", "review .review/789/\n")
	write("README.md", "no stale path here\n")
	if err := os.MkdirAll(filepath.Join(root, "examples", "python-client"), 0o755); err != nil {
		t.Fatal(err)
	}

	hits := scanForLegacyReviewRef(t, root)
	want := []string{
		filepath.ToSlash(filepath.Join("apps", "docs", "content", "bare.mdx")) + ":1: bundle at .review",
		filepath.ToSlash(filepath.Join("apps", "docs", "content", "stale.mdx")) + ":2: bundle at .review/123/",
		filepath.ToSlash(filepath.Join("apps", "docs", "pages", "stale.astro")) + ":1: bundle at .review/456/",
		filepath.ToSlash(filepath.Join("examples", "stale.yml")) + ":1: review .review/789/",
	}
	if !slices.Equal(hits, want) {
		t.Fatalf("hits = %v; want %v", hits, want)
	}
}

// scanForLegacyReviewRef walks the current-state roots under root and returns
// one "file:line: text" entry per line naming the legacy bundle path, sorted so
// the report is stable across runs.
func scanForLegacyReviewRef(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	for _, rel := range docsPathRoots {
		hits = append(hits, scanLegacyReviewRoot(t, root, rel)...)
	}
	slices.Sort(hits)
	return hits
}

// scanLegacyReviewRoot scans one current-state root, which may be a directory
// or a single file, and returns its matching lines.
func scanLegacyReviewRoot(t *testing.T, root, rel string) []string {
	t.Helper()
	path := filepath.Join(root, rel)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("scan root %s: %v", rel, err)
	}
	if !info.IsDir() {
		return scanLegacyReviewFile(t, root, path)
	}
	var hits []string
	walkErr := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		return scanLegacyReviewEntry(t, root, p, d, err, &hits)
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", rel, walkErr)
	}
	return hits
}

// scanLegacyReviewEntry is the WalkDir callback for one entry: skip generated
// directories and append matches from files.
func scanLegacyReviewEntry(t *testing.T, root, path string, d os.DirEntry, err error, hits *[]string) error {
	t.Helper()
	if err != nil {
		return err
	}
	if d.IsDir() {
		if docsPathSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	*hits = append(*hits, scanLegacyReviewFile(t, root, path)...)
	return nil
}

// scanLegacyReviewFile reports the legacy path on every matching line of one
// file. A file that is not valid UTF-8 is binary evidence, not prose, and is
// skipped; the bundles carry text only.
func scanLegacyReviewFile(t *testing.T, root, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !utf8.Valid(raw) {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("rel %s: %v", path, err)
	}
	var hits []string
	for i, line := range strings.Split(string(raw), "\n") {
		if legacyReviewPath.MatchString(line) {
			hits = append(hits, filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+": "+line)
		}
	}
	return hits
}
