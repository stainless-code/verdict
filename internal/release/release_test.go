package release_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/release"
)

// newReleaseServer serves /v<version>/<file> plus /latest redirecting to
// the same version.
func newReleaseServer(t *testing.T, version string, files map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v"+version, http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

// archiveName renders the contract archive name.
func archiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("verdict_%s_%s_%s.%s", version, goos, goarch, ext)
}

// buildArchive builds a release archive for goos with the contract
// members plus the given binary bytes.
//
//nolint:gocognit,revive // fixture builder mirrors the archive layout member by member
func buildArchive(t *testing.T, goos string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	members := []struct {
		name string
		data []byte
	}{
		{releaseBinaryName(goos), binary},
		{"LICENSE", []byte("MIT\n")},
		{"README.md", []byte("verdict\n")},
	}
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for _, member := range members {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: member.name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(member.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
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
	return buf.Bytes()
}

// releaseBinaryName is the contract member name per platform.
func releaseBinaryName(goos string) string {
	if goos == "windows" {
		return "verdict.exe"
	}
	return "verdict"
}

// releaseCommitSHA is the build commit of the recorded v0.4.0 release.
const releaseCommitSHA = "72e7d304d3cb8b85bc6c18eab2e51a50bd38fdad"

// releaseFixture builds the served file map: archive, checksums, bundle,
// and the build commit.
func releaseFixture(t *testing.T, version string, mutators ...func(map[string][]byte)) map[string][]byte {
	t.Helper()
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	name := archiveName(version, goos, goarch)
	archive := buildArchive(t, goos, []byte("new-binary"))
	sum := sha256.Sum256(archive)
	files := map[string][]byte{
		name:                          archive,
		"checksums.txt":               []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)),
		"checksums.txt.sigstore.json": []byte("{}"),
		"commit.txt":                  []byte(releaseCommitSHA + "\n"),
	}
	for _, mutate := range mutators {
		if mutate != nil {
			mutate(files)
		}
	}
	return files
}

// request builds a hermetic verifier request under t.TempDir.
func request(t *testing.T, server *httptest.Server, version string) release.Request {
	t.Helper()
	dir := t.TempDir()
	return release.Request{
		Version:     version,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		BaseURL:     server.URL,
		DownloadDir: filepath.Join(dir, "download"),
		StageDir:    filepath.Join(dir, "stage"),
		Warn:        func(string) {},
	}
}

// fakeExecutable creates an existing file usable as the cosign path.
func fakeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDefaultBaseURLShape pins the contract-derived base.
//
//nolint:revive // the nested platform loop is the assertion
func TestDefaultBaseURLShape(t *testing.T) {
	t.Parallel()
	base, err := release.DefaultBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(base, "/releases/download") {
		t.Fatalf("DefaultBaseURL = %q; want a /releases/download suffix", base)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			name := archiveName("1.2.3", goos, goarch)
			if !strings.HasPrefix(name, "verdict_1.2.3_") {
				t.Fatalf("archive name %q drifted", name)
			}
		}
	}
}

// TestFetchVerifyChecksumOnly proves the loud checksum-only fallback and
// the extracted staged binary.
func TestFetchVerifyChecksumOnly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	version := "1.2.3"
	server := newReleaseServer(t, version, releaseFixture(t, version))
	var warns []string
	req := request(t, server, version)
	req.Cosign = ""
	req.Warn = func(msg string) { warns = append(warns, msg) }
	result, ref := release.FetchVerify(context.Background(), &req)
	if ref != nil {
		t.Fatalf("FetchVerify refused: %+v", ref)
	}
	if result.Signature != "checksum-only" {
		t.Errorf("signature = %q; want checksum-only", result.Signature)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "WARNING: cosign not found") {
		t.Errorf("warnings = %v; want the loud checksum-only warning", warns)
	}
	raw, err := os.ReadFile(result.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new-binary" {
		t.Errorf("staged binary = %q; want new-binary", raw)
	}
	if result.SHA256 == "" {
		t.Error("SHA256 is empty")
	}
}

// TestFetchVerifySignature proves cosign argv and the verified marker.
func TestFetchVerifySignature(t *testing.T) {
	version := "1.2.3"
	server := newReleaseServer(t, version, releaseFixture(t, version))
	var argv []string
	req := request(t, server, version)
	req.Cosign = fakeExecutable(t)
	req.RunCosign = func(_ context.Context, path string, args []string) ([]byte, error) {
		argv = append([]string{path}, args...)
		return nil, nil
	}
	result, ref := release.FetchVerify(context.Background(), &req)
	if ref != nil {
		t.Fatalf("FetchVerify refused: %+v", ref)
	}
	if result.Signature != "verified" {
		t.Errorf("signature = %q; want verified", result.Signature)
	}
	policy := contractCosign(t)
	exact := "--certificate-identity-regexp " + policy.IdentityRegexp + " --certificate-oidc-issuer " + policy.Issuer
	pinned := "--certificate-github-workflow-sha " + releaseCommitSHA
	joined := strings.Join(argv, " ")
	for _, want := range []string{"verify-blob", "--bundle", "checksums.txt", exact, pinned} {
		if !strings.Contains(joined, want) {
			t.Errorf("cosign argv %q misses %q", joined, want)
		}
	}
}

