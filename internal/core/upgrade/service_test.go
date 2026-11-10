package upgrade_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/upgrade"
)

func nonRoot() *int {
	uid := 1000
	return &uid
}

func baseRequest(t *testing.T) upgrade.Request {
	t.Helper()
	home := t.TempDir()
	env := map[string]string{"HOME": home, "SHELL": "/bin/zsh", "PATH": "/usr/bin"}
	return upgrade.Request{
		Runtime:  &core.Runtime{Env: env, Now: time.Now},
		EUID:     nonRoot(),
		GOOS:     "linux",
		EnvSlice: []string{"HOME=" + home, "PATH=/usr/bin"},
		Smoke:    func(context.Context, string, string) error { return nil },
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func installerLayout(t *testing.T) (dir, target, receipt string) {
	t.Helper()
	dir = resolvedTempDir(t)
	target = filepath.Join(dir, "verdict")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt = filepath.Join(dir, ".verdict-installer")
	body := "{\"binary\":\"verdict\",\"version\":\"0.1.0\",\"installed_by\":\"install.sh\"}\n"
	if err := os.WriteFile(receipt, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, target, receipt
}

func fakeCosign(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// releaseFiles builds the served archive, checksums, and bundle.
//
//nolint:revive // the archive builder is the fixture
func releaseFiles(t *testing.T, version string, binary []byte, badChecksum bool) map[string][]byte {
	t.Helper()
	name := fmt.Sprintf("verdict_%s_linux_%s.tar.gz", version, runtime.GOARCH)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	members := []struct {
		name string
		data []byte
	}{
		{"verdict", binary},
		{"LICENSE", []byte("MIT\n")},
		{"README.md", []byte("verdict\n")},
	}
	for _, member := range members {
		hdr := &tar.Header{Name: member.name, Mode: 0o755, Size: int64(len(member.data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	if badChecksum {
		line = strings.Repeat("0", 64) + "  " + name + "\n"
	}
	return map[string][]byte{
		name:                          buf.Bytes(),
		"checksums.txt":               []byte(line),
		"checksums.txt.sigstore.json": []byte("{}"),
		"commit.txt":                  []byte("72e7d304d3cb8b85bc6c18eab2e51a50bd38fdad\n"),
	}
}

//
//nolint:revive // the handler switch is the fixture
func releaseServer(t *testing.T, version string, files map[string][]byte, calls *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		http.Redirect(w, r, "/v"+version, http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		name, ok := strings.CutPrefix(r.URL.Path, "/v"+version+"/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data) //nolint:errcheck // test server write; the client asserts the body
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func selfReplaceRequest(t *testing.T, server *httptest.Server, dir, target, version string) upgrade.Request {
	t.Helper()
	req := baseRequest(t)
	req.Executable = target
	req.PinVersion = version
	req.Client = server.Client()
	req.Env["VERDICT_INSTALL"] = dir
	req.Env["VERDICT_DOWNLOAD_URL"] = server.URL
	req.Env["VERDICT_COSIGN"] = fakeCosign(t)
	req.RunCosign = func(context.Context, string, []string) ([]byte, error) { return nil, nil }
	return req
}

// TestSelfReplace proves the verified atomic swap and the receipt update.
//
//nolint:revive // the assertions are the contract
func TestSelfReplace(t *testing.T) {
	dir, target, receipt := installerLayout(t)
	server := releaseServer(t, "1.2.3", releaseFiles(t, "1.2.3", []byte("new-binary"), false), nil)
	req := selfReplaceRequest(t, server, dir, target, "1.2.3")
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Channel != upgrade.ChannelInstaller || resp.Action != upgrade.ActionSelfReplace {
		t.Errorf("response = %+v; want installer self-replace", resp)
	}
	if resp.Version != "1.2.3" || resp.SHA256 == "" {
		t.Errorf("response version/sha = %q/%q", resp.Version, resp.SHA256)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new-binary" {
		t.Errorf("target = %q; want new-binary", raw)
	}
	receiptRaw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(receiptRaw), `"version":"1.2.3"`) || !strings.Contains(string(receiptRaw), `"installed_by":"install.sh"`) {
		t.Errorf("receipt = %q; want the updated version and the preserved installer", receiptRaw)
	}
	assertNoStaging(t, dir)
}

// TestSelfReplaceLatest resolves through /latest with no pin.
func TestSelfReplaceLatest(t *testing.T) {
	dir, target, receipt := installerLayout(t)
	server := releaseServer(t, "2.0.0", releaseFiles(t, "2.0.0", []byte("latest-binary"), false), nil)
	req := selfReplaceRequest(t, server, dir, target, "")
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Version != "2.0.0" {
		t.Errorf("version = %q; want 2.0.0", resp.Version)
	}
	raw := readFile(t, receipt)
	if !strings.Contains(string(raw), `"version":"2.0.0"`) {
		t.Errorf("receipt = %q; want 2.0.0", raw)
	}
}

// TestSelfReplaceSmokeFailureRollsBack proves a bad staged binary never
// replaces the installed one.
func TestSelfReplaceSmokeFailureRollsBack(t *testing.T) {
	dir, target, receipt := installerLayout(t)
	server := releaseServer(t, "1.2.3", releaseFiles(t, "1.2.3", []byte("bad-binary"), false), nil)
	req := selfReplaceRequest(t, server, dir, target, "1.2.3")
	req.Smoke = func(context.Context, string, string) error { return errors.New("smoke exploded") }
	_, ref := upgrade.Run(context.Background(), &req)
	if ref == nil || !strings.Contains(ref.Message, "smoke test") {
		t.Fatalf("refusal = %+v; want a smoke-test refusal", ref)
	}
	raw := readFile(t, target)
	if string(raw) != "old-binary" {
		t.Errorf("target = %q; want the old binary", raw)
	}
	receiptRaw := readFile(t, receipt)
	if !strings.Contains(string(receiptRaw), `"version":"0.1.0"`) {
		t.Errorf("receipt = %q; want the old version", receiptRaw)
	}
	assertNoStaging(t, dir)
}

// TestSelfReplaceChecksumFailureLeavesOld proves verification runs before
// any replacement.
func TestSelfReplaceChecksumFailureLeavesOld(t *testing.T) {
	dir, target, receipt := installerLayout(t)
	server := releaseServer(t, "1.2.3", releaseFiles(t, "1.2.3", []byte("new-binary"), true), nil)
	req := selfReplaceRequest(t, server, dir, target, "1.2.3")
	_, ref := upgrade.Run(context.Background(), &req)
	if ref == nil || !strings.Contains(ref.Message, "SHA-256 mismatch") {
		t.Fatalf("refusal = %+v; want a checksum refusal", ref)
	}
	raw := readFile(t, target)
	if string(raw) != "old-binary" {
		t.Errorf("target = %q; want the old binary", raw)
	}
	receiptRaw := readFile(t, receipt)
	if !strings.Contains(string(receiptRaw), `"version":"0.1.0"`) {
		t.Errorf("receipt = %q; want the old version", receiptRaw)
	}
	assertNoStaging(t, dir)
}

// TestDryRunTouchesNothing proves the plan makes no network call and
// leaves the install alone.
func TestDryRunTouchesNothing(t *testing.T) {
	dir, target, _ := installerLayout(t)
	calls := 0
	server := releaseServer(t, "1.2.3", releaseFiles(t, "1.2.3", []byte("new-binary"), false), &calls)
	req := selfReplaceRequest(t, server, dir, target, "1.2.3")
	req.DryRun = true
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if !resp.DryRun || resp.Action != upgrade.ActionSelfReplace || resp.Version != "1.2.3" {
		t.Errorf("plan = %+v", resp)
	}
	if calls != 0 {
		t.Errorf("dry-run made %d HTTP calls; want 0", calls)
	}
	raw := readFile(t, target)
	if string(raw) != "old-binary" {
		t.Errorf("dry-run changed the target to %q", raw)
	}
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".verdict-stage") || strings.HasPrefix(entry.Name(), "previous-") {
			t.Errorf("staging artifact survived: %s", entry.Name())
		}
	}
}

// TestDetectChannels pins the operator's §6 table.
//
//nolint:revive // the table is the matrix
func TestDetectChannels(t *testing.T) {
	home := t.TempDir()
	tmp := t.TempDir()
	cases := []struct {
		name    string
		exe     string
		env     map[string]string
		want    string
		action  string
		wantCmd string
	}{
		{"npm root", filepath.Join(home, ".npm-global", "lib", "node_modules", "@stainless-code", "verdict", "bin", "verdict"), nil, upgrade.ChannelNPM, upgrade.ActionDelegate, "npm i -g"},
		{"npm marker", filepath.Join(tmp, "verdict"), map[string]string{"VERDICT_NPM_MANAGED": "1"}, upgrade.ChannelNPM, upgrade.ActionDelegate, "npm i -g"},
		{"pnpm root", filepath.Join(home, ".local", "share", "pnpm", "global", "5", "node_modules", "verdict", "bin", "verdict"), nil, upgrade.ChannelPNPM, upgrade.ActionDelegate, "pnpm add -g"},
		{"bun root", filepath.Join(home, ".bun", "install", "global", "node_modules", "verdict", "bin", "verdict"), nil, upgrade.ChannelBun, upgrade.ActionDelegate, "bun add -g"},
		{"yarn root", filepath.Join(home, ".config", "yarn", "global", "node_modules", "verdict", "bin", "verdict"), nil, upgrade.ChannelYarn, upgrade.ActionDelegate, "yarn global add"},
		{"brew cellar", "/opt/homebrew/Cellar/verdict/1.2.3/bin/verdict", nil, upgrade.ChannelBrew, upgrade.ActionDelegate, "brew upgrade stainless-code/tap/verdict"},
		{"brew marker", filepath.Join(tmp, "verdict"), map[string]string{"VERDICT_MANAGED": "1"}, upgrade.ChannelBrew, upgrade.ActionDelegate, "brew upgrade stainless-code/tap/verdict"},
		{"project-local", filepath.Join(home, "proj", "node_modules", "@stainless-code", "verdict", "bin", "verdict"), nil, upgrade.ChannelProject, upgrade.ActionRefuse, "npm install @stainless-code/verdict@latest"},
		{"skill copy", filepath.Join(home, ".agents", "skills", "verdict", "bin", "verdict"), nil, upgrade.ChannelSkill, upgrade.ActionRefuse, "npx skills update"},
		{"unknown", filepath.Join(tmp, "bin", "verdict"), nil, upgrade.ChannelUnknown, upgrade.ActionRefuse, "install.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest(t)
			req.Env["HOME"] = home
			req.Executable = tc.exe
			req.DryRun = true
			maps.Copy(req.Env, tc.env)
			resp, ref := upgrade.Run(context.Background(), &req)
			if ref != nil {
				t.Fatalf("dry-run refused: %+v", ref)
			}
			if resp.Channel != tc.want || resp.Action != tc.action {
				t.Errorf("response = %s/%s; want %s/%s", resp.Channel, resp.Action, tc.want, tc.action)
			}
			if !strings.Contains(strings.Join(resp.Command, " "), tc.wantCmd) {
				t.Errorf("command = %v; want %q", resp.Command, tc.wantCmd)
			}
		})
	}
}

// TestDetectInstaller proves the receipt-gated installer channel.
func TestDetectInstaller(t *testing.T) {
	dir, target, _ := installerLayout(t)
	req := baseRequest(t)
	req.Executable = target
	req.DryRun = true
	req.Env["VERDICT_INSTALL"] = dir
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("dry-run refused: %+v", ref)
	}
	if resp.Channel != upgrade.ChannelInstaller || resp.Action != upgrade.ActionSelfReplace {
		t.Fatalf("response = %+v; want installer self-replace", resp)
	}
}

// TestDetectInstallerWithoutEnvVar proves an install made with VERDICT_INSTALL
// stays installer-managed once the variable is gone: the receipt beside the
// running binary is the fact, not the environment. Before the directory was
// adopted from the executable, this reported `unknown` and refused.
func TestDetectInstallerWithoutEnvVar(t *testing.T) {
	dir, target, _ := installerLayout(t)
	req := baseRequest(t)
	req.Executable = target
	req.DryRun = true
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("dry-run refused: %+v", ref)
	}
	if resp.Channel != upgrade.ChannelInstaller || resp.Action != upgrade.ActionSelfReplace {
		t.Fatalf("response = %s/%s; want installer-managed/self-replace", resp.Channel, resp.Action)
	}
	if resp.Reason != "" {
		t.Errorf("reason = %q; want an unremarked installer plan", resp.Reason)
	}
	if resp.Target != target {
		t.Errorf("target = %q; want %q", resp.Target, target)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 2 {
		t.Errorf("dry-run changed %s: %v", dir, entries)
	}
}

// TestSelfReplaceWithoutEnvVar proves the derived directory is the one the
// upgrade stages in and writes to, not the default ~/.verdict/bin.
func TestSelfReplaceWithoutEnvVar(t *testing.T) {
	dir, target, receipt := installerLayout(t)
	server := releaseServer(t, "1.2.3", releaseFiles(t, "1.2.3", []byte("new-binary"), false), nil)
	req := selfReplaceRequest(t, server, dir, target, "1.2.3")
	delete(req.Env, "VERDICT_INSTALL")
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Channel != upgrade.ChannelInstaller || resp.Action != upgrade.ActionSelfReplace {
		t.Fatalf("response = %s/%s; want installer-managed/self-replace", resp.Channel, resp.Action)
	}
	if raw := readFile(t, target); string(raw) != "new-binary" {
		t.Errorf("target = %q; want new-binary", raw)
	}
	if raw := readFile(t, receipt); !strings.Contains(string(raw), `"version":"1.2.3"`) {
		t.Errorf("receipt = %q; want 1.2.3", raw)
	}
	if _, err := os.Stat(filepath.Join(req.Env["HOME"], ".verdict")); !os.IsNotExist(err) {
		t.Errorf("the default install dir was touched: %v", err)
	}
	assertNoStaging(t, dir)
}

// TestAdoptInstallerDirRefuses proves the fallback never widens the channel:
// only a receipt that names the running file, in its own directory, counts.
func TestAdoptInstallerDirRefuses(t *testing.T) {
	cases := []struct {
		prepare func(t *testing.T, dir string) string
		name    string
	}{
		{name: "no receipt", prepare: writeForeignBinary},
		{name: "receipt in a parent dir", prepare: writeOrphanReceipt},
		{name: "receipt names another file", prepare: writeRenamedCopy},
		{name: "receipt from a foreign installer", prepare: writeForeignInstallerReceipt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUnknownChannel(t, tc.prepare(t, resolvedTempDir(t)))
		})
	}
}

func assertUnknownChannel(t *testing.T, target string) {
	t.Helper()
	req := baseRequest(t)
	req.Executable = target
	req.DryRun = true
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("dry-run refused: %+v", ref)
	}
	if resp.Channel != upgrade.ChannelUnknown || resp.Action != upgrade.ActionRefuse {
		t.Fatalf("response = %s/%s; want unknown/refuse for %s", resp.Channel, resp.Action, target)
	}
}

// TestAdoptInstallerDirKeepsOwner proves the receipt is a hint, not an
// authority: a valid receipt beside a binary inside a directory a package
// manager, Homebrew, a project's node_modules or a skill copy owns leaves that
// channel in place. Self-replacing there would write over files the owner
// tracks (and `uninstall` would delete them), which is exactly what the
// channel table promises never happens.
func TestAdoptInstallerDirKeepsOwner(t *testing.T) {
	home := resolvedTempDir(t)
	cases := []struct {
		name    string
		exe     string
		action  string
		channel string
	}{
		{
			name:    "npm global root",
			channel: upgrade.ChannelNPM,
			action:  upgrade.ActionDelegate,
			exe: filepath.Join(home, ".npm-global", "lib", "node_modules", "@stainless-code",
				"verdict", "bin", "platforms", "linux-amd64", "verdict"),
		},
		{
			name:    "homebrew cellar",
			channel: upgrade.ChannelBrew,
			action:  upgrade.ActionDelegate,
			exe:     filepath.Join(home, "homebrew", "Cellar", "verdict", "0.4.0", "bin", "verdict"),
		},
		{
			name:    "project-local",
			channel: upgrade.ChannelProject,
			action:  upgrade.ActionRefuse,
			exe:     filepath.Join(home, "proj", "node_modules", ".bin", "verdict"),
		},
		{
			name:    "skill copy",
			channel: upgrade.ChannelSkill,
			action:  upgrade.ActionRefuse,
			exe:     filepath.Join(home, ".agents", "skills", "verdict", "bin", "verdict"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertOwnerChannel(t, home, tc.exe, tc.channel, tc.action)
		})
	}
}

func assertOwnerChannel(t *testing.T, home, exe, channel, action string) {
	t.Helper()
	dir := filepath.Dir(exe)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := writeBinary(t, dir, filepath.Base(exe), "managed-elsewhere")
	writeReceipt(t, dir, receiptBodyFor(filepath.Base(exe), "install.sh"))
	req := baseRequest(t)
	req.Env["HOME"] = home
	req.Executable = target
	req.DryRun = true
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("dry-run refused: %+v", ref)
	}
	if resp.Channel != channel || resp.Action != action {
		t.Fatalf("response = %s/%s; want %s/%s for %s", resp.Channel, resp.Action, channel, action, target)
	}
}

func receiptBodyFor(binary, installedBy string) string {
	return `{"binary":"` + binary + `","version":"0.1.0","installed_by":"` + installedBy + `"}` + "\n"
}

func writeBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReceipt(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".verdict-installer"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeForeignBinary(t *testing.T, dir string) string {
	t.Helper()
	return writeBinary(t, dir, "verdict", "foreign")
}

func writeOrphanReceipt(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, receiptBodyFor("verdict", "install.sh"))
	return writeBinary(t, bin, "verdict", "copied")
}

func writeRenamedCopy(t *testing.T, dir string) string {
	t.Helper()
	installerLayoutNamed(t, dir)
	return writeBinary(t, dir, "verdict-copy", "copied")
}

func writeForeignInstallerReceipt(t *testing.T, dir string) string {
	t.Helper()
	writeReceipt(t, dir, receiptBodyFor("verdict", "manual"))
	return writeBinary(t, dir, "verdict", "copied")
}

func installerLayoutNamed(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	target := writeBinary(t, dir, "verdict", "old-binary")
	writeReceipt(t, dir, receiptBodyFor("verdict", "install.sh"))
	return dir, target, filepath.Join(dir, ".verdict-installer")
}

// TestOverrideAndSafety covers the test override plus privilege refusals.
//
//nolint:revive // the subtests are the matrix
func TestOverrideAndSafety(t *testing.T) {
	t.Run("override npm", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.DryRun = true
		req.Env["VERDICT_INSTALL_METHOD"] = "npm"
		resp, ref := upgrade.Run(context.Background(), &req)
		if ref != nil || resp.Channel != upgrade.ChannelNPM {
			t.Fatalf("override response %+v ref %+v", resp, ref)
		}
	})
	t.Run("bad override", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.Env["VERDICT_INSTALL_METHOD"] = "bogus"
		_, ref := upgrade.Run(context.Background(), &req)
		if ref == nil || ref.Code != "upgrade-bad-method" {
			t.Fatalf("refusal = %+v; want upgrade-bad-method", ref)
		}
	})
	t.Run("root refuses", func(t *testing.T) {
		dir, target, _ := installerLayout(t)
		req := baseRequest(t)
		req.Executable = target
		root := 0
		req.EUID = &root
		req.Env["VERDICT_INSTALL"] = dir
		_, ref := upgrade.Run(context.Background(), &req)
		if ref == nil || ref.Code != "upgrade-needs-privilege" {
			t.Fatalf("refusal = %+v; want upgrade-needs-privilege", ref)
		}
	})
	t.Run("installer override needs receipt", func(t *testing.T) {
		dir := resolvedTempDir(t)
		req := baseRequest(t)
		req.Executable = filepath.Join(dir, "verdict")
		req.DryRun = true
		req.Env["VERDICT_INSTALL"] = dir
		req.Env["VERDICT_INSTALL_METHOD"] = "installer-managed"
		resp, ref := upgrade.Run(context.Background(), &req)
		if ref != nil {
			t.Fatalf("dry-run refused: %+v", ref)
		}
		if resp.Action != upgrade.ActionRefuse || !strings.Contains(resp.Reason, "no installer receipt") {
			t.Fatalf("plan = %+v; want a no-receipt refusal", resp)
		}
	})
}

// TestDelegate proves the manager command, streaming, and exit propagation.
func TestDelegate(t *testing.T) {
	req := baseRequest(t)
	req.Executable = filepath.Join(t.TempDir(), "verdict")
	req.Env["VERDICT_INSTALL_METHOD"] = "npm"
	var gotArgv []string
	var out bytes.Buffer
	req.Stdout, req.Stderr = &out, &out
	req.LookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	req.Exec = func(_ context.Context, argv, _ []string, stdout, _ io.Writer) (int, error) {
		gotArgv = argv
		fmt.Fprint(stdout, "manager says hi\n")
		return 0, nil
	}
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if strings.Join(gotArgv, " ") != "/fake/npm i -g @stainless-code/verdict@latest" {
		t.Errorf("argv = %v", gotArgv)
	}
	if strings.Join(resp.Command, " ") != "npm i -g @stainless-code/verdict@latest" {
		t.Errorf("response command = %v", resp.Command)
	}
	if !strings.Contains(out.String(), "manager says hi") {
		t.Errorf("manager output was not streamed: %q", out.String())
	}
}

// TestDelegatePinAndFailures covers the pin, missing manager, and failure.
//
//nolint:revive // the subtests are the matrix
func TestDelegatePinAndFailures(t *testing.T) {
	t.Run("pin", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.PinVersion = "v3.0.0"
		req.Env["VERDICT_INSTALL_METHOD"] = "yarn"
		req.LookPath = func(string) (string, error) { return "/fake/yarn", nil }
		req.Exec = func(context.Context, []string, []string, io.Writer, io.Writer) (int, error) { return 0, nil }
		resp, ref := upgrade.Run(context.Background(), &req)
		if ref != nil || strings.Join(resp.Command, " ") != "yarn global add @stainless-code/verdict@3.0.0" {
			t.Fatalf("pin response %+v ref %+v", resp, ref)
		}
	})
	t.Run("missing manager", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.Env["VERDICT_INSTALL_METHOD"] = "npm"
		req.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		_, ref := upgrade.Run(context.Background(), &req)
		if ref == nil || ref.Code != "manager-missing" {
			t.Fatalf("refusal = %+v; want manager-missing", ref)
		}
	})
	t.Run("failure propagates exit code", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.Env["VERDICT_INSTALL_METHOD"] = "pnpm"
		req.LookPath = func(string) (string, error) { return "/fake/pnpm", nil }
		req.Exec = func(context.Context, []string, []string, io.Writer, io.Writer) (int, error) { return 7, nil }
		resp, ref := upgrade.Run(context.Background(), &req)
		if ref == nil || ref.Code != "manager-failed" || resp.ExitCode != 7 {
			t.Fatalf("refusal %+v exit %d; want manager-failed/7", ref, resp.ExitCode)
		}
	})
	t.Run("brew pin refuses", func(t *testing.T) {
		req := baseRequest(t)
		req.Executable = filepath.Join(t.TempDir(), "verdict")
		req.PinVersion = "1.2.3"
		req.Env["VERDICT_INSTALL_METHOD"] = "brew"
		_, ref := upgrade.Run(context.Background(), &req)
		if ref == nil || ref.Code != "upgrade-version-unsupported" {
			t.Fatalf("refusal = %+v; want upgrade-version-unsupported", ref)
		}
	})
}

