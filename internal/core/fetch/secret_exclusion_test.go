package fetchcore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/exclude"
	"github.com/stainless-code/verdict/internal/jq"
)

// The three secret sections below name the shapes STA-478 protects: the
// environment document, a TLS key pair, and an SSH private key. Each is written
// as a literal so a defect in the built-in list reproduces here as a mismatch,
// not as agreement with itself.
const (
	envSection     = "diff --git a/.env b/.env\n--- a/.env\n+++ b/.env\n@@ -1 +1 @@\n-A=1\n+A=2\n"
	pemSection     = "diff --git a/certs/server.pem b/certs/server.pem\n--- a/certs/server.pem\n+++ b/certs/server.pem\n@@ -1 +1 @@\n-a\n+b\n"
	idRSASection   = "diff --git a/keys/id_rsa b/keys/id_rsa\n--- a/keys/id_rsa\n+++ b/keys/id_rsa\n@@ -1 +1 @@\n-a\n+b\n"
	renamedSection = "diff --git a/.env b/safe.txt\n--- a/.env\n+++ b/safe.txt\n@@ -1 +1 @@\n-A=1\n+A=2\n"
)

// TestSecretExclusionsSurviveEveryOverride proves the withholding floor: a
// negation in the ignore file, a CLI negation, and the --no-default-excludes
// opt-out all leave every secret section withheld, and a rename out of a
// secret path is withheld too.
func TestSecretExclusionsSurviveEveryOverride(t *testing.T) {
	raw := envSection + pemSection + idRSASection + renamedSection + keptSection
	negations, err := exclude.ParseIgnore([]byte("!.env\n!**/*.pem\n!**/id_rsa\n!safe.txt\n"), "ignore")
	if err != nil {
		t.Fatal(err)
	}
	assertSecretFloor(t, raw, nil, negations)
	assertSecretFloor(t, raw, exclude.Defaults(), negations)
	assertSecretFloor(t, raw, nil, cliNegations(t))
}

