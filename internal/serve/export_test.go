package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	dir := exportTempDir(t)
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
	link := filepath.Join(exportTempDir(t), "link")
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
	root := writeFixture(t, htmlFixtureFiles())
	return query.New(query.Snapshot{Version: "test", Roots: []query.Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}}, query.Options{Clock: fixedNow, NoWatch: true})
}

// TestHTMLSnapshot exercises API parity, determinism and all six UI views.
func TestHTMLSnapshot(t *testing.T) {
	layer := exportLayer(t)
	dest := filepath.Join(exportTempDir(t), "export space")
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
	dest := filepath.Join(exportTempDir(t), "export")
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
	parent := filepath.Join(exportTempDir(t), "ancestor.tmp-dir")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(parent, "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	before := readExport(t, filepath.Join(dest, "data.json"))
	interruptExport(t, dest)
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
	dest := filepath.Join(exportTempDir(t), "export[1]")
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

func interruptExport(t *testing.T, dest string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestHTMLCrashChild$")
	cmd.Env = testenv.Base(map[string]string{"VERDICT_CRASH_EXPORT": dest, "HOME": exportTempDir(t)})
	cmd.WaitDelay = time.Second
	var diag bytes.Buffer
	cmd.Stderr = &diag
	scanner := startExportPublisher(t, cmd)
	if !scanner.Scan() || scanner.Text() != "export publication seam" {
		t.Fatalf("publisher did not reach seam: %q; %v", scanner.Text(), scanner.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatalf("publisher did not crash: %s", diag.String())
	} else {
		t.Logf("publisher termination at seam: %v; %s", err, diag.String())
	}
}

func startExportPublisher(t *testing.T, cmd *exec.Cmd) *bufio.Scanner {
	t.Helper()
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reapExportPublisher(t, cmd) })
	return bufio.NewScanner(output)
}

func reapExportPublisher(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.ProcessState != nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill publisher: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Logf("publisher cleanup: %v", err)
	}
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
	if !strings.HasPrefix(filepath.Base(from), filepath.Base(to)+".tmp-") {
		return os.Rename(from, to)
	}
	if _, err := fmt.Fprintln(os.Stdout, "export publication seam"); err != nil {
		return err
	}
	var release [1]byte
	if _, err := os.Stdin.Read(release[:]); err != nil {
		return err
	}
	return errors.New("publisher released without being killed")
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

// TestHTMLRefusalParity preserves the bundle identity in refused envelopes.
func TestHTMLRefusalParity(t *testing.T) {
	files := fixtureFiles()
	files[".verdict/state/1234/findings.json"] = "null"
	root := writeFixture(t, files)
	layer := query.New(query.Snapshot{Roots: []query.Root{{Path: root}}, Runtime: &core.Runtime{Now: fixedNow}},
		query.Options{Clock: fixedNow, NoWatch: true})
	body, err := snapshotData(t.Context(), layer, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	rootID := query.RootID(root)
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	req.SetPathValue("root", rootID)
	req.SetPathValue("bundle", "1234")
	recorder := httptest.NewRecorder()
	server := &Server{layer: layer, now: fixedNow}
	server.handleDetail(recorder, req)
	if string(data["/v1/roots/"+rootID+"/bundles/1234"])+"\n" != recorder.Body.String() {
		t.Fatalf("refused export differs from /v1: %s\n%s", body, recorder.Body.String())
	}
}

// TestHTMLCancellation returns the shared cancellation exit code.
func TestHTMLCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	runner := &cli.Runner{Out: &out, Diag: &out, Human: &out, Now: fixedNow}
	args := []string{"--html", filepath.Join(exportTempDir(t), "export"), "--root", writeFixture(t, fixtureFiles())}
	if code := RunHTML(ctx, runner, args, "test"); code != 3 {
		t.Fatalf("canceled export exit = %d; want 3", code)
	}
}

func htmlFixtureFiles() map[string]string {
	files := fixtureFiles()
	files[".verdict/state/1234/cost.json"] = `[{"models":[{"name":"alpha","input_tokens":10,"output_tokens":5}],"est_usd":0.25}]`
	files[".verdict/state/local-1/cost.json"] = `[{"models":[{"name":"beta","input_tokens":3,"output_tokens":2}],"est_usd":0.1}]`
	files[".verdict/state/1234/threads.json"] = `{"schema_version":"1","provider":"github","pr":1234,"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"fetched_at":"2026-11-12T09:00:00Z","truncated":false,"threads":[{"id":"T_export","path":"notes.txt",` +
		`"line":2,"start_line":null,"side":"RIGHT","resolved":false,"outdated":false,"url":null,` +
		`"author":"reviewer","author_kind":"human","finding_sig":"c111111111111111111111111111111111111111",` +
		`"kind":"review-comment","comments":[{"id":"C_export","author":"reviewer","author_kind":"human",` +
		`"body":"Exported discussion","truncated":false,"created_at":"2026-11-12T09:00:00Z"}]}]}`
	return files
}

func exportTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestHTMLParentSymlink refuses an alias before creating any export files.
func TestHTMLParentSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	dir := exportTempDir(t)
	target := filepath.Join(dir, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	layer := exportLayer(t)
	for _, dest := range []string{filepath.Join(link, "export"), link + string(filepath.Separator) + ".." + string(filepath.Separator) + "export"} {
		err := exportHTML(t.Context(), dest, layer, fixedNow())
		ref, ok := errors.AsType[*cli.RefusalError](err)
		if !ok || ref.Code != "html-symlink" {
			t.Fatalf("parent symlink refusal = %v; want html-symlink", err)
		}
	}
	assertExportEmpty(t, target)
}

func assertExportEmpty(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target was modified: %v", entries)
	}
}

