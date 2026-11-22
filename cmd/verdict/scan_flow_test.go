package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// scanBundleArtifacts names every file a scan bundle carries beside the diff.
var scanBundleArtifacts = []string{
	"source.json", "full.diff", "full.raw.diff", "toc.txt", "roles.txt",
	"hunks.tsv", "excluded.json", "run.json", "provenance.json",
	"identity.json", "lenses.txt",
}

// scanExclusions is the excluded.json receipt a scan writes.
type scanExclusions struct {
	Defaults []string        `json:"defaults"`
	Excluded []scanExclusion `json:"excluded"`
}

// scanExclusion is one withheld path with the rule and reason that withheld it.
type scanExclusion struct {
	Path   string `json:"path"`
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

// row renders one exclusion's rule and reason, or the empty string when the
// path is not receipted at all.
func (s scanExclusions) row(path string) string {
	for _, e := range s.Excluded {
		if e.Path == path {
			return e.RuleID + " " + e.Reason
		}
	}
	return ""
}

// scanPreviewFile is one selected file in the --preview payload.
type scanPreviewFile struct {
	Path string `json:"path"`
}

// scanPreviewData is the preview block a scan answers with.
type scanPreviewData struct {
	Included      []scanPreviewFile `json:"included"`
	Excluded      []scanExclusion   `json:"excluded"`
	IncludedBytes int               `json:"included_bytes"`
}

// scanPreviewPayload is the --preview --json envelope.
type scanPreviewPayload struct {
	Data struct {
		Preview scanPreviewData `json:"preview"`
	} `json:"data"`
}

// scanChunk is one ordered chunk in the chunk manifest.
type scanChunk struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// scanChunkManifest is the manifest a chunked scan writes.
type scanChunkManifest struct {
	Chunks     []scanChunk `json:"chunks"`
	ChunkCount int         `json:"chunk_count"`
}

// scanSarifText is a SARIF message.
type scanSarifText struct {
	Text string `json:"text"`
}

// scanSarifArtifact is a SARIF artifact location.
type scanSarifArtifact struct {
	URI string `json:"uri"`
}

// scanSarifRegion is the line range a SARIF result anchors to.
type scanSarifRegion struct {
	StartLine int `json:"startLine"`
}

// scanSarifPhysical is one SARIF physical location.
type scanSarifPhysical struct {
	ArtifactLocation scanSarifArtifact `json:"artifactLocation"`
	Region           scanSarifRegion   `json:"region"`
}

// scanSarifLocation is one SARIF location entry.
type scanSarifLocation struct {
	PhysicalLocation scanSarifPhysical `json:"physicalLocation"`
}

// scanSarifResult is one SARIF result.
type scanSarifResult struct {
	RuleID    string              `json:"ruleId"`
	Message   scanSarifText       `json:"message"`
	Locations []scanSarifLocation `json:"locations"`
}

// scanSarifRun is one SARIF run.
type scanSarifRun struct {
	Results []scanSarifResult `json:"results"`
}

// scanSarif is the SARIF document a scan report exports.
type scanSarif struct {
	Version string         `json:"version"`
	Runs    []scanSarifRun `json:"runs"`
}

// scanWrite writes one fixture file, creating its parent directories: writeIn
// writes only into directories the caller already made.
func scanWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIn(t, path, body)
}

// scanRepo seeds a committed checkout with no uncommitted changes, so every
// --scan run below reviews files no git diff names. The secret documents are
// committed on purpose: the withholding floor is a path rule, not a diff rule.
func scanRepo(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	scanWrite(t, filepath.Join(root, "app", "main.go"), "package main\n\nfunc main() { println(1) }\n")
	scanWrite(t, filepath.Join(root, "app", "util.go"), "package main\n\nfunc util() int { return 1 }\n")
	scanWrite(t, filepath.Join(root, "docs", "readme.md"), "# docs\n")
	scanWrite(t, filepath.Join(root, ".env"), "DATABASE_PASSWORD=hunter2\n")
	scanWrite(t, filepath.Join(root, "server.pem"), "-----BEGIN PRIVATE KEY-----\nsecret\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "base")
	if dirty := gitIn(t, root, "status", "--porcelain"); dirty != "" {
		t.Fatalf("fixture tree is dirty, so the scan proves no zero-diff run: %q", dirty)
	}
	t.Chdir(root)
	return root
}

// scanBundle names the local bundle a --scan run writes.
func scanBundle(root, name string) string {
	return filepath.Join(root, ".verdict", "state", "local-"+name)
}

// mustRead returns one file's bytes, failing the test if it cannot.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// requireContains fails unless text carries every want, naming what was read.
func requireContains(t *testing.T, label, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s misses %q:\n%s", label, want, text)
		}
	}
}