// cliNegations parses the same negations under the CLI source, so the receipt
// ids name the layer the operator typed them in.
func cliNegations(t *testing.T) []exclude.Rule {
	t.Helper()
	rules, err := exclude.ParseIgnore([]byte("!.env\n!**/*.pem\n!**/id_rsa\n!safe.txt\n"), "cli")
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// assertSecretFloor proves one override combination keeps only the test
// section and receipts every secret path as the built-in floor.
func assertSecretFloor(t *testing.T, raw string, defaults, explicit []exclude.Rule) {
	t.Helper()
	filtered, receipt := excludedDiff(raw, defaults, explicit, nil)
	if filtered != keptSection {
		t.Fatalf("filtered = %q; want only the test section %q", filtered, keptSection)
	}
	var paths []string
	for _, row := range receipt.Excluded {
		paths = append(paths, row.Path+"|"+row.RuleID+"|"+row.Reason)
	}
	want := []string{
		".env|default:secret|secret-builtin",
		"safe.txt|default:secret|secret-builtin",
		"certs/server.pem|default:secret|secret-builtin",
		"keys/id_rsa|default:secret|secret-builtin",
	}
	slices.Sort(want)
	slices.Sort(paths)
	if !slices.Equal(paths, want) {
		t.Fatalf("excluded = %v; want %v", paths, want)
	}
	if got, wantBytes := receipt.ExcludedBytes, len(raw)-len(keptSection); got != wantBytes {
		t.Fatalf("excluded bytes = %d; want %d", got, wantBytes)
	}
}

// TestSecretExclusionsKeepEnvTemplates proves the deliberate exception: the
// five documented `.env` template spellings are key-name documentation and stay
// reviewable at any depth, while the value-carrying spellings do not.
func TestSecretExclusionsKeepEnvTemplates(t *testing.T) {
	for path, wantIn := range map[string]bool{
		".env":                false,
		".env.local":          false,
		"app/.env":            false,
		"app/.env.production": false,
		".env.example":        true,
		".env.sample":         true,
		".env.template":       true,
		".env.dist":           true,
		".env.defaults":       true,
		"app/.env.example":    true,
		"keys/id_rsa.pub":     true,
	} {
		assertTemplateReviewable(t, path, wantIn)
	}
}

// assertTemplateReviewable proves one path is kept or withheld by the built-in
// secret set alone: no policy default, no ignore file, no attributes.
func assertTemplateReviewable(t *testing.T, path string, wantIn bool) {
	t.Helper()
	section := "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path +
		"\n@@ -1 +1 @@\n-a\n+b\n"
	filtered, receipt := excludedDiff(section, nil, nil, nil)
	if wantIn {
		if filtered != section || len(receipt.Excluded) != 0 {
			t.Fatalf("%s: filtered = %q excluded = %v; want kept", path, filtered, receipt.Excluded)
		}
		return
	}
	if filtered != "" || len(receipt.Excluded) != 1 || receipt.Excluded[0].RuleID != "default:secret" {
		t.Fatalf("%s: filtered = %q excluded = %v; want withheld as default:secret", path, filtered, receipt.Excluded)
	}
}

// TestFetchPreviewLeavesNoBundle proves two promises at once: the preview
// reports the same exclusion the run would apply, and it writes nothing — the
// reviewed repository has no .verdict tree afterwards and the staging
// directory it used is gone.
func TestFetchPreviewLeavesNoBundle(t *testing.T) {
	repoDir := t.TempDir()
	staging := t.TempDir()
	t.Chdir(repoDir)
	t.Setenv("TMPDIR", staging)
	cmdr := fetchHappyCommander(t)
	cmdr.out["gh\x00pr\x00diff\x007\x00--repo\x00o/r"] = scriptResult{stdout: envSection + fetchTestDiff()}
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	req.Flags.Preview = true
	resp, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run preview: %v", err)
	}
	preview := resp.Data.Preview
	if preview == nil {
		t.Fatal("preview payload missing")
	}
	if got := previewRows(preview); !slices.Equal(got, []string{"a.txt|docs|69"}) {
		t.Fatalf("included = %v; want [a.txt|docs|69]", got)
	}
	if got := previewExcludedRows(preview); !slices.Equal(got, []string{".env|default:secret|secret-builtin"}) {
		t.Fatalf("excluded = %v; want [.env|default:secret|secret-builtin]", got)
	}
	if preview.Budget.Tier != "light" || preview.Budget.Finders != 1 || preview.Budget.CrossCheck != "off" {
		t.Fatalf("budget = %+v; want light/1/off", preview.Budget)
	}
	if !slices.Equal(preview.Budget.Reasons, []string{"docs-or-test-only"}) {
		t.Fatalf("budget reasons = %v; want [docs-or-test-only]", preview.Budget.Reasons)
	}
	assertNoBundleWritten(t, repoDir, staging)
}

// TestFetchPreviewJSONEnvelope proves the --json shape: one envelope whose data
// is the preview payload alone, with the withheld section, the byte totals, and
// the resolved tier.
func TestFetchPreviewJSONEnvelope(t *testing.T) {
	repoDir := t.TempDir()
	staging := t.TempDir()
	t.Chdir(repoDir)
	t.Setenv("TMPDIR", staging)
	cmdr := fetchHappyCommander(t)
	cmdr.out["gh\x00pr\x00diff\x007\x00--repo\x00o/r"] = scriptResult{stdout: envSection + fetchTestDiff()}
	decoded := decodePreview(t, runPreview(t, cmdr))
	assertPreviewTotals(t, &decoded)
	assertPreviewExcludedWire(t, decoded.Excluded)
	assertPreviewBudgetWire(t, decoded.Budget)
	assertNoBundleWritten(t, repoDir, staging)
}

// assertPreviewTotals proves the payload's scalars and its single included row.
func assertPreviewTotals(t *testing.T, wire *previewWire) {
	t.Helper()
	if wire.SchemaVersion != "1" || wire.Files != 1 {
		t.Fatalf("preview scalars = %+v; want schema 1 and 1 file", wire)
	}
	if wire.IncludedBytes != 69 || wire.ExcludedBytes != 69 {
		t.Fatalf("bytes = %d included / %d excluded; want 69/69", wire.IncludedBytes, wire.ExcludedBytes)
	}
	if len(wire.Included) != 1 || wire.Included[0].Path != "a.txt" ||
		wire.Included[0].Role != "docs" || wire.Included[0].Bytes != 69 {
		t.Fatalf("included = %+v; want a.txt docs 69", wire.Included)
	}
}

