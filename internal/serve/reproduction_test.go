package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/repro"
)

func TestReproductionDownload(t *testing.T) {
	files := fixtureFiles()
	raw := finding(sigCrit, "open", "critical", "reproduced")
	files[filepath.ToSlash(filepath.Join(".verdict", "state", "1234", "findings.json"))] = `[` + strings.TrimSuffix(raw, "}") + `,"reproduction":{"path":"tests/` + sigCrit + `.sh","command":"sh artifact"}}]`
	dir := writeFixture(t, files)
	bundle := filepath.Join(dir, ".verdict", "state", "1234")
	if _, err := repro.Capture(bundle, sigCrit, "", "false", false); err != nil {
		t.Fatal(err)
	}
	ts := testServer(t, testLayer(dir, nil))
	path := "/v1/roots/" + query.RootID(mustAbs(t, dir)) + "/bundles/1234/findings/" + sigCrit + "/reproduction"
	result := call(t, ts, http.MethodGet, path, "", bearer())
	if result.status != http.StatusOK || string(result.body) != "false\n" {
		t.Fatalf("download = %d %q", result.status, result.body)
	}
	if err := os.Remove(filepath.Join(bundle, "tests", sigCrit+".sh")); err != nil {
		t.Fatal(err)
	}
	result = call(t, ts, http.MethodGet, path, "", bearer())
	if result.status != http.StatusNotFound {
		t.Fatalf("missing artifact status = %d", result.status)
	}
}