// TestHTMLConcurrentAddition preserves files planted at both rename boundaries.
func TestHTMLConcurrentAddition(t *testing.T) {
	for _, boundary := range []int{1, 2} {
		t.Run(strconv.Itoa(boundary), func(t *testing.T) { checkConcurrentExportAddition(t, boundary) })
	}
}

func checkConcurrentExportAddition(t *testing.T, boundary int) {
	t.Helper()
	layer := exportLayer(t)
	dest := filepath.Join(exportTempDir(t), "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	before := readExport(t, filepath.Join(dest, "data.json"))
	seam, planted := exportPlantSeam(dest, boundary)
	err := exportWithRename(t.Context(), dest, layer, fixedNow(), seam)
	ref, ok := errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-not-export" {
		t.Fatalf("concurrent addition refusal = %v; want html-not-export", err)
	}
	if got := string(readExport(t, filepath.Join(*planted, "keep.txt"))); got != "operator file" {
		t.Fatalf("planted file changed: %q", got)
	}
	if !bytes.Equal(before, readExport(t, filepath.Join(*planted, "data.json"))) {
		t.Fatal("old export changed")
	}
}

func exportPlantSeam(dest string, boundary int) (func(string, string) error, *string) {
	calls := 0
	location := dest
	rename := func(from, to string) error {
		calls++
		if err := plantExportAddition(location, calls == boundary); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		if from == location {
			location = to
		}
		return nil
	}
	return rename, &location
}

// TestHTMLModifiedAsset preserves an asset changed after initial validation.
func TestHTMLModifiedAsset(t *testing.T) {
	layer := exportLayer(t)
	dest := filepath.Join(exportTempDir(t), "export")
	if err := exportHTML(t.Context(), dest, layer, fixedNow()); err != nil {
		t.Fatal(err)
	}
	seam := modifiedAssetSeam(dest)
	err := exportWithRename(t.Context(), dest, layer, fixedNow(), seam)
	ref, ok := errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-not-export" {
		t.Fatalf("modified asset refusal = %v; want html-not-export", err)
	}
	if got := string(readExport(t, filepath.Join(dest, "app.css"))); got != "operator style" {
		t.Fatalf("modified asset was lost: %q", got)
	}
}

func plantExportAddition(location string, plant bool) error {
	if !plant {
		return nil
	}
	return os.WriteFile(filepath.Join(location, "keep.txt"), []byte("operator file"), 0o600)
}

func modifiedAssetSeam(dest string) func(string, string) error {
	return func(from, to string) error {
		if from == dest {
			if err := os.WriteFile(filepath.Join(dest, "app.css"), []byte("operator style"), 0o600); err != nil {
				return err
			}
		}
		return os.Rename(from, to)
	}
}