// assertPreviewBudgetWire proves the classifier decision reached the wire.
func assertPreviewBudgetWire(t *testing.T, budget previewWireBudget) {
	t.Helper()
	if budget.Tier != "light" || budget.Finders != 1 || budget.CrossCheck != "off" ||
		!slices.Equal(budget.Reasons, []string{"docs-or-test-only"}) {
		t.Fatalf("budget = %+v", budget)
	}
}

// runPreview drives one preview fetch through the scripted commander.
func runPreview(t *testing.T, cmdr *scriptCommander) *Response {
	t.Helper()
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	req.Flags.Preview = true
	resp, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run preview: %v", err)
	}
	return resp
}

// decodePreview renders the wire payload the envelope carries and decodes it.
func decodePreview(t *testing.T, resp *Response) previewWire {
	t.Helper()
	wire := jq.PrettyOrdered(resp.Data.Wire())
	var doc struct {
		Preview previewWire `json:"preview"`
	}
	if err := json.Unmarshal([]byte(wire), &doc); err != nil {
		t.Fatalf("decode preview wire: %v (%s)", err, wire)
	}
	return doc.Preview
}

// assertPreviewExcludedWire proves the withheld row carries the secret floor's
// path, rule, pattern, reason, bytes, and rename source.
func assertPreviewExcludedWire(t *testing.T, rows []previewWireRow) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("excluded = %+v; want one row", rows)
	}
	row := rows[0]
	if row.Path != ".env" || row.RuleID != "default:secret" || row.Pattern != ".env" ||
		row.Reason != "secret-builtin" || row.Bytes != len(envSection) {
		t.Fatalf("excluded row = %+v", row)
	}
	if row.OldPath == nil || *row.OldPath != ".env" {
		t.Fatalf("old_path = %v; want .env", row.OldPath)
	}
}

// assertNoBundleWritten proves the preview left the reviewed repository and the
// staging root untouched: no .verdict tree, and no scratch directory behind.
func assertNoBundleWritten(t *testing.T, repoDir, staging string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoDir, ".verdict")); !os.IsNotExist(err) {
		t.Fatalf(".verdict after preview: err = %v; want no bundle directory", err)
	}
	left, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range left {
		names = append(names, entry.Name())
	}
	if len(names) != 0 {
		t.Fatalf("preview left %v in its staging root; want nothing", names)
	}
}

// previewRows renders the included rows as path|role|bytes literals.
func previewRows(p *core.PreviewData) []string {
	var out []string
	for _, f := range p.Included {
		out = append(out, f.Path+"|"+f.Role+"|"+strconv.Itoa(f.Bytes))
	}
	return out
}

// previewExcludedRows renders the withheld rows as path|rule|reason literals.
func previewExcludedRows(p *core.PreviewData) []string {
	var out []string
	for _, e := range p.Excluded {
		out = append(out, e.Path+"|"+e.RuleID+"|"+e.Reason)
	}
	return out
}

// TestReviewedRowsMergeOnePath proves a path with two diff sections is one
// reviewed file whose bytes both sections count, so a repeated section can
// neither duplicate a row nor inflate the count the budget compares.
func TestReviewedRowsMergeOnePath(t *testing.T) {
	repeated := fetchTestDiff() + fetchTestDiff()
	included, files := reviewedRows(repeated)
	if len(included) != 1 || len(files) != 1 {
		t.Fatalf("rows = %d included / %d classified; want 1/1", len(included), len(files))
	}
	if got, want := included[0].Bytes, len(repeated); got != want {
		t.Fatalf("bytes = %d; want %d (both sections)", got, want)
	}
	if included[0].Path != "a.txt" || included[0].Role != "docs" || files[0].Role != "docs" {
		t.Fatalf("row = %+v / %+v; want a.txt as docs", included[0], files[0])
	}
}