// requireAbsent fails if text carries any banned fragment.
func requireAbsent(t *testing.T, label, text string, banned ...string) {
	t.Helper()
	for _, bad := range banned {
		if strings.Contains(text, bad) {
			t.Errorf("%s carries %q:\n%s", label, bad, text)
		}
	}
}

// requireFile pins one file's exact contents.
func requireFile(t *testing.T, path, want string) {
	t.Helper()
	if got := mustRead(t, path); got != want {
		t.Errorf("%s = %q; want %q", filepath.Base(path), got, want)
	}
}

// assertScanBundleArtifacts proves the bundle carries every artifact the
// downstream verbs read.
func assertScanBundleArtifacts(t *testing.T, bundle string) {
	t.Helper()
	for _, name := range scanBundleArtifacts {
		if _, err := os.Stat(filepath.Join(bundle, name)); err != nil {
			t.Errorf("bundle misses %s: %v", name, err)
		}
	}
}

// TestScanDirectoryReviewsACommittedTree proves the core promise: a directory
// scan builds a reviewable bundle from a clean tree, needing no git diff.
func TestScanDirectoryReviewsACommittedTree(t *testing.T) {
	root := scanRepo(t)
	if got := runOne(t, "fetch-local", "acme/app", "dir", "--scan", "app"); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan app = %+v; want success", got)
	}
	bundle := scanBundle(root, "dir")
	assertScanBundleArtifacts(t, bundle)
	requireContains(t, "source.json", mustRead(t, filepath.Join(bundle, "source.json")), `"kind": "scan"`)
	diff := mustRead(t, filepath.Join(bundle, "full.diff"))
	requireContains(t, "full.diff", diff,
		"diff --git a/app/main.go b/app/main.go\nnew file mode 100644\n--- /dev/null\n+++ b/app/main.go\n@@ -0,0 +1,3 @@\n+package main\n",
		"diff --git a/app/util.go b/app/util.go\n")
	requireAbsent(t, "full.diff", diff, "docs/readme.md")
	requireFile(t, filepath.Join(bundle, "hunks.tsv"), "app/main.go\tNEW\t1\t3\napp/util.go\tNEW\t1\t3\n")
}

// TestScanFileReviewsExactlyThatPath proves a file operand selects one file.
func TestScanFileReviewsExactlyThatPath(t *testing.T) {
	root := scanRepo(t)
	if got := runOne(t, "fetch-local", "acme/app", "one", "--scan", "app/util.go"); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan app/util.go = %+v; want success", got)
	}
	diff := mustRead(t, filepath.Join(scanBundle(root, "one"), "full.diff"))
	requireContains(t, "full.diff", diff, "diff --git a/app/util.go b/app/util.go\n")
	requireAbsent(t, "full.diff", diff, "app/main.go", "docs/readme.md")
}

// TestScanWithholdsBuiltInSecrets proves the withholding floor applies to a
// scan exactly as it does to a diff: the paths are withheld from the reviewed
// bytes and receipted as default:secret, whatever the tree holds.
func TestScanWithholdsBuiltInSecrets(t *testing.T) {
	root := scanRepo(t)
	if got := runOne(t, "fetch-local", "acme/app", "all", "--scan", "."); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan . = %+v; want success", got)
	}
	bundle := scanBundle(root, "all")
	requireAbsent(t, "full.diff", mustRead(t, filepath.Join(bundle, "full.diff")),
		"DATABASE_PASSWORD", "BEGIN PRIVATE KEY")
	assertSecretReceipts(t, bundle)
}

