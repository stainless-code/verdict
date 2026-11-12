package serve

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestHTMLExportCSP pins the static page policy before any loaded assets.
func TestHTMLExportCSP(t *testing.T) {
	dest := filepath.Join(exportTempDir(t), "export")
	if err := exportHTML(t.Context(), dest, exportLayer(t), fixedNow()); err != nil {
		t.Fatal(err)
	}
	page := string(readExport(t, filepath.Join(dest, "index.html")))
	tag := `<meta http-equiv="Content-Security-Policy" content="` + wantCSP + `" />`
	position := strings.Index(page, tag)
	if position < 0 {
		t.Fatal("exported index is missing the exact served CSP meta policy")
	}
	if strings.Count(page, `http-equiv="Content-Security-Policy"`) != 1 {
		t.Fatal("export must have exactly one CSP meta policy")
	}
	for _, asset := range []string{`<link`, `<script`} {
		if index := strings.Index(page, asset); index < 0 || index < position {
			t.Fatalf("CSP must precede %s", asset)
		}
	}
}