// TestFetchLocalPreviewLeavesNoBundle proves the local verb's preview: the diff
// comes from git, every exclusion applies, and no bundle directory is created.
func TestFetchLocalPreviewLeavesNoBundle(t *testing.T) {
	repoDir := t.TempDir()
	staging := t.TempDir()
	t.Chdir(repoDir)
	t.Setenv("TMPDIR", staging)
	cmdr := stagedLocalCommander(t, envSection+fetchTestDiff())
	var human, diag bytes.Buffer
	var streamed []string
	req := localScriptReq(cmdr, &human, &diag, &streamed, map[string]string{}, LocalFlags{Staged: true, Preview: true})
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal preview: %v", err)
	}
	preview := resp.Data.Preview
	if preview == nil {
		t.Fatal("preview payload missing")
	}
	if got := previewRows(preview); !slices.Equal(got, []string{"a.txt|docs|69"}) {
		t.Fatalf("included = %v; want [a.txt|docs|69]", got)
	}
	if got := previewExcludedRows(preview); !slices.Equal(got, []string{".env|default:secret|secret-builtin"}) {
		t.Fatalf("excluded = %v; want [.env|default:secret|secret-builtin]", got)
	}
	if preview.Budget.Tier != "light" {
		t.Fatalf("tier = %q; want light", preview.Budget.Tier)
	}
	assertNoBundleWritten(t, repoDir, staging)
}

// TestSecretDefaultsReceipt proves the receipt always names the floor rule,
// with the policy defaults and without them.
func TestSecretDefaultsReceipt(t *testing.T) {
	assertReceiptDefaults(t, false, "default:secret,default:generated,default:attribute")
	assertReceiptDefaults(t, true, "default:secret")
}

// assertReceiptDefaults runs one exclusion pass in a fresh directory and
// checks the receipt's defaults list.
func assertReceiptDefaults(t *testing.T, off bool, want string) {
	t.Helper()
	dir := physicalTempDir(t)
	if err := os.WriteFile(dir+"/full.diff", []byte(keptSection), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := []string{}
	fc := &fetchRunCtx{dir: dir, runner: &session{warnings: &warnings}, noDefaultExcludes: off}
	if err := filterExcluded(fc); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt ExcludedFile
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(receipt.Defaults, ","); got != want {
		t.Fatalf("defaults = %q; want %q", got, want)
	}
}

// previewWire is the decoded --json preview payload. The field order is
// irrelevant to the decoder and chosen for alignment.
type previewWire struct { //nolint:govet // decoded field order reads with the wire
	Included      []previewWireFile `json:"included"`
	Excluded      []previewWireRow  `json:"excluded"`
	SchemaVersion string            `json:"schema_version"`
	ExcludedBytes int               `json:"excluded_bytes"`
	IncludedBytes int               `json:"included_bytes"`
	Files         int               `json:"files"`
	Budget        previewWireBudget `json:"budget"`
}

// previewWireFile is one decoded included row.
type previewWireFile struct { //nolint:govet // decoded field order reads with the wire
	Bytes int    `json:"bytes"`
	Path  string `json:"path"`
	Role  string `json:"role"`
}

// previewWireRow is one decoded withheld row.
type previewWireRow struct {
	OldPath *string `json:"old_path"`
	Path    string  `json:"path"`
	RuleID  string  `json:"rule_id"`
	Pattern string  `json:"pattern"`
	Reason  string  `json:"reason"`
	Bytes   int     `json:"bytes"`
}

// previewWireBudget is the decoded classifier decision.
type previewWireBudget struct { //nolint:govet // decoded field order reads with the wire
	Reasons    []string `json:"reasons"`
	Finders    int      `json:"finders"`
	Tier       string   `json:"tier"`
	CrossCheck string   `json:"cross_check"`
}

// vendorSection and authSection are the two policy-withheld paths the PR's own
// file list also names in the test below.
const (
	vendorSection = "diff --git a/vendor/lib.go b/vendor/lib.go\n--- a/vendor/lib.go\n+++ b/vendor/lib.go\n@@ -1 +1 @@\n-a\n+b\n"
	authSection   = "diff --git a/auth/login.go b/auth/login.go\n--- a/auth/login.go\n+++ b/auth/login.go\n@@ -1 +1 @@\n-a\n+b\n"
)

// TestFetchPreviewReadsOnlyTheFilteredDiff proves the preview never re-includes
// a withheld path from the PR's own file list. The staging pass writes PR.json
// before the exclusion filter runs, so PR.json names every file the PR touched,
// `.env` and the policy-excluded paths among them; a preview that read it would
// report a secret as reviewed and classify the review from the PR instead of
// from the diff the reviewer would get.
func TestFetchPreviewReadsOnlyTheFilteredDiff(t *testing.T) {
	repoDir := t.TempDir()
	staging := t.TempDir()
	t.Chdir(repoDir)
	t.Setenv("TMPDIR", staging)
	cmdr := fetchHappyCommander(t)
	cmdr.out["gh\x00"+fetchTestViewArgv()] = scriptResult{stdout: prListingHiddenFiles()}
	cmdr.out["gh\x00pr\x00diff\x007\x00--repo\x00o/r"] = scriptResult{stdout: envSection + vendorSection + authSection + fetchTestDiff()}
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	req.Flags.Preview = true
	req.Flags.Ignore = []string{"auth/**"}
	resp, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run preview: %v", err)
	}
	if resp.Data.Preview == nil {
		t.Fatal("preview payload missing")
	}
	assertFilteredPreview(t, resp.Data.Preview)
	assertNoBundleWritten(t, repoDir, staging)
}