// assertSecretReceipts pins the excluded.json rows and the toc beside them.
func assertSecretReceipts(t *testing.T, bundle string) {
	t.Helper()
	var receipt scanExclusions
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(bundle, "excluded.json"))), &receipt); err != nil {
		t.Fatalf("excluded.json: %v", err)
	}
	if !slices.Contains(receipt.Defaults, "default:secret") {
		t.Errorf("excluded.json defaults = %v; want default:secret", receipt.Defaults)
	}
	for _, path := range []string{".env", "server.pem"} {
		if got := receipt.row(path); got != "default:secret secret-builtin" {
			t.Errorf("excluded.json row for %s = %q; want default:secret secret-builtin", path, got)
		}
	}
	requireContains(t, "toc.txt", mustRead(t, filepath.Join(bundle, "toc.txt")), "- .env (default:secret")
}

// TestScanPreviewWritesNoBundle proves --preview stops before every write: the
// state tree does not exist afterwards, while the payload names the withheld
// secrets with their rule.
func TestScanPreviewWritesNoBundle(t *testing.T) {
	root := scanRepo(t)
	got := runOne(t, "fetch-local", "acme/app", "preview", "--scan", ".", "--preview", "--json")
	if got.code != cli.ExitOK {
		t.Fatalf("preview = %+v; want success", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict")); !os.IsNotExist(err) {
		t.Fatalf("preview left .verdict behind: %v", err)
	}
	var envelope scanPreviewPayload
	if err := json.Unmarshal([]byte(got.out), &envelope); err != nil {
		t.Fatalf("preview envelope: %v\n%s", err, got.out)
	}
	preview := envelope.Data.Preview
	if preview.IncludedBytes == 0 {
		t.Error("preview reports no included bytes")
	}
	for _, path := range []string{".env", "server.pem"} {
		assertPreviewExcluded(t, preview, path)
	}
}

// assertPreviewExcluded pins one withheld path in the preview payload.
func assertPreviewExcluded(t *testing.T, preview scanPreviewData, path string) {
	t.Helper()
	for _, e := range preview.Excluded {
		if e.Path != path {
			continue
		}
		if e.RuleID != "default:secret" || e.Reason != "secret-builtin" {
			t.Errorf("preview excluded %s = %s/%s; want default:secret/secret-builtin", path, e.RuleID, e.Reason)
		}
		return
	}
	included := make([]string, 0, len(preview.Included))
	for _, f := range preview.Included {
		included = append(included, f.Path)
	}
	t.Errorf("preview does not report %s as excluded: included=%v", path, included)
}

// TestScanRefusesUnsafeOperands proves every operand failure is fail-closed:
// an escape, a symlink (inside or escaping), a managed directory, and a missing
// path each refuse bad-scan with the text that says why.
func TestScanRefusesUnsafeOperands(t *testing.T) {
	root := scanRepo(t)
	outside := scanLinkFixture(t, root)
	cases := []struct {
		name string
		arg  string
		want string
	}{
		{"escape", filepath.Join(outside, "outside.txt"), "resolves outside the repository root"},
		{"inside symlink", "inside-link.go", "is a symlink"},
		{"escaping symlink", "outside-link.txt", "is a symlink"},
		{"managed dir", ".git", "which a review never reads"},
		{"missing", "nope.go", "no such path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertScanOperandRefusal(t, c.arg, c.want)
		})
	}
}

// scanLinkFixture plants the inside and escaping links the operand cases read,
// returning the directory the escaping one targets.
func scanLinkFixture(t *testing.T, root string) string {
	t.Helper()
	outside := t.TempDir()
	scanWrite(t, filepath.Join(outside, "outside.txt"), "outside\n")
	if err := os.Symlink(filepath.Join(root, "app", "main.go"), filepath.Join(root, "inside-link.go")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(root, "outside-link.txt")); err != nil {
		t.Fatal(err)
	}
	return outside
}

