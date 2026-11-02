// Package release is the one verified-download path shared by `verdict
// upgrade` and the skill bootstrap: fetch a release archive, checksums.txt,
// and the sigstore bundle; verify the bundle with cosign when available
// (loud checksum-only fallback, VERDICT_REQUIRE_SIGNATURE=1 fails closed);
// verify the archive SHA-256; and extract only the expected binary into a
// caller-supplied staging directory.
//
// Every failure leaves the caller's target untouched: the package only
// writes into the download and stage directories it is given.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	contract "github.com/stainless-code/verdict/release"
)

// Download limits and archive names.
const (
	maxAssetBytes     = 128 << 20
	downloadTimeout   = 2 * time.Minute
	checksumHexLength = 64
	binNameUnix       = "verdict"
	binNameWindows    = "verdict.exe"
	licenseName       = "LICENSE"
	readmeName        = "README.md"
	sigVerified       = "verified"
	sigChecksumOnly   = "checksum-only"
	filePermExec      = 0o755
	commandWaitDelay  = 10 * time.Second
	redirectBodyCap   = 4096
	slash             = "/"
)

// refuse codes surfaced to the CLI edge.
const (
	codeFetchFailed     = "download-failed"
	codeVerifyFailed    = "verify-failed"
	codeUnsupportedHost = "unsupported-platform"
)

// Request is one verified fetch. Version must be concrete (latest is
// resolved by [LatestVersion] first). BaseURL is the release download base
// without the version segment (VERDICT_DOWNLOAD_URL semantics).
type Request struct {
	Client           *http.Client
	RunCosign        func(context.Context, string, []string) ([]byte, error)
	Warn             func(string)
	Version          string
	GOOS             string
	GOARCH           string
	BaseURL          string
	DownloadDir      string
	StageDir         string
	Cosign           string
	RequireSignature bool
}

// Result is a verified archive plus the staged binary extracted from it.
type Result struct {
	Version     string
	ArchiveName string
	ArchivePath string
	Binary      string
	SHA256      string
	Signature   string
}

// contractDoc mirrors the frozen release/contract.json subset the verifier
// consumes.
type contractDoc struct { //nolint:govet // contract order documents the artifact table
	Archives        []archiveEntry `json:"archives"`
	Checksums       string         `json:"checksums"`
	SignatureBundle string         `json:"signatureBundle"`
	Repo            string         `json:"repo"`
	Binary          string         `json:"binary"`
	URLs            struct {
		ReleaseBase string `json:"releaseBase"`
	} `json:"urls"`
	Cosign struct {
		Identity string `json:"identity"`
		Issuer   string `json:"issuer"`
	} `json:"cosign"`
}

// archiveEntry is one os/arch row of the contract.
type archiveEntry struct {
	Goos   string `json:"goos"`
	Goarch string `json:"goarch"`
	File   string `json:"file"`
}

// loadContract parses the embedded contract once.
var loadContract = sync.OnceValues(func() (contractDoc, error) {
	var doc contractDoc
	if err := json.Unmarshal(contract.ContractJSON(), &doc); err != nil {
		//nolint:errorlint // malformed embedded asset, not inspected
		return contractDoc{}, fmt.Errorf("release contract: %v", err)
	}
	return doc, nil
})

// DefaultBaseURL is the contract's release download base without the
// version segment (install.sh's REPO_BASE default).
func DefaultBaseURL() (string, error) {
	doc, err := loadContract()
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(doc.URLs.ReleaseBase, "/v{version}")
	if base == doc.URLs.ReleaseBase {
		return "", newErr("release contract: releaseBase lacks the version placeholder")
	}
	return base, nil
}

// defaultLatestURL derives the Releases latest redirect from the contract.
func defaultLatestURL() (string, error) {
	doc, err := loadContract()
	if err != nil {
		return "", err
	}
	return "https://github.com/" + doc.Repo + "/releases/latest", nil
}

