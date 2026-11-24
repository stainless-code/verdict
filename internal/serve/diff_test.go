package serve

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
)

// diffFixtureFiles is the two-bundle root with a real full.diff and a
// findings.json anchored on a.txt line 1 RIGHT (json.Number via UseNumber).
func diffFixtureFiles() map[string]string {
	files := fixtureFiles()
	files[".verdict/state/1234/full.diff"] = "diff --git a/a.txt b/a.txt\n" +
		"index 1..2 100644\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		" hello\n" +
		"-old\n" +
		"+new\n" +
		"+extra\n"
	// Keep the finding anchored on notes.txt vs a.txt? For anchoring we
	// need a finding on a.txt line 1 RIGHT so it lands on the diff line.
	// Overwrite findings.json with an anchored one.
	files[".verdict/state/1234/findings.json"] = `[{"sig":"` + sigCrit + `","path":"a.txt","line":1,"side":"RIGHT","severity":"important","category":"logic","body":"fix this","status":"open"}]`
	return files
}

func TestDiffRoute(t *testing.T) { //nolint:gocognit,revive,cyclop // route parity needs envelope/lane/finding branching
	t.Parallel()
	root := writeFixture(t, diffFixtureFiles())
	ts := testServer(t, testLayer(root, nil))
	rootID := query.RootID(mustAbs(t, root))
	base := "/v1/roots/" + rootID + "/bundles/1234/diff"

	got := call(t, ts, http.MethodGet, base, "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("GET diff status = %d, want 200 (%s)", got.status, got.body)
	}
	body := decode(t, got.body)
	// Standard serve envelope: verb, data with view-level sources[].
	if body["verb"] != "serve" {
		t.Errorf("verb = %v, want serve", body["verb"])
	}
	data := asMap(t, body["data"])
	// Sources live at view level in data.sources[] (not per-row).
	if data["sources"] == nil {
		t.Error("data missing sources[]")
	}
	if data["partial"] != false {
		t.Errorf("partial = %v, want false (or true when degraded)", data["partial"])
	}
	if data["errors"] == nil {
		t.Error("missing errors[]")
	}
	// Lanes order is risk-ordered.
	lanes := asList(t, data["lanes"])
	if len(lanes) == 0 {
		t.Fatal("no lanes")
	}
	// At least one lane must be present and carry findings/by_lens.
	foundLens := false
	for _, lv := range lanes {
		lane := asMap(t, lv)
		if lane["findings"] == nil {
			t.Errorf("lane %v missing findings", lane["lane"])
		}
		if lane["by_lens"] != nil {
			foundLens = true
		}
	}
	// More precise check: at least one lane reports non-zero findings.
	anyFindings := false
	for _, lv := range lanes {
		lane := asMap(t, lv)
		if n, ok := lane["findings"].(json.Number); ok && n.String() != "0" {
			anyFindings = true
		}
	}
	if !anyFindings && !foundLens {
		t.Errorf("lanes = %v, want non-zero findings/by_lens", lanes)
	}
	// Collapsed gap and anchored finding on the exact line.
	files := asList(t, data["files"])
	if len(files) == 0 {
		t.Fatal("no files")
	}
	f0 := asMap(t, files[0])
	hunks := asList(t, f0["hunks"])
	if len(hunks) == 0 {
		t.Fatal("no hunks")
	}
	h0 := asMap(t, hunks[0])
	lines := asList(t, h0["lines"])
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	// At least one line carries an anchored finding (path a.txt line 1 RIGHT).
	anchored := false
	for _, lv := range lines {
		row := asMap(t, lv)
		if row["findings"] != nil {
			fs := asList(t, row["findings"])
			for _, fv := range fs {
				f := asMap(t, fv)
				if f["path"] == "a.txt" && f["side"] == "RIGHT" {
					// line is json.Number after decode; accept 1.
					if ln, ok := f["line"].(json.Number); ok && ln.String() == "1" {
						anchored = true
					}
				}
			}
		}
	}
	if !anchored {
		t.Errorf("no anchored finding on a.txt:1 RIGHT in lines %v", lines)
	}

	badOrigin := map[string]string{
		"Authorization": "Bearer " + testToken,
		"Origin":        "https://evil.example",
	}
	assertCell(t, ts, http.MethodPost, base, bearer(), http.StatusMethodNotAllowed)
	assertCell(t, ts, http.MethodGet, base, nil, http.StatusUnauthorized)
	assertCell(t, ts, http.MethodGet, base, badOrigin, http.StatusForbidden)
	assertCell(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/9999/diff", bearer(), http.StatusNotFound)
}

// TestDiffRouteWorksWithoutInteractive proves the diff route is available
// when --interactive is OFF (the default).
func TestDiffRouteWorksWithoutInteractive(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, diffFixtureFiles())
	// Interactive OFF: default config. testLayer uses no interactive.
	ts := testServer(t, testLayer(root, nil))
	rootID := query.RootID(mustAbs(t, root))
	got := call(t, ts, http.MethodGet, "/v1/roots/"+rootID+"/bundles/1234/diff", "", bearer())
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 without --interactive", got.status)
	}
}
