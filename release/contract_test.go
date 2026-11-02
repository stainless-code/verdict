// Package release_test pins the release artifact contract: the frozen
// naming in release/contract.json, the GoReleaser config that must honor
// it, and the npm/formula generators plus the wrapper shim that consume it.
package release_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// archiveEntry is one frozen os/arch row of the contract.
type archiveEntry struct {
	Goos       string `json:"goos"`
	Goarch     string `json:"goarch"`
	File       string `json:"file"`
	NPMPackage string `json:"npmPackage"`
	NPMOS      string `json:"npmOS"`
	NPMCPU     string `json:"npmCPU"`
	NPMBin     string `json:"npmBin"`
}

// contractDoc mirrors release/contract.json. Field order is the GC-optimal
// layout (govet fieldalignment): larger pointerful sections first.
type contractDoc struct {
	Formula struct {
		Tap        string `json:"tap"`
		Path       string `json:"path"`
		Class      string `json:"class"`
		Desc       string `json:"desc"`
		Homepage   string `json:"homepage"`
		License    string `json:"license"`
		InstallBin string `json:"installBin"`
	} `json:"formula"`
	URLs struct {
		ReleaseBase     string `json:"releaseBase"`
		Archive         string `json:"archive"`
		Checksums       string `json:"checksums"`
		SignatureBundle string `json:"signatureBundle"`
	} `json:"urls"`
	Cosign struct {
		Identity string `json:"identity"`
		Issuer   string `json:"issuer"`
	} `json:"cosign"`
	Project         string `json:"project"`
	Repo            string `json:"repo"`
	TagPrefix       string `json:"tagPrefix"`
	Binary          string `json:"binary"`
	Checksums       string `json:"checksums"`
	SignatureBundle string `json:"signatureBundle"`
	SBOMSuffix      string `json:"sbomSuffix"`
	NPM             struct {
		Wrapper      string            `json:"wrapper"`
		Bin          map[string]string `json:"bin"`
		WrapperFiles []string          `json:"wrapperFiles"`
		PublishArgs  []string          `json:"publishArgs"`
	} `json:"npm"`
	Archives []archiveEntry `json:"archives"`
}