// LatestVersion resolves the release tag through the Releases redirect,
// never the rate-limited API. baseURL is empty for the canonical repo; a
// custom base uses its own /latest endpoint.
func LatestVersion(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	latest, err := latestEndpoint(baseURL)
	if err != nil {
		return "", err
	}
	c := *clientOr(client)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latest, nil)
	if err != nil {
		return "", sealed("latest "+latest, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", sealed("latest "+latest, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only response; the status check owns the outcome
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, redirectBodyCap)); err != nil {
		return "", sealed("latest body", err)
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return "", newErrf("latest %s: HTTP %d (want a release redirect)", latest, resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return "", sealed("latest redirect", err)
	}
	version := strings.TrimPrefix(path.Base(loc.Path), "v")
	if !ValidVersion(version) {
		return "", newErrf("could not parse a version from the latest redirect: %s", loc)
	}
	return version, nil
}

// latestEndpoint picks the canonical or custom latest URL.
func latestEndpoint(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return defaultLatestURL()
	}
	base, err := DefaultBaseURL()
	if err != nil {
		return "", err
	}
	if strings.TrimRight(baseURL, "/") == base {
		return defaultLatestURL()
	}
	return strings.TrimRight(baseURL, "/") + "/latest", nil
}

// FetchVerify downloads and verifies one archive and extracts its binary
// into Request.StageDir. The returned refusals carry installer-shaped
// messages ("SHA-256 mismatch", "signature verification failed").
func FetchVerify(ctx context.Context, req *Request) (Result, *core.RefusalError) {
	doc, asset, version, ref := resolveAsset(req)
	if ref != nil {
		return Result{}, ref
	}
	dirs, ref := prepareDirs(req)
	if ref != nil {
		return Result{}, ref
	}
	src, ref := sourceFrom(req, &doc, asset, version, dirs)
	if ref != nil {
		return Result{}, ref
	}
	if ref := downloadAssets(ctx, req, src); ref != nil {
		return Result{}, ref
	}
	return check(ctx, req, &doc, src)
}

// resolveAsset validates the platform/version against the contract.
func resolveAsset(req *Request) (contractDoc, archiveEntry, string, *core.RefusalError) {
	doc, err := loadContract()
	if err != nil {
		return contractDoc{}, archiveEntry{}, "", refuse(codeFetchFailed, err.Error())
	}
	asset, ok := assetFor(&doc, req.GOOS, req.GOARCH)
	if !ok {
		return contractDoc{}, archiveEntry{}, "", refuse(codeUnsupportedHost,
			fmt.Sprintf("unsupported platform %s/%s (supported: darwin/linux/windows, amd64/arm64)", req.GOOS, req.GOARCH))
	}
	version := strings.TrimPrefix(req.Version, "v")
	if !ValidVersion(version) {
		return contractDoc{}, archiveEntry{}, "", refuse(codeFetchFailed, "bad version: "+req.Version+" (use X.Y.Z or vX.Y.Z)")
	}
	return doc, asset, version, nil
}

// dirs carries the two caller-supplied working directories.
type dirs struct {
	download string
	stage    string
}

// prepareDirs creates the download and stage directories.
func prepareDirs(req *Request) (dirs, *core.RefusalError) {
	if req.DownloadDir == "" {
		return dirs{}, refuse(codeFetchFailed, "no download directory")
	}
	d := dirs{download: req.DownloadDir, stage: req.StageDir}
	if d.stage == "" {
		d.stage = d.download
	}
	for _, dir := range []string{d.download, d.stage} {
		if err := os.MkdirAll(dir, filePermExec); err != nil {
			return dirs{}, refuse(codeFetchFailed, sealed("create "+dir, err).Error())
		}
	}
	return d, nil
}

// source is the concrete set of URLs and local paths for one release.
type source struct {
	Version   string
	Archive   string
	Checksums string
	Bundle    string
	BaseURL   string
	BundleOK  bool
}

// sourceFrom joins the contract names onto the download base.
func sourceFrom(req *Request, doc *contractDoc, asset archiveEntry, version string, dirs dirs) (*source, *core.RefusalError) {
	base := strings.TrimRight(req.BaseURL, slash)
	if base == "" {
		var err error
		if base, err = DefaultBaseURL(); err != nil {
			return nil, refuse(codeFetchFailed, err.Error())
		}
	}
	name := strings.ReplaceAll(asset.File, "{version}", version)
	return &source{
		Version:   version,
		Archive:   filepath.Join(dirs.download, name),
		Checksums: filepath.Join(dirs.download, doc.Checksums),
		Bundle:    filepath.Join(dirs.download, doc.SignatureBundle),
		BaseURL:   base + "/v" + version + "/",
	}, nil
}