// TestDelegateDryRun touches no manager.
func TestDelegateDryRun(t *testing.T) {
	req := baseRequest(t)
	req.Executable = filepath.Join(t.TempDir(), "verdict")
	req.Env["VERDICT_INSTALL_METHOD"] = "npm"
	req.DryRun = true
	req.Exec = func(context.Context, []string, []string, io.Writer, io.Writer) (int, error) {
		t.Fatal("dry-run executed the manager")
		return 0, nil
	}
	resp, ref := upgrade.Run(context.Background(), &req)
	if ref != nil || len(resp.Command) == 0 || !resp.DryRun {
		t.Fatalf("plan = %+v ref %+v", resp, ref)
	}
}

// TestUninstall proves receipt-gated removal and the PATH marker.
//
//nolint:cyclop,gocognit,revive // the subtests are the matrix
func TestUninstall(t *testing.T) {
	t.Run("removes managed files and marker", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows PATH removal is covered by the PowerShell path, not this test")
		}
		dir, target, receipt := installerLayout(t)
		home := t.TempDir()
		profile := filepath.Join(home, ".zshrc")
		body := "export KEEP_ME=1\nexport PATH=\"" + dir + ":$PATH\" # verdict installer\n"
		if err := os.WriteFile(profile, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		req := baseRequest(t)
		req.Executable = target
		req.RemovePath = true
		req.Env["VERDICT_INSTALL"] = dir
		req.Env["HOME"] = home
		resp, ref := upgrade.Uninstall(context.Background(), &req)
		if ref != nil {
			t.Fatalf("Uninstall refused: %+v", ref)
		}
		if !resp.PathLineRemoved {
			t.Error("PathLineRemoved = false; want true")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("binary still exists: %v", err)
		}
		if _, err := os.Stat(receipt); !os.IsNotExist(err) {
			t.Errorf("receipt still exists: %v", err)
		}
		raw := readFile(t, profile)
		if strings.Contains(string(raw), "# verdict installer") || !strings.Contains(string(raw), "KEEP_ME") {
			t.Errorf("profile = %q; want the marker gone and KEEP_ME kept", raw)
		}
	})
	t.Run("dry-run touches nothing", func(t *testing.T) {
		dir, target, receipt := installerLayout(t)
		req := baseRequest(t)
		req.Executable = target
		req.DryRun = true
		req.Env["VERDICT_INSTALL"] = dir
		resp, ref := upgrade.Uninstall(context.Background(), &req)
		if ref != nil || !resp.DryRun || len(resp.Removed) != 2 {
			t.Fatalf("plan = %+v ref %+v", resp, ref)
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("dry-run removed the binary: %v", err)
		}
		if _, err := os.Stat(receipt); err != nil {
			t.Errorf("dry-run removed the receipt: %v", err)
		}
	})
	t.Run("no receipt refuses", func(t *testing.T) {
		dir := resolvedTempDir(t)
		target := filepath.Join(dir, "verdict")
		if err := os.WriteFile(target, []byte("foreign"), 0o755); err != nil {
			t.Fatal(err)
		}
		req := baseRequest(t)
		req.Executable = target
		req.Env["VERDICT_INSTALL"] = dir
		_, ref := upgrade.Uninstall(context.Background(), &req)
		if ref == nil || ref.Code != "uninstall-no-receipt" {
			t.Fatalf("refusal = %+v; want uninstall-no-receipt", ref)
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("foreign binary was removed: %v", err)
		}
	})
	for _, tc := range []struct {
		channel string
		want    string
	}{
		{"npm", "npm uninstall -g @stainless-code/verdict"},
		{"pnpm", "pnpm remove -g @stainless-code/verdict"},
		{"bun", "bun remove -g @stainless-code/verdict"},
		{"yarn", "yarn global remove @stainless-code/verdict"},
		{"brew", "brew uninstall stainless-code/tap/verdict"},
		{"project-local", "npm uninstall @stainless-code/verdict"},
		{"skill", "verdict skill uninstall"},
	} {
		t.Run("refuses "+tc.channel, func(t *testing.T) {
			req := baseRequest(t)
			req.Executable = filepath.Join(t.TempDir(), "node_modules", "verdict", "bin", "verdict")
			req.Env["VERDICT_INSTALL_METHOD"] = tc.channel
			_, ref := upgrade.Uninstall(context.Background(), &req)
			if ref == nil || ref.Code != "uninstall-foreign" || !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("refusal = %+v; want uninstall-foreign containing %q", ref, tc.want)
			}
		})
	}
}

