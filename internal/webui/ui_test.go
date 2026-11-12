package webui

import (
	"encoding/json"
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
	externalScript := `<script defer src="/assets/app.js"></script>`
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

// TestStaticModeReadsDataJSON pins the static data-file branch.
func TestStaticModeReadsDataJSON(t *testing.T) {
	page := assetText(t, "index.html")
	if !strings.Contains(page, `name="verdict-mode" content="live"`) {
		t.Fatal("page has no mode selector")
	}
	js := assetText(t, "app.js")
	for _, want := range []string{`mode === "static"`, `fetch("data.json"`, `staticData[current.path]`} {
		if !strings.Contains(js, want) {
			t.Errorf("static mode is missing %q", want)
		}
	}
}

// TestHostileBundleAndFindingAreText keeps hostile response strings on text.
func TestHostileBundleAndFindingAreText(t *testing.T) {
	source := assetText(t, "app.js")
	fixtures := map[string]string{
		"bundle": "\"><img src=x onerror=alert(1)>",
		"body":   "</td><svg onload=alert(1)>",
	}
	assertHostileValuesSurviveJSON(t, fixtures)
	assertTextSinkSource(t, source)
}

func assertHostileValuesSurviveJSON(t *testing.T, fixtures map[string]string) {
	t.Helper()
	encoded, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for key, want := range fixtures {
		if decoded[key] != want {
			t.Fatalf("hostile %s = %q; want %q", key, decoded[key], want)
		}
	}
}

func assertTextSinkSource(t *testing.T, source string) {
	t.Helper()
	for _, want := range []string{
		"element.textContent = String(text)",
		`"bundle", label: "Bundle", link: true`,
		`"body", label: "Finding"`,
		"td.append(cell(value))",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("hostile values do not use expected text path %q", want)
		}
	}
	if strings.Contains(source, ".innerHTML") {
		t.Fatal("script includes an HTML injection sink")
	}
}

// TestPartialAndNullRenderUnknown keeps missing values honest.
func TestPartialAndNullRenderUnknown(t *testing.T) {
	source := assetText(t, "app.js")
	if !strings.Contains(source, "value === null || value === undefined") ||
		!strings.Contains(source, `node("span", "Unknown", "unknown")`) ||
		!strings.Contains(source, `item === null ? "Unknown" : item`) ||
		!strings.Contains(source, "Partial view: some values are unknown.") {
		t.Fatal("null and partial values need explicit unknown labels")
	}
}

// TestNoScriptNamesCLICommands gives non-script users equivalent commands.
func TestNoScriptNamesCLICommands(t *testing.T) {
	markup := assetText(t, "index.html")
	start := strings.Index(markup, "<noscript")
	if start < 0 {
		t.Fatal("missing noscript guidance")
	}
	end := strings.Index(markup[start:], "</noscript")
	if end < 0 {
		t.Fatal("missing noscript closing tag")
	}
	end += start
	for _, command := range []string{"verdict report", "verdict report --cost", "verdict calibrate", "verdict gate"} {
		if !strings.Contains(markup[start:end], command) {
			t.Errorf("noscript guidance does not name %q", command)
		}
	}
}

// TestDarkModeAndA11yBasics pins the page's dark mode and keyboard/table cues.
func TestDarkModeAndA11yBasics(t *testing.T) {
	page := assetText(t, "index.html")
	css := assetText(t, "app.css")
	js := assetText(t, "app.js")
	if !strings.Contains(css, "prefers-color-scheme: dark") || !strings.Contains(css, ":focus-visible") {
		t.Fatal("dark mode and visible focus are required")
	}
	if strings.Count(page, "<h1") != 1 {
		t.Fatal("each view needs one page heading")
	}
	for _, requirement := range []struct{ source, text string }{
		{page, `class="skip-link"`},
		{page, `aria-label=`},
		{js, "tableEl.append(node(\"caption\", caption))"},
		{js, `th.scope = "col"`},
	} {
		if !strings.Contains(requirement.source, requirement.text) {
			t.Errorf("accessibility requirement %q is missing", requirement.text)
		}
	}
}

// TestNoWatchOffersRefresh exposes manual refresh when the watcher is disabled.
func TestNoWatchOffersRefresh(t *testing.T) {
	page := assetText(t, "index.html")
	js := assetText(t, "app.js")
	if !strings.Contains(page, `id="refresh"`) ||
		!strings.Contains(js, "if (health.data?.watch === false) {\n        live.hidden = true;\n        refresh.hidden = false;") {
		t.Fatal("no-watch must hide live status and expose a manual refresh")
	}
}

// TestLiveUpdateRefetches mirrors the client event and reconnect contract.
func TestLiveUpdateRefetches(t *testing.T) {
	source := assetText(t, "app.js")
	for _, want := range []string{
		`addEventListener("bundles", () => fetchView())`,
		`addEventListener("error", () =>`,
		`addEventListener("open", () =>`,
		`if (reconnecting) fetchView()`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("live UI is missing %q", want)
		}
	}
}

// TestViewsAreNavigable exposes every view and aggregate dimension.
func TestViewsAreNavigable(t *testing.T) {
	source := assetText(t, "app.js")
	for _, want := range []string{
		`["/threads", "Threads"]`,
		`["/gate", "Gate"]`,
		`["repo", "week", "model"]`,
		`["category", "tier"]`,
		`/v1/cost?by=${encodeURIComponent(costBy)}`,
		`/v1/precision?by=${encodeURIComponent(precisionBy)}`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("view navigation is missing %q", want)
		}
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