// assertScanOperandRefusal runs one selector and pins the envelope it answers.
func assertScanOperandRefusal(t *testing.T, operand, want string) {
	t.Helper()
	got := runOne(t, "fetch-local", "acme/app", "bad", "--scan", operand, "--json")
	if got.code != cli.ExitRefused {
		t.Fatalf("--scan %s exit = %d; want %d\n%s%s", operand, got.code, cli.ExitRefused, got.out, got.err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(got.out), &envelope); err != nil {
		t.Fatalf("refusal envelope: %v\n%s", err, got.out)
	}
	if envelope.Error.Code != "bad-scan" {
		t.Errorf("--scan %s refused %q; want bad-scan", operand, envelope.Error.Code)
	}
	if !strings.Contains(got.out, want) {
		t.Errorf("--scan %s envelope = %s; want the message to carry %q", operand, got.out, want)
	}
}

// TestScanRefusesSecondSelector proves --scan cannot share the invocation with
// a second source or a pathspec filter: each is a usage refusal.
func TestScanRefusesSecondSelector(t *testing.T) {
	scanRepo(t)
	flags := []struct {
		name string
		args []string
	}{
		{"staged", []string{"--scan", ".", "--staged"}},
		{"range", []string{"--scan", ".", "--range", "HEAD~1..HEAD"}},
		{"base", []string{"--scan", ".", "--base", "main"}},
		{"stash", []string{"--scan", ".", "--stash"}},
		{"worktree", []string{"--scan", ".", "--worktree"}},
		{"paths", []string{"--scan", ".", "--", "app/main.go"}},
	}
	for _, c := range flags {
		t.Run(c.name, func(t *testing.T) {
			assertSecondSelectorRefusal(t, c.args)
		})
	}
}

// assertSecondSelectorRefusal pins one mutually-exclusive combination.
func assertSecondSelectorRefusal(t *testing.T, args []string) {
	t.Helper()
	full := append([]string{"fetch-local", "acme/app", "clash"}, args...)
	got := runOne(t, full...)
	if got.code != cli.ExitRefused {
		t.Fatalf("%v exit = %d; want refusal %d\n%s", full, got.code, cli.ExitRefused, got.err)
	}
	if !strings.Contains(got.err, "mutually exclusive") {
		t.Errorf("%v stderr = %q; want the mutual-exclusion refusal", full, got.err)
	}
}

// TestFailedScanRerunNeverLeavesUnfilteredBytes proves the scan stages its diff
// in memory: a refusal between the scan and the exclusion filter leaves the
// bundle's filtered full.diff byte-identical, never an unfiltered copy holding
// the secret documents the filter withholds.
func TestFailedScanRerunNeverLeavesUnfilteredBytes(t *testing.T) {
	root := scanRepo(t)
	if got := runOne(t, "fetch-local", "acme/app", "guard", "--scan", "."); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan . = %+v; want success\n%s", got, got.err)
	}
	bundle := scanBundle(root, "guard")
	filtered := mustRead(t, filepath.Join(bundle, "full.diff"))
	requireAbsent(t, "the first full.diff", filtered, "DATABASE_PASSWORD", "BEGIN PRIVATE KEY")

	// A malformed trusted-base config refuses after the scan has synthesized its
	// diff and before the exclusion filter runs.
	scanWrite(t, filepath.Join(root, ".verdict", "config.json"), "{not json\n")
	gitIn(t, root, "add", "-f", ".verdict/config.json")
	gitIn(t, root, "commit", "-qm", "broken config")

	got := runOne(t, "fetch-local", "acme/app", "guard", "--scan", ".")
	if got.code != cli.ExitRefused {
		t.Fatalf("the rerun = %+v; want a refusal\n%s", got, got.err)
	}
	requireFile(t, filepath.Join(bundle, "full.diff"), filtered)
	requireAbsent(t, "the rerun's full.diff", mustRead(t, filepath.Join(bundle, "full.diff")),
		"DATABASE_PASSWORD", "BEGIN PRIVATE KEY")
}

// TestScanChunkSplitsAnOverCapTree proves a scan flows into the shipped chunk
// gate: an over-cap synthesized diff slices on file boundaries, the manifest
// caps it, and the ordered chunks concatenate back to full.diff byte for byte.
func TestScanChunkSplitsAnOverCapTree(t *testing.T) {
	root := scanRepo(t)
	writeScanChunkTree(t, root)
	if got := runOne(t, "fetch-local", "acme/app", "chunked", "--scan", "big", "--chunk"); got.code != cli.ExitOK {
		t.Fatalf("--scan big --chunk = %+v; want success\n%s", got, got.err)
	}
	assertScanChunks(t, scanBundle(root, "chunked"))
}

