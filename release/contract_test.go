// Package release_test pins the release artifact contract: the frozen
// naming in release/contract.json, the GoReleaser config that must honor
// it, and the npm/formula generators plus the wrapper shim that consume it.
package release_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// archiveEntry is one frozen os/arch row of the contract.
type archiveEntry struct {
	Goos       string   `json:"goos"`
	Goarch     string   `json:"goarch"`
	File       string   `json:"file"`
	NPMPackage string   `json:"npmPackage"`
	NPMOS      string   `json:"npmOS"`
	NPMCPU     string   `json:"npmCPU"`
	NPMBin     string   `json:"npmBin"`
	Members    []string `json:"members"`
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
	Cosign          cosignPolicy `json:"cosign"`
	Project         string       `json:"project"`
	Repo            string       `json:"repo"`
	TagPrefix       string       `json:"tagPrefix"`
	Binary          string       `json:"binary"`
	Checksums       string       `json:"checksums"`
	SignatureBundle string       `json:"signatureBundle"`
	SBOMSuffix      string       `json:"sbomSuffix"`
	NPM             struct {
		Wrapper      string            `json:"wrapper"`
		Bin          map[string]string `json:"bin"`
		WrapperFiles []string          `json:"wrapperFiles"`
		PublishArgs  []string          `json:"publishArgs"`
	} `json:"npm"`
	Archives []archiveEntry `json:"archives"`
}

