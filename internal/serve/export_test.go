package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestExportDestinationRefusals rejects unrelated directories and symlinks.
func TestExportDestinationRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := exportDestination(dir)
	ref, ok := errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-not-export" {
		t.Fatalf("non-export refusal = %v; want html-not-export", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	err = exportDestination(link)
	ref, ok = errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-symlink" {
		t.Fatalf("symlink refusal = %v; want html-symlink", err)
	}
}

func exportLayer(t *testing.T) *query.Layer {
	t.Helper()
	root := writeFixture(t, fixtureFiles())
	return query.New(query.Snapshot{Version: "test", Roots: []query.Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}}, query.Options{Clock: fixedNow, NoWatch: true})
}

// TestHTMLSnapshot exercises API parity, determinism and all six UI views.
func TestHTMLSnapshot(t *testing.T) {
	layer := exportLayer(t)
	dest := filepath.Join(t.TempDir(), "export space")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	body := readExport(t, filepath.Join(dest, "data.json"))
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server := &Server{layer: layer}
	server.handleBundles(recorder, httptest.NewRequestWithContext(t.Context(), "GET", "/v1/bundles", nil))
	if string(data["/v1/bundles"])+"\n" != recorder.Body.String() {
		t.Fatal("export envelope differs byte-for-byte from /v1/bundles")
	}
	other, err := snapshotData(t.Context(), layer, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, other) {
		t.Fatal("snapshot is not deterministic")
	}
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	runExportUI(t, dest)
}

// TestHTMLRollback restores the previous snapshot after failed publication.
func TestHTMLRollback(t *testing.T) {
	layer := exportLayer(t)
	dest := filepath.Join(t.TempDir(), "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	before := readExport(t, filepath.Join(dest, "data.json"))
	calls := 0
	seam := func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("injected publish failure")
		}
		return os.Rename(from, to)
	}
	if err := exportWithRename(t.Context(), dest, layer, fixedNow(), seam); err == nil {
		t.Fatal("publish failure was lost")
	}
	after := readExport(t, filepath.Join(dest, "data.json"))
	if calls != 3 || !bytes.Equal(before, after) {
		t.Fatal("rollback did not restore the old export")
	}
	if err := exportRemnants(dest); err != nil {
		t.Fatal(err)
	}
}

// TestHTMLInterruptedRefresh kills a publisher between the two renames.
func TestHTMLInterruptedRefresh(t *testing.T) {
	layer := exportLayer(t)
	dest := filepath.Join(t.TempDir(), "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	before := readExport(t, filepath.Join(dest, "data.json"))
	interruptExport(t, dest, layer)
	old := oldExport(t, dest)
	after := readExport(t, filepath.Join(filepath.Dir(dest), old, "data.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("interruption damaged the old snapshot")
	}
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("missing recovery refusal: %v", err)
	}
}

// TestHTMLStaleStage rejects leftover staging directories.
func TestHTMLStaleStage(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "export[1]")
	if err := os.Mkdir(dest+".tmp-stale", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := exportRemnants(dest); err == nil {
		t.Fatal("stale stage was accepted")
	}
}

func runExportUI(t *testing.T, dest string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "--test", "../webui/test/app.test.mjs")
	cmd.Env = append(testenv.Base(nil), "VERDICT_EXPORT="+dest)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exported six-view harness: %v\n%s", err, out)
	}
}

func interruptExport(t *testing.T, dest string, _ Layer) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestHTMLCrashChild$")
	cmd.Env = testenv.Base(map[string]string{"VERDICT_CRASH_EXPORT": dest, "HOME": t.TempDir()})
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("publisher did not crash: %s", out)
	}
	t.Logf("publisher termination: %v; %s", err, out)
}

// TestHTMLCrashChild is the isolated publisher killed at the publication seam.
func TestHTMLCrashChild(t *testing.T) {
	dest := os.Getenv("VERDICT_CRASH_EXPORT")
	if dest == "" {
		return
	}
	layer := query.New(query.Snapshot{}, query.Options{Clock: fixedNow, NoWatch: true})
	if err := exportWithRename(t.Context(), dest, layer, fixedNow(), crashRename); err != nil {
		t.Fatal(err)
	}
}

func crashRename(from, to string) error {
	if !strings.Contains(from, ".tmp-") {
		return os.Rename(from, to)
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return process.Kill()
}

func readExport(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func oldExport(t *testing.T, dest string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	var old string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "export.old-") {
			old = entry.Name()
		}
	}
	if len(entries) != 2 || old == "" {
		t.Fatalf("old export missing after interruption: %v", entries)
	}
	return old
}