// loadContract reads release/contract.json (the test cwd is the package dir).
func loadContract(t *testing.T) contractDoc {
	t.Helper()
	raw, err := os.ReadFile("contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc contractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var header struct {
		Contract int `json:"contract"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if header.Contract != 1 {
		t.Fatalf("contract = %d; want 1", header.Contract)
	}
	return doc
}

// repoFile reads a repo-root-relative file.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// nodeBin returns the node binary or skips when node is unavailable.
func nodeBin(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	return bin
}

// runNode runs node with args, returning combined output and exit code.
func runNode(t *testing.T, extraEnv []string, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v\n%s", name, err, out)
	}
	return string(out), code
}

// toJSON marshals for test comparisons, failing on error.
func toJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// stageBins writes one fake executable per contract archive.
func stageBins(t *testing.T, doc *contractDoc, dir string) {
	t.Helper()
	for _, entry := range doc.Archives {
		ext := ""
		if strings.HasSuffix(entry.NPMBin, ".exe") {
			ext = ".exe"
		}
		name := "verdict-" + entry.Goos + "-" + entry.Goarch + ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// contractArchiveName renders the frozen file name for one row.
func contractArchiveName(entry *archiveEntry) string {
	ext := ".tar.gz"
	if entry.Goos == "windows" {
		ext = ".zip"
	}
	return "verdict_{version}_" + entry.Goos + "_" + entry.Goarch + ext
}

// checkArchiveRow validates one contract row.
func checkArchiveRow(t *testing.T, entry *archiveEntry, seen map[string]bool) {
	t.Helper()
	if entry.File != contractArchiveName(entry) {
		t.Errorf("archive file = %q", entry.File)
	}
	checkArchiveNames(t, entry)
	if entry.NPMBin != "bin/verdict" && entry.NPMBin != "bin/verdict.exe" {
		t.Errorf("npm bin = %q; want bin/verdict[.exe]", entry.NPMBin)
	}
	key := entry.File + "\x00" + entry.NPMPackage
	if seen[key] {
		t.Errorf("duplicate archive row: %+v", entry)
	}
	seen[key] = true
}

// checkArchiveNames validates the npm naming of one row.
func checkArchiveNames(t *testing.T, entry *archiveEntry) {
	t.Helper()
	if entry.NPMOS == "windows" || entry.NPMCPU == "amd64" {
		t.Errorf("npm names use win32/x64, not windows/amd64: %+v", entry)
	}
	if !strings.HasPrefix(entry.NPMPackage, "@stainless-code/verdict-") {
		t.Errorf("npm package = %q; want scoped verdict name", entry.NPMPackage)
	}
}

// checkContractMeta validates the URL, formula, and cosign sections.
func checkContractMeta(t *testing.T, doc *contractDoc) {
	t.Helper()
	for _, placeholder := range []string{"{version}", "{goos}", "{goarch}", "{ext}"} {
		if !strings.Contains(doc.URLs.Archive, placeholder) {
			t.Errorf("urls.archive missing %s: %q", placeholder, doc.URLs.Archive)
		}
	}
	if doc.Formula.Tap == "" || doc.Formula.Class == "" || doc.Formula.Homepage == "" {
		t.Errorf("formula incomplete: %+v", doc.Formula)
	}
	if !strings.Contains(doc.Cosign.Identity, "refs/tags/v*") || doc.Cosign.Issuer == "" {
		t.Errorf("cosign incomplete: %+v", doc.Cosign)
	}
}

// TestContractShape validates the frozen naming itself.
func TestContractShape(t *testing.T) {
	doc := loadContract(t)
	if doc.Binary != "verdict" || doc.TagPrefix != "v" {
		t.Fatalf("contract header = %+v; want binary verdict, tag v", doc)
	}
	if len(doc.Archives) != 6 {
		t.Fatalf("archives = %d; want 6", len(doc.Archives))
	}
	seen := map[string]bool{}
	for i := range doc.Archives {
		checkArchiveRow(t, &doc.Archives[i], seen)
	}
	checkContractMeta(t, &doc)
}

// goreleaserWants lists substrings the config must contain.
func goreleaserWants() []string {
	return []string{
		"name_template: verdict_{{ .Version }}_{{ .Os }}_{{ .Arch }}",
		"main: ./cmd/verdict",
		"CGO_ENABLED=0",
		"-trimpath",
		"-X main.version={{.Version}}",
		"name_template: checksums.txt",
		"cmd: cosign",
		"sign-blob",
		"--bundle=${signature}",
		"${artifact}.sigstore.json",
		"artifacts: checksum",
		"artifacts: archive",
		"- LICENSE",
		"- README.md",
		"formats: [tar.gz]",
		"formats: [zip]",
	}
}

// goreleaserBanned lists pipe keys the config must not define.
func goreleaserBanned() []string {
	return []string{
		"homebrew_casks:", "homebrew_formulas:", "npms:", "winget:", "scoop:", "nfpms:",
	}
}

// checkGoreleaserKeys asserts the required and banned config keys.
func checkGoreleaserKeys(t *testing.T, gr string) {
	t.Helper()
	for _, want := range goreleaserWants() {
		if !strings.Contains(gr, want) {
			t.Errorf(".goreleaser.yml missing %q", want)
		}
	}
	for _, banned := range goreleaserBanned() {
		if strings.Contains(gr, banned) {
			t.Errorf(".goreleaser.yml must not define %q (contract owns those channels)", banned)
		}
	}
}

// TestGoreleaserMatchesContract asserts the GoReleaser config honors the
// frozen naming and owns the GitHub Release without store pipes.
func TestGoreleaserMatchesContract(t *testing.T) {
	doc := loadContract(t)
	gr := repoFile(t, ".goreleaser.yml")
	checkGoreleaserKeys(t, gr)
	for i := range doc.Archives {
		if doc.Archives[i].File != contractArchiveName(&doc.Archives[i]) {
			t.Errorf("contract archive %q does not match the goreleaser name template", doc.Archives[i].File)
		}
	}
}

// readManifest decodes a generated package.json.
func readManifest(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// checkNoScripts rejects install scripts (ignore-scripts installs work).
func checkNoScripts(t *testing.T, pkg string, manifest map[string]any) {
	t.Helper()
	for key := range manifest {
		if key == "scripts" || key == "postinstall" {
			t.Errorf("%s must define no %q", pkg, key)
		}
	}
}

// checkPlatformPackage validates one generated platform package.
func checkPlatformPackage(t *testing.T, out string, entry *archiveEntry) {
	t.Helper()
	pkg := strings.TrimPrefix(entry.NPMPackage, "@stainless-code/")
	dir := filepath.Join(out, pkg)
	manifest := readManifest(t, dir)
	if manifest["name"] != entry.NPMPackage || manifest["version"] != "1.2.3" {
		t.Errorf("%s name/version = %v/%v", pkg, manifest["name"], manifest["version"])
	}
	if toJSON(t, manifest["os"]) != `["`+entry.NPMOS+`"]` {
		t.Errorf("%s os = %v; want [%s]", pkg, manifest["os"], entry.NPMOS)
	}
	if toJSON(t, manifest["cpu"]) != `["`+entry.NPMCPU+`"]` {
		t.Errorf("%s cpu = %v; want [%s]", pkg, manifest["cpu"], entry.NPMCPU)
	}
	if toJSON(t, manifest["files"]) != `["`+entry.NPMBin+`"]` {
		t.Errorf("%s files = %v; want [%s]", pkg, manifest["files"], entry.NPMBin)
	}
	checkNoScripts(t, pkg, manifest)
	checkBinExecutable(t, pkg, filepath.Join(dir, entry.NPMBin))
}

// checkBinExecutable asserts the staged binary exists and is executable.
func checkBinExecutable(t *testing.T, pkg, bin string) {
	t.Helper()
	info, err := os.Stat(bin)
	if err != nil {
		t.Errorf("%s missing binary: %v", pkg, err)
	} else if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s binary is not executable", pkg)
	}
}

// checkWrapper validates the generated wrapper package.
func checkWrapper(t *testing.T, out string, doc *contractDoc) {
	t.Helper()
	wrapper := readManifest(t, filepath.Join(out, "verdict"))
	if wrapper["name"] != doc.NPM.Wrapper || wrapper["version"] != "1.2.3" {
		t.Errorf("wrapper name/version = %v/%v", wrapper["name"], wrapper["version"])
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(toJSON(t, wrapper["optionalDependencies"])), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(doc.Archives) {
		t.Errorf("wrapper optionalDependencies = %d; want %d", len(decoded), len(doc.Archives))
	}
	for _, entry := range doc.Archives {
		if decoded[entry.NPMPackage] != "1.2.3" {
			t.Errorf("wrapper dep %s = %q; want exact 1.2.3", entry.NPMPackage, decoded[entry.NPMPackage])
		}
	}
	checkNoScripts(t, "wrapper", wrapper)
}

// checkShimSource validates the checked-in shim the wrapper copies.
func checkShimSource(t *testing.T) {
	t.Helper()
	shim, err := os.ReadFile(filepath.Join("..", "scripts", "npm-shim", "verdict.js"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(shim), "\n") + 1; lines > 80 {
		t.Errorf("shim is %d lines; budget is ~80", lines)
	}
	for _, want := range []string{"require.resolve", "npm i -g @stainless-code/verdict@latest", "stdio"} {
		if !strings.Contains(string(shim), want) {
			t.Errorf("shim missing %q", want)
		}
	}
}

// TestNpmTreeMatchesContract generates the npm tree from staged binaries
// and asserts every manifest matches the contract.
func TestNpmTreeMatchesContract(t *testing.T) {
	node := nodeBin(t)
	doc := loadContract(t)
	bins := t.TempDir()
	stageBins(t, &doc, bins)
	out := filepath.Join(t.TempDir(), "npm")
	got, code := runNode(t, nil, node, "../scripts/generate-npm.mjs",
		"--bins", bins, "--out", out, "--version", "1.2.3")
	if code != 0 {
		t.Fatalf("generate-npm.mjs exit %d:\n%s", code, got)
	}
	for i := range doc.Archives {
		checkPlatformPackage(t, out, &doc.Archives[i])
	}
	checkWrapper(t, out, &doc)
	checkShimSource(t)
}

// TestPublishOrderRecordsArgv runs the publish script against a fake npm and
// asserts platforms publish first (contract order), the wrapper last, with
// provenance flags and a trailing dry-run.
func TestPublishOrderRecordsArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake npm is a shell script (unix); order is platform-independent JS")
	}
	node := nodeBin(t)
	doc := loadContract(t)
	bins := t.TempDir()
	stageBins(t, &doc, bins)
	fakeDir := t.TempDir()
	log := filepath.Join(fakeDir, "argv.log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + log + "\"\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "npm"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "npm")
	got, code := runNode(t, []string{"PATH=" + fakeDir + ":" + os.Getenv("PATH")},
		node, "../scripts/publish-npm.mjs",
		"--bins="+bins, "--out="+out, "--dry-run", "--version=1.2.3")
	if code != 0 {
		t.Fatalf("publish-npm.mjs exit %d:\n%s", code, got)
	}
	lines := strings.Split(strings.TrimSpace(string(mustRead(t, log))), "\n")
	if len(lines) != len(doc.Archives)+1 {
		t.Fatalf("fake npm saw %d publishes; want %d", len(lines), len(doc.Archives)+1)
	}
	checkPublishOrder(t, out, &doc, lines)
	checkPublishFlags(t, lines)
}

// checkPublishOrder asserts contract order with the wrapper last.
func checkPublishOrder(t *testing.T, out string, doc *contractDoc, lines []string) {
	t.Helper()
	for i := range doc.Archives {
		pkg := strings.TrimPrefix(doc.Archives[i].NPMPackage, "@stainless-code/")
		if !strings.Contains(lines[i], "publish "+filepath.Join(out, pkg)) {
			t.Errorf("publish %d = %q; want platform package %s", i, lines[i], pkg)
		}
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "publish "+filepath.Join(out, "verdict")) {
		t.Errorf("last publish = %q; want the wrapper", last)
	}
}

// mustRead reads a file, failing on error.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// checkPublishFlags asserts every fake publish carried the registry flags.
func checkPublishFlags(t *testing.T, lines []string) {
	t.Helper()
	for i, line := range lines {
		for _, want := range []string{"--provenance", "--access public", "--dry-run"} {
			if !strings.Contains(line, want) {
				t.Errorf("publish %d = %q; missing %q", i, line, want)
			}
		}
	}
}

// fixtureArchive sums the formula fixture: the four unix archives with
// fixed SHAs, plus windows archives and SBOM lines the generator ignores.
func fixtureArchive() [][2]string {
	return [][2]string{
		{"verdict_1.2.3_darwin_amd64.tar.gz", "e21955396dba8aba0342b65e915648661318546a097d87752080090837addaaa"},
		{"verdict_1.2.3_darwin_arm64.tar.gz", "481ea7a50543b38cc20de2edfbccec9bbb05e625594baa9d8b92f934a76798d2"},
		{"verdict_1.2.3_linux_amd64.tar.gz", "09f6952466cfe01b945faf1f3971cc2a0cbc0ed8ceeb6ad4f070b3a74e8a799a"},
		{"verdict_1.2.3_linux_arm64.tar.gz", "044cebf6f86921a5709d30f49cfd8b88c3b20fe7cb03c17130e2e948da512f6f"},
		{"verdict_1.2.3_windows_amd64.zip", strings.Repeat("a", 64)},
		{"verdict_1.2.3_windows_arm64.zip", strings.Repeat("b", 64)},
		{"verdict_1.2.3_darwin_amd64.tar.gz.sbom.json", strings.Repeat("c", 64)},
		{"verdict_1.2.3_linux_arm64.tar.gz.sbom.json", strings.Repeat("d", 64)},
	}
}

// TestFormulaMatchesContract renders the formula from a fixture checksums
// file and pins it byte-for-byte against the golden.
func TestFormulaMatchesContract(t *testing.T) {
	node := nodeBin(t)
	dir := t.TempDir()
	var sums strings.Builder
	for _, row := range fixtureArchive() {
		sums.WriteString(row[1] + "  " + row[0] + "\n")
	}
	sumsPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sumsPath, []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	formula := filepath.Join(dir, "verdict.rb")
	got, code := runNode(t, nil, node, "../scripts/generate-formula.mjs",
		"--checksums", sumsPath, "--out", formula, "--version", "1.2.3")
	if code != 0 {
		t.Fatalf("generate-formula.mjs exit %d:\n%s", code, got)
	}
	rendered := mustRead(t, formula)
	golden := mustRead(t, filepath.Join("testdata", "verdict.rb.golden"))
	if !bytes.Equal(rendered, golden) {
		t.Errorf("formula differs from golden:\n--- rendered ---\n%s\n--- golden ---\n%s",
			rendered, golden)
	}
}

// writeShimTree plants a wrapper (shim + manifest) under dir/node_modules,
// returning the shim path. Siblings resolve like installed optional
// dependencies.
func writeShimTree(t *testing.T, dir string) string {
	t.Helper()
	shim, err := os.ReadFile(filepath.Join("..", "scripts", "npm-shim", "verdict.js"))
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "node_modules", "wrapper", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"@stainless-code/verdict","version":"1.2.3"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "wrapper", "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	shimPath := filepath.Join(binDir, "verdict.js")
	if err := os.WriteFile(shimPath, shim, 0o755); err != nil {
		t.Fatal(err)
	}
	return shimPath
}

// currentPlatformPackage maps the test runtime to its contract npm package,
// skipping runtimes the contract does not ship.
func currentPlatformPackage(t *testing.T) (npmOS, npmCPU string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux":
		npmOS = runtime.GOOS
	case "windows":
		npmOS = "win32"
	default:
		t.Skip("contract ships no " + runtime.GOOS + " package")
	}
	switch runtime.GOARCH {
	case "amd64":
		npmCPU = "x64"
	case "arm64":
		npmCPU = "arm64"
	default:
		t.Skip("contract ships no " + runtime.GOARCH + " package")
	}
	return npmOS, npmCPU
}

// plantPlatformBinary stages the current platform's binary fixture.
func plantPlatformBinary(t *testing.T, pkgDir, node string) {
	t.Helper()
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(node)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "verdict.exe"), raw, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	stub := "#!/bin/sh\necho \"stub-verdict $@\"\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "verdict"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

// checkShimHit runs the shim against a planted platform pair.
func checkShimHit(t *testing.T, out string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("shim with platform pair exit %d; want 0\n%s", code, out)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(out, "v") {
			t.Errorf("shim hit output missing version marker:\n%s", out)
		}
	} else if !strings.Contains(out, "stub-verdict --version") {
		t.Errorf("shim did not exec the platform binary:\n%s", out)
	}
}

// TestShimResolvesPlatformPackage proves the wrapper shim execs the
// platform binary on a hit and refuses typed with the repair command on a
// miss.
func TestShimResolvesPlatformPackage(t *testing.T) {
	node := nodeBin(t)
	dir := t.TempDir()
	shim := writeShimTree(t, dir)
	out, code := runNode(t, nil, node, shim, "--version")
	if code != 1 {
		t.Fatalf("shim without platform pair exit %d; want 1\n%s", code, out)
	}
	for _, want := range []string{"missing-platform-binary", "npm i -g @stainless-code/verdict@latest"} {
		if !strings.Contains(out, want) {
			t.Errorf("shim miss output missing %q:\n%s", want, out)
		}
	}
	npmOS, npmCPU := currentPlatformPackage(t)
	pkgDir := filepath.Join(dir, "node_modules", "@stainless-code", "verdict-"+npmOS+"-"+npmCPU, "bin")
	plantPlatformBinary(t, pkgDir, node)
	name := "@stainless-code/verdict-" + npmOS + "-" + npmCPU
	manifest := `{"name":"` + name + `","version":"1.2.3"}` + "\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "..", "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runNode(t, nil, node, shim, "--version")
	checkShimHit(t, out, code)
}