// TestFetchVerifyRefusals covers the fail-closed verification matrix.
//
//nolint:gocognit,revive // the table is the matrix; splitting it hides the cases
func TestFetchVerifyRefusals(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	version := "1.2.3"
	cases := []struct {
		name    string
		mutate  func(map[string][]byte)
		prepare func(*release.Request)
		code    string
		want    string
	}{
		{"checksum mismatch", func(files map[string][]byte) {
			files["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  " + archiveName(version, runtime.GOOS, runtime.GOARCH) + "\n")
		}, nil, "verify-failed", "SHA-256 mismatch"},
		{"missing checksum entry", func(files map[string][]byte) {
			files["checksums.txt"] = []byte("")
		}, nil, "verify-failed", "no entry"},
		{"signature failed", nil, func(req *release.Request) {
			req.Cosign = fakeExecutable(t)
			req.RunCosign = func(context.Context, string, []string) ([]byte, error) {
				return []byte("bad signature"), errors.New("exit 1")
			}
		}, "verify-failed", "signature verification failed"},
		{"cosign without bundle", func(files map[string][]byte) {
			delete(files, "checksums.txt.sigstore.json")
		}, func(req *release.Request) {
			req.Cosign = fakeExecutable(t)
		}, "verify-failed", "sigstore bundle is missing"},
		{"release without build commit", func(files map[string][]byte) {
			delete(files, "commit.txt")
		}, nil, "verify-failed", "does not publish its build commit"},
		{"build commit not a SHA", func(files map[string][]byte) {
			files["commit.txt"] = []byte("main\n")
		}, nil, "verify-failed", "is not a full 40-character commit SHA"},
		{"abbreviated build commit", func(files map[string][]byte) {
			files["commit.txt"] = []byte(releaseCommitSHA[:12] + "\n")
		}, nil, "verify-failed", "is not a full 40-character commit SHA"},
		{"require signature without cosign", nil, func(req *release.Request) {
			req.RequireSignature = true
		}, "verify-failed", "VERDICT_REQUIRE_SIGNATURE=1"},
		{"unexpected archive member", func(files map[string][]byte) {
			name := archiveName(version, runtime.GOOS, runtime.GOARCH)
			tampered := buildArchive(t, runtime.GOOS, []byte("new-binary"))
			tampered = addUnexpectedMember(t, tampered, runtime.GOOS)
			files[name] = tampered
			sum := sha256.Sum256(tampered)
			files["checksums.txt"] = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name))
		}, nil, "verify-failed", "unexpected member"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := releaseFixture(t, version, tc.mutate)
			server := newReleaseServer(t, version, files)
			req := request(t, server, version)
			req.Cosign = ""
			if tc.prepare != nil {
				tc.prepare(&req)
			}
			_, ref := release.FetchVerify(context.Background(), &req)
			if ref == nil {
				t.Fatal("FetchVerify succeeded; want a refusal")
			}
			if ref.Code != tc.code || !strings.Contains(ref.Message, tc.want) {
				t.Errorf("refusal = %+v; want %s containing %q", ref, tc.code, tc.want)
			}
			if _, err := os.Stat(req.StageDir); err == nil {
				entries, readErr := os.ReadDir(req.StageDir)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Errorf("refusal left staged files: %v", entries)
				}
			}
		})
	}
}

// addUnexpectedMember appends a member that is not in the contract.
func addUnexpectedMember(t *testing.T, archive []byte, goos string) []byte {
	t.Helper()
	if goos == "windows" {
		return appendZipMember(t, archive, "../escape")
	}
	return appendTarMember(t, archive, "../escape")
}

// appendTarMember appends one tar.gz member.
//
//nolint:revive // fixture builder mirrors the archive layout member by member
func appendTarMember(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	var files [][2]string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, [2]string{hdr.Name, string(data)})
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, file := range files {
		if err := tw.WriteHeader(&tar.Header{Name: file[0], Mode: 0o755, Size: int64(len(file[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// appendZipMember appends one zip member.
//
//nolint:revive // fixture builder mirrors the archive layout member by member
func appendZipMember(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLatestVersion proves the redirect resolution, never the API.
func TestLatestVersion(t *testing.T) {
	server := newReleaseServer(t, "2.0.0", releaseFixture(t, "2.0.0"))
	version, err := release.LatestVersion(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.0.0" {
		t.Fatalf("LatestVersion = %q; want 2.0.0", version)
	}
}

// TestFetchVerifyUnsupportedPlatform proves the matrix refusal.
func TestFetchVerifyUnsupportedPlatform(t *testing.T) {
	req := release.Request{Version: "1.2.3", GOOS: "plan9", GOARCH: "mips", DownloadDir: t.TempDir()}
	_, ref := release.FetchVerify(context.Background(), &req)
	if ref == nil || ref.Code != "unsupported-platform" {
		t.Fatalf("refusal = %+v; want unsupported-platform", ref)
	}
}

// TestFetchVerifyBadVersion proves the pin alphabet.
func TestFetchVerifyBadVersion(t *testing.T) {
	req := release.Request{Version: "../evil", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, DownloadDir: t.TempDir()}
	_, ref := release.FetchVerify(context.Background(), &req)
	if ref == nil || !strings.Contains(ref.Message, "bad version") {
		t.Fatalf("refusal = %+v; want bad version", ref)
	}
}