// assertFilteredPreview proves the payload covers the filter's output alone:
// PR.json names four files, three of them withheld, and the review reads one.
func assertFilteredPreview(t *testing.T, preview *core.PreviewData) {
	t.Helper()
	if got := previewRows(preview); !slices.Equal(got, []string{"a.txt|docs|69"}) {
		t.Fatalf("included = %v; want only the filtered a.txt row", got)
	}
	if preview.Files != 1 || preview.IncludedBytes != 69 {
		t.Fatalf("files/bytes = %d/%d; want 1/69 (the PR lists four files)", preview.Files, preview.IncludedBytes)
	}
	wantExcluded := []string{
		".env|default:secret|secret-builtin",
		"vendor/lib.go|default:generated|default",
		"auth/login.go|cli:1|cli",
	}
	if got := previewExcludedRows(preview); !slices.Equal(got, wantExcluded) {
		t.Fatalf("excluded = %v; want %v", got, wantExcluded)
	}
	// The classifier sees one documentation row. Counting the PR's list would
	// read four files with source paths in them, so it could not report a
	// docs-only light tier.
	if preview.Budget.Tier != "light" || !slices.Equal(preview.Budget.Reasons, []string{"docs-or-test-only"}) {
		t.Fatalf("budget = %+v; want light/docs-or-test-only from the filtered diff", preview.Budget)
	}
}

// prListingHiddenFiles is a PR record whose file list still names every path
// the filter withholds.
func prListingHiddenFiles() string {
	return `{"number":7,"title":"T","body":"B","baseRefName":"main","headRefName":"feat",` +
		`"headRefOid":"` + fetchTestHead + `","baseRefOid":"` + fetchTestBase + `",` +
		`"author":{"login":"me"},"url":"https://example/o/r/pull/7","commits":[],` +
		`"files":[{"path":".env","additions":1,"deletions":1},` +
		`{"path":"vendor/lib.go","additions":1,"deletions":1},` +
		`{"path":"auth/login.go","additions":1,"deletions":1},` +
		`{"path":"a.txt","additions":1,"deletions":1}],` +
		`"additions":4,"deletions":4,"isDraft":false,"reviewDecision":"","state":"OPEN"}`
}

// TestFetchPreviewKeepsWarnings proves the preview carries the warnings the run
// raised before the filter. Dropping them would hide why a preview's numbers
// differ from a full fetch, and the operator would lose the signal that the
// controls were resolved from a snapshot rather than the base revision.
func TestFetchPreviewKeepsWarnings(t *testing.T) {
	repoDir := t.TempDir()
	staging := t.TempDir()
	t.Chdir(repoDir)
	t.Setenv("TMPDIR", staging)
	cmdr := fetchHappyCommander(t)
	cmdr.out["gh\x00pr\x00diff\x007\x00--repo\x00o/r"] = scriptResult{stdout: envSection + fetchTestDiff()}
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	req.Flags.Preview = true
	resp, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run preview: %v", err)
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("preview warnings = none; want the run's own warnings")
	}
	if len(streamed) == 0 {
		t.Fatal("streamed warnings = none; want the same warnings on the progress channel")
	}
	if !slices.Equal(resp.Warnings, streamed) {
		t.Fatalf("response warnings = %v; streamed = %v; want one list", resp.Warnings, streamed)
	}
}