// cosignPolicy is the contract's signing policy: the tag-wildcard identity
// GoReleaser signs with, its installer-facing regexp form, and the OIDC issuer.
type cosignPolicy struct {
	Identity       string `json:"identity"`
	IdentityRegexp string `json:"identityRegexp"`
	Issuer         string `json:"issuer"`
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
	for i := range doc.Archives {
		entry := &doc.Archives[i]
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
	checkArchiveMembers(t, entry)
	if entry.NPMBin != "bin/verdict" && entry.NPMBin != "bin/verdict.exe" {
		t.Errorf("npm bin = %q; want bin/verdict[.exe]", entry.NPMBin)
	}
	key := entry.File + "\x00" + entry.NPMPackage
	if seen[key] {
		t.Errorf("duplicate archive row: %+v", entry)
	}
	seen[key] = true
}

// checkArchiveMembers validates one row's archive allowlist: the platform
// binary first, then the extra files GoReleaser packs, with no duplicates and
// no path separators (both installers match bare member names).
func checkArchiveMembers(t *testing.T, entry *archiveEntry) {
	t.Helper()
	wantBinary := "verdict"
	if entry.Goos == "windows" {
		wantBinary = "verdict.exe"
	}
	if len(entry.Members) < 2 || entry.Members[0] != wantBinary {
		t.Errorf("%s/%s members = %v; want the platform binary first", entry.Goos, entry.Goarch, entry.Members)
	}
	seen := checkMemberShapes(t, entry)
	for _, required := range []string{"LICENSE", "README.md"} {
		if !seen[required] {
			t.Errorf("%s/%s members %v lack %s", entry.Goos, entry.Goarch, entry.Members, required)
		}
	}
}

// checkMemberShapes validates every member name and returns the seen set.
func checkMemberShapes(t *testing.T, entry *archiveEntry) map[string]bool {
	t.Helper()
	seen := make(map[string]bool, len(entry.Members))
	for _, member := range entry.Members {
		switch {
		case member == "":
			t.Errorf("%s/%s has an empty member", entry.Goos, entry.Goarch)
		case strings.ContainsAny(member, "/\\"):
			t.Errorf("member %q must be a bare file name", member)
		case seen[member]:
			t.Errorf("%s/%s repeats member %q", entry.Goos, entry.Goarch, member)
		}
		seen[member] = true
	}
	return seen
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
	checkCosignPolicy(t, &doc.Cosign)
}

// checkCosignPolicy validates the signing identity and its regexp form.
func checkCosignPolicy(t *testing.T, cosign *cosignPolicy) {
	t.Helper()
	if !strings.Contains(cosign.Identity, "refs/tags/v*") || cosign.Issuer == "" {
		t.Errorf("cosign incomplete: %+v", *cosign)
	}
	if want := deriveIdentityRegexp(cosign.Identity); cosign.IdentityRegexp != want {
		t.Errorf("cosign.identityRegexp = %q; want the expansion of identity %q = %q",
			cosign.IdentityRegexp, cosign.Identity, want)
	}
}

// deriveIdentityRegexp is the independent oracle for cosign.identityRegexp:
// the installers take a regexp, GoReleaser signs with the tag wildcard, and
// the two representations must stay equivalent.
func deriveIdentityRegexp(identity string) string {
	if prefix, ok := strings.CutSuffix(identity, "v*"); ok {
		return "^" + regexp.QuoteMeta(prefix) + `v[0-9]+\.[0-9]+\.[0-9]+.*$`
	}
	return "^" + regexp.QuoteMeta(identity) + "$"
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

// checkGoreleaserKeys asserts the required and banned config keys, and that
// every non-binary archive member the contract allowlists is actually packed.
func checkGoreleaserKeys(t *testing.T, gr string, doc *contractDoc) {
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
	checkArchiveFilesList(t, gr, doc.Archives[0].Members[1:])
}

// checkArchiveFilesList asserts the goreleaser archive files carry every
// extra contract member.
func checkArchiveFilesList(t *testing.T, gr string, members []string) {
	t.Helper()
	for _, member := range members {
		if !strings.Contains(gr, "\n      - "+member+"\n") {
			t.Errorf(".goreleaser.yml archive files lack the contract member %q", member)
		}
	}
}

// TestGoreleaserMatchesContract asserts the GoReleaser config honors the
// frozen naming and owns the GitHub Release without store pipes.
func TestGoreleaserMatchesContract(t *testing.T) {
	doc := loadContract(t)
	gr := repoFile(t, ".goreleaser.yml")
	checkGoreleaserKeys(t, gr, &doc)
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
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 { // NTFS carries no executable bit
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
	for i := range doc.Archives {
		entry := &doc.Archives[i]
		if decoded[entry.NPMPackage] != "1.2.3" {
			t.Errorf("wrapper dep %s = %q; want exact 1.2.3", entry.NPMPackage, decoded[entry.NPMPackage])
		}
	}
	checkWrapperPlatformMap(t, out)
	checkNoScripts(t, "wrapper", wrapper)
}

// checkWrapperPlatformMap proves the wrapper ships the generated map the shim
// requires, byte for byte.
func checkWrapperPlatformMap(t *testing.T, out string) {
	t.Helper()
	shipped := mustRead(t, filepath.Join(out, "verdict", "bin", "platforms.gen.json"))
	if want := mustRead(t, filepath.Join("..", "scripts", "npm-shim", "platforms.gen.json")); !bytes.Equal(shipped, want) {
		t.Error("wrapper bin/platforms.gen.json differs from the generated shim map")
	}
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
	for _, want := range []string{"require.resolve", "npm i -g @stainless-code/verdict@latest", "stdio", `require("./platforms.gen.json")`} {
		if !strings.Contains(string(shim), want) {
			t.Errorf("shim missing %q", want)
		}
	}
	// The map has one source (release/contract.json): an inlined copy here
	// would drift from the client's generated copy on the next rename.
	if strings.Contains(string(shim), "@stainless-code/verdict-linux-x64") {
		t.Error("shim inlines the platform map; require the generated platforms.gen.json instead")
	}
}

// TestGeneratedPlatformMapsMatchContract proves the platform -> npm package
// map has one source: the client's typed copy and the shim's JSON copy must
// both equal contract.archives, so renaming a package cannot leave a stale
// resolver behind.
func TestGeneratedPlatformMapsMatchContract(t *testing.T) {
	doc := loadContract(t)
	want := make(map[string]string, len(doc.Archives))
	for _, entry := range doc.Archives {
		want[entry.NPMOS+"-"+entry.NPMCPU] = entry.NPMPackage
	}
	shimMap := decodePlatformMap(t, mustRead(t, filepath.Join("..", "scripts", "npm-shim", "platforms.gen.json")))
	if !maps.Equal(shimMap, want) {
		t.Errorf("scripts/npm-shim/platforms.gen.json = %v; want %v", shimMap, want)
	}
	clientMap := parseClientPlatformMap(t, repoFile(t, "clients/ts/src/platforms.gen.ts"))
	if !maps.Equal(clientMap, want) {
		t.Errorf("clients/ts/src/platforms.gen.ts = %v; want %v", clientMap, want)
	}
}

// decodePlatformMap decodes the shim's generated JSON map.
func decodePlatformMap(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("platforms.gen.json: %v", err)
	}
	return got
}

// clientPlatformPair matches one `"os-cpu": "@stainless-code/verdict-…"` row
// of the generated TypeScript map.
var clientPlatformPair = regexp.MustCompile(`"([a-z0-9]+-[a-z0-9]+)": "([^"]+)"`)

// parseClientPlatformMap reads the generated TypeScript map's rows.
func parseClientPlatformMap(t *testing.T, source string) map[string]string {
	t.Helper()
	got := make(map[string]string)
	for _, match := range clientPlatformPair.FindAllStringSubmatch(source, -1) {
		got[match[1]] = match[2]
	}
	if len(got) == 0 {
		t.Fatal("clients/ts/src/platforms.gen.ts has no platform rows")
	}
	return got
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

// TestPublishSkipsExistingVersions proves --skip-existing resumes a partial
// publish: the version the fake registry already serves is skipped and the
// remaining packages keep contract order with the wrapper last.
func TestPublishSkipsExistingVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake npm is a shell script (unix); order is platform-independent JS")
	}
	node := nodeBin(t)
	doc := loadContract(t)
	bins := t.TempDir()
	stageBins(t, &doc, bins)
	fakeDir := t.TempDir()
	log := filepath.Join(fakeDir, "argv.log")
	skipped := "@stainless-code/verdict-linux-x64"
	fake := "#!/bin/sh\n" +
		"if [ \"$1\" = view ]; then\n" +
		"  if [ \"$2\" = \"" + skipped + "@1.2.3\" ]; then echo 1.2.3; exit 0; fi\n" +
		"  exit 1\n" +
		"fi\n" +
		"printf '%s\\n' \"$*\" >> \"" + log + "\"\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "npm"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "npm")
	got, code := runNode(t, []string{"PATH=" + fakeDir + ":" + os.Getenv("PATH")},
		node, "../scripts/publish-npm.mjs",
		"--bins="+bins, "--out="+out, "--skip-existing", "--version=1.2.3")
	if code != 0 {
		t.Fatalf("publish-npm.mjs exit %d:\n%s", code, got)
	}
	lines := strings.Split(strings.TrimSpace(string(mustRead(t, log))), "\n")
	if len(lines) != len(doc.Archives) {
		t.Fatalf("fake npm saw %d publishes; want %d (one skipped)", len(lines), len(doc.Archives))
	}
	assertPublishOrderSkipping(t, out, &doc, lines, skipped)
}

// wantPublishDirs lists the expected publish targets after skipping one
// platform package, wrapper last.
func wantPublishDirs(out string, doc *contractDoc, skipped string) []string {
	want := make([]string, 0, len(doc.Archives))
	for i := range doc.Archives {
		if doc.Archives[i].NPMPackage == skipped {
			continue
		}
		want = append(want, filepath.Join(out, strings.TrimPrefix(doc.Archives[i].NPMPackage, "@stainless-code/")))
	}
	return append(want, filepath.Join(out, "verdict"))
}

// assertPublishOrderSkipping pins the resume order against the fake npm log.
func assertPublishOrderSkipping(t *testing.T, out string, doc *contractDoc, lines []string, skipped string) {
	t.Helper()
	for i, pkg := range wantPublishDirs(out, doc, skipped) {
		if !strings.Contains(lines[i], "publish "+pkg) {
			t.Errorf("publish %d = %q; want %s", i, lines[i], pkg)
		}
	}
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

// writeShimTree plants a wrapper (shim, generated platform map, and
// manifest) under dir/node_modules, returning the shim path. Siblings resolve
// like installed optional dependencies.
func writeShimTree(t *testing.T, dir string) string {
	t.Helper()
	shim := mustRead(t, filepath.Join("..", "scripts", "npm-shim", "verdict.js"))
	platforms := mustRead(t, filepath.Join("..", "scripts", "npm-shim", "platforms.gen.json"))
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
	if err := os.WriteFile(filepath.Join(binDir, "platforms.gen.json"), platforms, 0o644); err != nil {
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

// platformRow returns the contract row for one goos/goarch, failing when the
// contract ships no such platform.
func platformRow(t *testing.T, doc *contractDoc, goos, goarch string) *archiveEntry {
	t.Helper()
	for i := range doc.Archives {
		if doc.Archives[i].Goos == goos && doc.Archives[i].Goarch == goarch {
			return &doc.Archives[i]
		}
	}
	t.Fatalf("contract has no %s/%s row", goos, goarch)
	return nil
}

// membersPattern renders a row's allowlist as the POSIX installer's anchored
// alternation (dots escaped, so the shell pattern is literal).
func membersPattern(members []string) string {
	escaped := make([]string, 0, len(members))
	for _, member := range members {
		escaped = append(escaped, strings.ReplaceAll(member, ".", `\.`))
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

// membersArray renders a row's allowlist as the PowerShell array literal.
func membersArray(members []string) string {
	return "@('" + strings.Join(members, "', '") + "')"
}

// requireLiterals fails for every literal the file does not carry verbatim.
func requireLiterals(t *testing.T, name, body string, literals []string) {
	t.Helper()
	for _, literal := range literals {
		if !strings.Contains(body, literal) {
			t.Errorf("%s drifted from release/contract.json: missing %q", name, literal)
		}
	}
}

// TestInstallersMatchContract pins the policy the shipped installers carry a
// second copy of: the cosign identity/issuer, the archive-member allowlist,
// and the checksum/bundle names. contract.json is the source; a repo rename,
// a workflow rename, or a new archive member must fail here instead of at
// install time.
func TestInstallersMatchContract(t *testing.T) {
	doc := loadContract(t)
	unix := platformRow(t, &doc, "darwin", "amd64")
	windows := platformRow(t, &doc, "windows", "amd64")
	sh := repoFile(t, "install.sh")
	ps1 := repoFile(t, "install.ps1")
	requireLiterals(t, "install.sh", sh, []string{
		"COSIGN_IDENTITY='" + doc.Cosign.Identity + "'",
		"COSIGN_IDENTITY_RE='" + doc.Cosign.IdentityRegexp + "'",
		"COSIGN_ISSUER='" + doc.Cosign.Issuer + "'",
		"CHECKSUMS='" + doc.Checksums + "'",
		"BUNDLE='" + doc.SignatureBundle + "'",
		"grep -Ev '" + membersPattern(unix.Members) + "'",
	})
	requireLiterals(t, "install.ps1", ps1, []string{
		"$IdentityRegexp = '" + doc.Cosign.IdentityRegexp + "'",
		"$Issuer = '" + doc.Cosign.Issuer + "'",
		"$ChecksumsName = '" + doc.Checksums + "'",
		"$BundleName = '" + doc.SignatureBundle + "'",
		"$allowed = " + membersArray(windows.Members),
	})
}
