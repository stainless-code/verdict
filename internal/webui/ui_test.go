package webui

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestUIBudget caps the full embedded asset set.
func TestUIBudget(t *testing.T) {
	var total int64
	if err := fs.WalkDir(FS(), "static", func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if total > 96*1024 {
		t.Fatalf("embedded UI = %d bytes; budget is %d", total, 96*1024)
	}
}

// TestAssetsAreWhitelisted rejects paths outside the public asset set.
func TestAssetsAreWhitelisted(t *testing.T) {
	for _, name := range Names() {
		if _, err := Asset(name); err != nil {
			t.Errorf("Asset(%q): %v", name, err)
		}
	}
	for _, name := range []string{"../app.js", "static/app.js", "unknown", "secret.txt", ""} {
		if _, err := Asset(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Asset(%q) error = %v; want fs.ErrNotExist", name, err)
		}
	}
	if err := fs.WalkDir(FS(), "static", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.Contains(name, "..") || !slices.Contains(Names(), strings.TrimPrefix(name, "static/"))) {
			t.Errorf("unexpected embedded asset %q", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCSPAssetsContainNoInlineCode checks the embedded assets.
func TestCSPAssetsContainNoInlineCode(t *testing.T) {
	markup := strings.ToLower(assetText(t, "index.html"))
	js := strings.ToLower(assetText(t, "app.js"))
	css := strings.ToLower(assetText(t, "app.css"))
	assertNoInlineMarkup(t, strings.Join([]string{markup, js, css}, " "))
	assertSafeScript(t, js)
	assertLocalStylesheet(t, css)
}

func assertNoInlineMarkup(t *testing.T, assets string) {
	t.Helper()
	externalScript := `<script defer src="app.js"></script>`
	if strings.Count(assets, externalScript) != 1 {
		t.Error("page must load exactly one external script")
	}
	assets = strings.ReplaceAll(assets, externalScript, "")
	for _, forbidden := range []string{"<script", "<style", " style=", "javascript:"} {
		if strings.Contains(assets, forbidden) {
			t.Errorf("embedded assets contain %q", forbidden)
		}
	}
}

func assertSafeScript(t *testing.T, source string) {
	t.Helper()
	for _, forbidden := range []string{"eval(", "innerHTML"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("script contains unsafe sink %q", forbidden)
		}
	}
}

func assertLocalStylesheet(t *testing.T, source string) {
	t.Helper()
	if strings.Contains(strings.ToLower(source), "@import") {
		t.Error("stylesheet imports an external sheet")
	}
}

func assetText(t *testing.T, name string) string {
	t.Helper()
	body, err := Asset(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