// downloadAssets fetches the archive, checksums, and (best effort) bundle.
func downloadAssets(ctx context.Context, req *Request, src *source) *core.RefusalError {
	client := clientOr(req.Client)
	if err := download(ctx, client, src.BaseURL+filepath.Base(src.Archive), src.Archive); err != nil {
		return refuse(codeFetchFailed, "download failed: "+err.Error())
	}
	if err := download(ctx, client, src.BaseURL+filepath.Base(src.Checksums), src.Checksums); err != nil {
		return refuse(codeFetchFailed, "download failed: "+err.Error())
	}
	src.BundleOK = download(ctx, client, src.BaseURL+filepath.Base(src.Bundle), src.Bundle) == nil
	return nil
}

// check verifies the signature and checksum, then extracts the binary.
func check(ctx context.Context, req *Request, doc *contractDoc, src *source) (Result, *core.RefusalError) {
	signature, ref := verifySignature(ctx, req, doc, src)
	if ref != nil {
		return Result{}, ref
	}
	name := filepath.Base(src.Archive)
	sum, err := expectedSum(src.Checksums, name)
	if err != nil {
		return Result{}, refuse(codeVerifyFailed, err.Error())
	}
	actual, err := fileSHA256(src.Archive)
	if err != nil {
		return Result{}, refuse(codeVerifyFailed, err.Error())
	}
	if !strings.EqualFold(actual, sum) {
		return Result{}, refuse(codeVerifyFailed,
			fmt.Sprintf("SHA-256 mismatch for %s (expected %s, got %s); nothing installed", name, sum, actual))
	}
	binary := filepath.Join(filepath.Dir(src.Archive), binaryName(req.GOOS))
	extracted, err := extractBinary(src.Archive, filepath.Dir(binary), binaryName(req.GOOS))
	if err != nil {
		_ = os.Remove(binary) // no partial binary survives a refusal
		return Result{}, refuse(codeVerifyFailed, err.Error())
	}
	return Result{
		Version:     src.Version,
		ArchiveName: name,
		ArchivePath: src.Archive,
		Binary:      extracted,
		SHA256:      actual,
		Signature:   signature,
	}, nil
}