// TestUninstallSkillHintNamesOnlyTheResolvedRoot pins the skill-copy hint: the
// single-target `skill uninstall` verb is named only when its fixed target is
// the detected skill root, and a sibling skill dir gets a hint that cannot
// resolve to it.
func TestUninstallSkillHintNamesOnlyTheResolvedRoot(t *testing.T) {
	t.Parallel()
	home := resolvedTempDir(t)
	skills := filepath.Join(home, ".agents", "skills")
	cases := []struct {
		name string
		exe  string
		want string
	}{
		{
			name: "verdict root",
			exe:  filepath.Join(skills, "verdict", "bin", "verdict"),
			want: "verdict skill uninstall --dir " + skills + " --force",
		},
		{
			name: "sibling root",
			exe:  filepath.Join(skills, "other", "bin", "verdict"),
			want: "the tool that installed the skill copy at " + filepath.Join(skills, "other"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := baseRequest(t)
			req.Env["HOME"] = home
			req.Executable = tc.exe
			_, ref := upgrade.Uninstall(context.Background(), &req)
			if ref == nil || ref.Code != "uninstall-foreign" {
				t.Fatalf("refusal = %+v, want uninstall-foreign", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
			if tc.name == "sibling root" && strings.Contains(ref.Message, "--force") {
				t.Fatalf("message = %q, want no command that can reach a sibling", ref.Message)
			}
		})
	}
}