// writeScanChunkTree writes six files whose synthesized diff passes the cap.
func writeScanChunkTree(t *testing.T, root string) {
	t.Helper()
	var body strings.Builder
	for range 3000 {
		body.WriteString("scanned line ")
		body.WriteString(strings.Repeat("x", 20))
		body.WriteString("\n")
	}
	for i := range 6 {
		scanWrite(t, filepath.Join(root, "big", "f"+string(rune('1'+i))+".txt"), body.String())
	}
}

// assertScanChunks pins the manifest's chunk count, per-chunk bytes, and the
// byte-identical concatenation back to full.diff.
func assertScanChunks(t *testing.T, bundle string) {
	t.Helper()
	manifest := mustRead(t, filepath.Join(bundle, "chunks", "manifest.json"))
	var man scanChunkManifest
	if err := json.Unmarshal([]byte(manifest), &man); err != nil {
		t.Fatalf("manifest: %v\n%s", err, manifest)
	}
	if man.ChunkCount < 2 || len(man.Chunks) != man.ChunkCount {
		t.Fatalf("manifest chunk_count = %d, chunks = %d; want at least 2 ordered chunks", man.ChunkCount, len(man.Chunks))
	}
	var concat strings.Builder
	for _, chunk := range man.Chunks {
		part := mustRead(t, filepath.Join(bundle, "chunks", chunk.Name+".diff"))
		if int64(len(part)) != chunk.Bytes {
			t.Errorf("%s bytes = %d; manifest says %d", chunk.Name, len(part), chunk.Bytes)
		}
		concat.WriteString(part)
	}
	if diff := mustRead(t, filepath.Join(bundle, "full.diff")); concat.String() != diff {
		t.Errorf("ordered chunk diffs do not concatenate to full.diff (%d vs %d bytes)", concat.Len(), len(diff))
	}
}

// TestScanBundleFlowsThroughRecordReportGate proves a scan bundle is an
// ordinary local bundle downstream: record writes the store, report renders it
// and exports SARIF, and gate composes a passing verdict from it.
func TestScanBundleFlowsThroughRecordReportGate(t *testing.T) {
	root := scanRepo(t)
	if got := runOne(t, "fetch-local", "acme/app", "flow", "--scan", "app"); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan app = %+v; want success\n%s", got, got.err)
	}
	bundle := scanBundle(root, "flow")
	writeScanReview(t, bundle)
	assertScanRecord(t, bundle)
	assertScanReport(t)
	assertScanSarif(t)
	assertScanGate(t)
}

// writeScanReview writes the review the scan bundle's diff can attest: both
// files covered and receipted, so the coverage and evidence floors pass.
func writeScanReview(t *testing.T, bundle string) {
	t.Helper()
	writeIn(t, filepath.Join(bundle, "review.json"), `{"schema_version":"1","summary":"one finding",`+
		`"findings":[{"path":"app/main.go","line":3,"side":"RIGHT",`+
		`"claim":"The entry point returns a constant that should be configurable.",`+
		`"quote":"func main() { println(1) }","confidence":"high","rigor":"walked","severity":"informational"}],`+
		`"coverage":[{"file":"app/main.go","ranges":"all"},{"file":"app/util.go","ranges":"all"}],`+
		`"receipts":[{"hunk":"app/main.go\tNEW\t1\t3","quote":"package main"},`+
		`{"hunk":"app/util.go\tNEW\t1\t3","quote":"package main"}],"spec_checks":[]}`+"\n")
}

// assertScanRecord pins the store the review lands in.
func assertScanRecord(t *testing.T, bundle string) {
	t.Helper()
	if got := runOne(t, "record", "acme/app", "local-flow", "--json"); got.code != cli.ExitOK {
		t.Fatalf("record = %+v; want success\n%s", got, got.err)
	}
	rec := mustRead(t, filepath.Join(bundle, "record.json"))
	requireContains(t, "record.json", rec, `"findings": 1`, `"status": "local"`)
}

// assertScanReport pins the human-facing projection of the recorded finding.
func assertScanReport(t *testing.T) {
	t.Helper()
	report := runOne(t, "report", "acme/app", "local-flow", "--json")
	if report.code != cli.ExitOK {
		t.Fatalf("report = %+v; want success\n%s", report, report.err)
	}
	requireContains(t, "report envelope", report.out, "configurable")
}