// verifySignature applies the installer policy: cosign present means the
// bundle is mandatory and a failed verification refuses; absent cosign
// warns loudly unless VERDICT_REQUIRE_SIGNATURE=1.
func verifySignature(ctx context.Context, req *Request, doc *contractDoc, src *source) (string, *core.RefusalError) {
	cosign := cosignPath(req)
	if cosign == "" {
		return checksumOnly(req)
	}
	if !src.BundleOK {
		return "", refuse(codeVerifyFailed, "cosign is installed but the sigstore bundle is missing; refusing an unverified install")
	}
	args := []string{
		"verify-blob", "--bundle", src.Bundle,
		"--certificate-identity-regexp", identityRegexp(doc.Cosign.Identity),
		"--certificate-oidc-issuer", doc.Cosign.Issuer, src.Checksums,
	}
	out, err := runCosign(ctx, req, cosign, args)
	if err != nil {
		detail := firstLine(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return "", refuse(codeVerifyFailed, "signature verification failed: "+detail)
	}
	return sigVerified, nil
}

// checksumOnly is the loud fallback and the fail-closed requirement.
func checksumOnly(req *Request) (string, *core.RefusalError) {
	if req.RequireSignature {
		return "", refuse(codeVerifyFailed, "VERDICT_REQUIRE_SIGNATURE=1 but cosign is not installed; nothing installed")
	}
	if req.Warn != nil {
		req.Warn("WARNING: cosign not found; verifying SHA-256 only (VERDICT_REQUIRE_SIGNATURE=1 fails closed).")
	}
	return sigChecksumOnly, nil
}

// cosignPath resolves an executable cosign from the override or PATH.
func cosignPath(req *Request) string {
	if req.Cosign != "" {
		if info, err := os.Stat(req.Cosign); err == nil && !info.IsDir() {
			return req.Cosign
		}
	}
	if found, err := exec.LookPath("cosign"); err == nil {
		return found
	}
	return ""
}

// runCosign invokes the verifier, or the injected runner in tests.
func runCosign(ctx context.Context, req *Request, cosign string, args []string) ([]byte, error) {
	if req.RunCosign != nil {
		return req.RunCosign(ctx, cosign, args)
	}
	cmd := exec.CommandContext(ctx, cosign, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.WaitDelay = commandWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return []byte(stderr.String()), sealed("cosign", err)
	}
	return out, nil
}

// identityRegexp expands the contract's tag wildcard into the installer's
// certificate-identity regexp.
func identityRegexp(identity string) string {
	if prefix, ok := strings.CutSuffix(identity, "v*"); ok {
		return "^" + regexp.QuoteMeta(prefix) + `v[0-9]+\.[0-9]+\.[0-9]+.*$`
	}
	return "^" + regexp.QuoteMeta(identity) + "$"
}

// assetFor finds the contract row for the running platform.
func assetFor(doc *contractDoc, goos, goarch string) (archiveEntry, bool) {
	for _, a := range doc.Archives {
		if a.Goos == goos && a.Goarch == goarch {
			return a, true
		}
	}
	return archiveEntry{}, false
}

// binaryName is the archive member the installer extracts per platform.
func binaryName(goos string) string {
	if goos == "windows" {
		return binNameWindows
	}
	return binNameUnix
}

// download fetches rawURL into dst, capping the body at maxAssetBytes.
func download(ctx context.Context, client *http.Client, rawURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return sealed("GET "+rawURL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return sealed("GET "+rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }() // the body's fate is the copy below
	if resp.StatusCode != http.StatusOK {
		return newErrf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, maxAssetBytes+1)
	file, err := os.Create(dst)
	if err != nil {
		return sealed("create "+dst, err)
	}
	written, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil {
		return sealed("write "+dst, copyErr)
	}
	if closeErr != nil {
		return sealed("write "+dst, closeErr)
	}
	if written > maxAssetBytes {
		_ = os.Remove(dst) // oversize cleanup; the size error rules
		return newErrf("%s exceeds the %d-byte download cap", rawURL, maxAssetBytes)
	}
	return nil
}

// expectedSum returns the checksums.txt SHA-256 for name.
func expectedSum(checksumsPath, name string) (string, error) {
	raw, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", sealed("read checksums", err)
	}
	return sumFor(string(raw), name)
}

// sumFor scans one checksums.txt document for name.
func sumFor(contents, name string) (string, error) {
	for line := range strings.SplitSeq(contents, "\n") {
		sum, ok, err := sumFromLine(line, name)
		if err != nil {
			return "", err
		}
		if ok {
			return sum, nil
		}
	}
	return "", newErr("checksums.txt has no entry for " + name)
}

// sumFromLine parses one checksums.txt line for name.
func sumFromLine(line, name string) (string, bool, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || strings.TrimPrefix(fields[1], "*") != name {
		return "", false, nil
	}
	sum := strings.ToLower(fields[0])
	if len(sum) != checksumHexLength || !isHex(sum) {
		return "", false, newErrf("checksums.txt has no valid entry for %s", name)
	}
	return sum, true, nil
}

// fileSHA256 streams the archive hash.
func fileSHA256(archivePath string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", sealed("open "+archivePath, err)
	}
	defer func() { _ = file.Close() }() // read-only handle; the hash error rules
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", sealed("hash "+archivePath, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractBinary opens archivePath (tar.gz or zip) and writes only the
// expected binary into stageDir. Anything but the binary plus LICENSE and
// README.md is refused, so a tampered archive never gets a free pass.
func extractBinary(archivePath, stageDir, binary string) (string, error) {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, stageDir, binary)
	}
	return extractTarGz(archivePath, stageDir, binary)
}

// allowedMember reports whether an archive member is part of the contract.
func allowedMember(name, binary string) bool {
	return name == binary || name == licenseName || name == readmeName
}

// checkMember refuses unexpected or non-regular members.
func checkMember(name, binary string, regular bool) error {
	if !allowedMember(name, binary) {
		return newErrf("archive has unexpected member(s): %s", name)
	}
	if !regular {
		return newErrf("archive member %s is not a regular file", name)
	}
	return nil
}