// assertScanSarif pins the SARIF document's shape and the location it anchors.
func assertScanSarif(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.sarif")
	if got := runOne(t, "report", "acme/app", "local-flow", "--sarif", path, "--json"); got.code != cli.ExitOK {
		t.Fatalf("report --sarif = %+v; want success\n%s", got, got.err)
	}
	var doc scanSarif
	if err := json.Unmarshal([]byte(mustRead(t, path)), &doc); err != nil {
		t.Fatalf("sarif: %v", err)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 1 {
		t.Fatalf("sarif = %+v; want one 2.1.0 run with one result", doc)
	}
	result := doc.Runs[0].Results[0]
	if result.RuleID != "verdict/inline" || !strings.Contains(result.Message.Text, "configurable") {
		t.Errorf("sarif result = %+v; want the inline finding", result)
	}
	assertSarifLocation(t, result)
}

// assertSarifLocation pins the artifact path and the line the result anchors.
func assertSarifLocation(t *testing.T, result scanSarifResult) {
	t.Helper()
	loc := result.Locations[0].PhysicalLocation
	if loc.ArtifactLocation.URI != "app/main.go" || loc.Region.StartLine != 3 {
		t.Errorf("sarif location = %+v; want app/main.go:3", loc)
	}
}

// assertScanGate pins the passing gate over the scan bundle.
func assertScanGate(t *testing.T) {
	t.Helper()
	gate := runOne(t, "gate", "acme/app", "local-flow", "--max-severity", "critical", "--json")
	if gate.code != cli.ExitOK {
		t.Fatalf("gate = %+v; want a passing gate\n%s%s", gate, gate.out, gate.err)
	}
	requireContains(t, "gate envelope", gate.out, `"passed": true`)
}

// TestScanFlowsThroughAnUnbornRepository proves a scan needs no commit at all:
// a fresh `git init` with files and no HEAD builds, records, reports, exports
// SARIF, and gates over the empty base it recorded.
func TestScanFlowsThroughAnUnbornRepository(t *testing.T) {
	hermeticEnv(t)
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	scanWrite(t, filepath.Join(root, "app", "main.go"), "package main\n\nfunc main() { println(1) }\n")
	t.Chdir(root)

	if got := runOne(t, "fetch-local", "acme/app", "unborn", "--scan", "app"); got.code != cli.ExitOK {
		t.Fatalf("fetch-local --scan in an unborn repo = %+v; want success\n%s", got, got.err)
	}
	bundle := scanBundle(root, "unborn")
	requireContains(t, "source.json", mustRead(t, filepath.Join(bundle, "source.json")),
		`"kind": "scan"`, `"base_sha": null`, `"head_sha": null`)
	writeUnbornReview(t, bundle)

	if got := runOne(t, "record", "acme/app", "local-unborn", "--json"); got.code != cli.ExitOK {
		t.Fatalf("record on an unborn scan = %+v; want success\n%s", got, got.err)
	}
	rec := mustRead(t, filepath.Join(bundle, "record.json"))
	requireContains(t, "record.json", rec, `"status": "local"`, `"gaps":0`,
		`"head": "dirty"`)
	if got := runOne(t, "report", "acme/app", "local-unborn", "--sarif",
		filepath.Join(t.TempDir(), "unborn.sarif"), "--json"); got.code != cli.ExitOK {
		t.Fatalf("report --sarif on an unborn scan = %+v; want success\n%s", got, got.err)
	}
	gate := runOne(t, "gate", "acme/app", "local-unborn", "--max-severity", "critical", "--json")
	if gate.code != cli.ExitOK {
		t.Fatalf("gate on an unborn scan = %+v; want a passing gate\n%s%s", gate, gate.out, gate.err)
	}
	requireContains(t, "gate envelope", gate.out, `"passed": true`)
}

// writeUnbornReview writes a one-file review the scan can attest.
func writeUnbornReview(t *testing.T, bundle string) {
	t.Helper()
	writeIn(t, filepath.Join(bundle, "review.json"), `{"schema_version":"1","summary":"one file",`+
		`"findings":[],`+
		`"coverage":[{"file":"app/main.go","ranges":"all"}],`+
		`"receipts":[{"hunk":"app/main.go\tNEW\t1\t3","quote":"package main"}],"spec_checks":[]}`+"\n")
}