// extractTarGz extracts the binary from a tar.gz archive.
func extractTarGz(archivePath, stageDir, binary string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", sealed("open archive", err)
	}
	defer func() { _ = file.Close() }() // read-only handle; extraction errors rule
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", sealed("not a valid tar.gz archive", err)
	}
	defer func() { _ = gz.Close() }() // gzip reader has no recovery path; extraction owns the outcome
	dst := filepath.Join(stageDir, binary)
	found, err := walkTar(tar.NewReader(gz), dst, binary)
	if err != nil {
		return "", err
	}
	if !found {
		return "", newErr("archive does not contain the verdict binary")
	}
	return dst, nil
}

// walkTar extracts the binary member while validating the rest.
//
//nolint:revive // the archive walk is the algorithm
func walkTar(tr *tar.Reader, dst, binary string) (bool, error) {
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return found, nil
		}
		if err != nil {
			return false, sealed("read archive", err)
		}
		if err := consumeTarMember(tr, hdr, dst, binary); err != nil {
			return false, err
		}
		if hdr.Name == binary {
			found = true
		}
	}
}

// consumeTarMember validates and extracts one tar member.
func consumeTarMember(tr *tar.Reader, hdr *tar.Header, dst, binary string) error {
	if err := checkMember(hdr.Name, binary, hdr.Typeflag == tar.TypeReg); err != nil {
		return err
	}
	if hdr.Name != binary {
		return nil
	}
	return writeMember(dst, tr)
}

// extractZip extracts the binary from a zip archive (Windows releases).
func extractZip(archivePath, stageDir, binary string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", sealed("not a valid zip archive", err)
	}
	defer func() { _ = zr.Close() }() // read-only archive; extraction owns the outcome
	dst := filepath.Join(stageDir, binary)
	found, err := walkZip(zr.File, dst, binary)
	if err != nil {
		return "", err
	}
	if !found {
		return "", newErr("archive does not contain the verdict binary")
	}
	return dst, nil
}

// walkZip extracts the binary member while validating the rest.
func walkZip(files []*zip.File, dst, binary string) (bool, error) {
	found := false
	for _, member := range files {
		extracted, err := consumeZipMember(member, dst, binary)
		if err != nil {
			return false, err
		}
		if extracted {
			found = true
		}
	}
	return found, nil
}

// consumeZipMember validates and extracts one zip member.
func consumeZipMember(member *zip.File, dst, binary string) (bool, error) {
	if err := checkMember(member.Name, binary, member.Mode().IsRegular()); err != nil {
		return false, err
	}
	if member.Name != binary {
		return false, nil
	}
	src, err := member.Open()
	if err != nil {
		return false, sealed("read archive member", err)
	}
	err = writeMember(dst, src)
	_ = src.Close() // the member read is done; writeMember's error rules
	if err != nil {
		return false, err
	}
	return true, nil
}

// writeMember writes one extracted file 0755, fsyncs, and closes it.
func writeMember(dst string, src io.Reader) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePermExec)
	if err != nil {
		return sealed("create "+dst, err)
	}
	if _, err := io.Copy(file, io.LimitReader(src, maxAssetBytes)); err != nil {
		_ = file.Close() // copy error rules; close adds nothing
		return sealed("extract "+dst, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close() // sync error rules
		return sealed("sync "+dst, err)
	}
	if err := file.Close(); err != nil {
		return sealed("close "+dst, err)
	}
	return nil
}

// ValidVersion matches the installer's accepted version alphabet.
func ValidVersion(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r == '.', r == '+', r == '-':
		default:
			return false
		}
	}
	return true
}

// isHex reports a lowercase hex string.
func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// firstLine returns the first non-empty output line.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// refuse builds the typed refusal.
func refuse(code, message string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: message}
}

// clientOr returns the injected client or a hardened default.
func clientOr(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: downloadTimeout}
}

// newErr builds a locally owned error (GO-ERRORS-03 sealing at the edge).
func newErr(msg string) error {
	return errors.New(msg) //nolint:wrapcheck // locally constructed message, not an external error
}

// newErrf builds a locally owned formatted error.
func newErrf(format string, args ...any) error {
	return fmt.Errorf(format, args...) //nolint:wrapcheck // locally constructed message, not an external error
}

// sealed annotates err without exposing it (GO-ERRORS-03).
func sealed(op string, err error) error {
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	return fmt.Errorf("%s: %v", op, err)
}
