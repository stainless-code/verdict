package learncore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/incident"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	incidentSha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// incidentLedgerRow is the exact line an attach of local-a/src/a.ts writes.
	incidentLedgerRow = `{"schema_version":"1","bundle":"local-a","sha":"` + incidentSha + `",` +
		`"paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-14T00:00:00Z"}` + "\n"
)

// TestIncidentRefusesMissingBundle proves an attach needs a recorded review:
// an unresolvable name, an absent directory, and a bundle without
// findings.json each refuse typed and leave the ledger untouched.
func TestIncidentRefusesMissingBundle(t *testing.T) {
	for _, tc := range []struct {
		bundle, wantRef string
		seedDir         bool
	}{
		{bundle: "nosuch", wantRef: "missing-bundle"},
		{bundle: "local-gone", wantRef: "missing-bundle"},
		{bundle: "local-empty", wantRef: "missing-findings", seedDir: true},
	} {
		t.Run(tc.bundle, func(t *testing.T) { assertMissingRefusal(t, tc.bundle, tc.wantRef, tc.seedDir) })
	}
}

// assertMissingRefusal proves one unattachable bundle refuses typed and leaves
// the ledger absent; seedDir lands the bundle directory without a store.
func assertMissingRefusal(t *testing.T, bundle, wantRef string, seedDir bool) {
	t.Helper()
	root := seedIncidentRepo(t)
	if seedDir {
		writeIncidentBundle(t, root, bundle, "")
	}
	resp, ref := Run(t.Context(), incidentRequest(root, func(req *Request) {
		req.Incident = bundle
	}))
	if ref == nil {
		t.Fatalf("Run returned %+v; want refusal %s", resp, wantRef)
	}
	if ref.Code != wantRef {
		t.Fatalf("refusal code = %q (%s); want %q", ref.Code, ref.Message, wantRef)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict", "incidents.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a refused attach wrote the ledger (stat err %v)", err)
	}
}

// TestIncidentFlagsNeedTheirMode proves the attach flags refuse beside another
// mode (one mode is already selected, so the count check cannot answer) and
// name the flag they belong to.
func TestIncidentFlagsNeedTheirMode(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	_, ref := Run(t.Context(), incidentRequest(root, func(req *Request) {
		req.Status = true
		req.Incident = ""
	}))
	want := "learn: --sha, --path, --note, --url, and --at belong to --incident <bundle>"
	if ref == nil || ref.Code != "bad-flags" || ref.Message != want {
		t.Fatalf("refusal = %v; want bad-flags %q", ref, want)
	}
}

// TestIncidentRefusesBadEvidence proves the note and url guards refuse typed
// without touching the ledger.
func TestIncidentRefusesBadEvidence(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	long := strings.Repeat("n", incidentNoteMaxBytes+1)
	assertBadAttach(t, root, "note over the cap", "bad-flags", func(req *Request) { req.Note = long })
	assertBadAttach(t, root, "url without a scheme", "bad-flags", func(req *Request) { req.URL = "example.test/inc/1" })
	assertBadAttach(t, root, "url with another scheme", "bad-flags", func(req *Request) { req.URL = "ftp://example.test/inc/1" })
	assertBadAttach(t, root, "at that is not RFC 3339", "bad-at", func(req *Request) { req.At = "2026-11-01" })
}

// assertBadAttach runs one attach spelling and proves it refuses with code.
func assertBadAttach(t *testing.T, root, label, code string, use func(*Request)) {
	t.Helper()
	if _, ref := Run(t.Context(), incidentRequest(root, use)); ref == nil || ref.Code != code {
		t.Fatalf("%s: refusal = %v; want %s", label, ref, code)
	}
}

// TestIncidentRefusesScopeEscape proves an incident path is held to the same
// clean repo-relative rule as every other learning path.
func TestIncidentRefusesScopeEscape(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	_, ref := Run(t.Context(), incidentRequest(root, func(req *Request) {
		req.Paths = []string{"../etc/passwd"}
	}))
	if ref == nil || ref.Code != "bad-scope" {
		t.Fatalf("refusal = %v; want bad-scope", ref)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict", "incidents.jsonl")); !os.IsNotExist(err) {
		t.Fatal("the refused attach wrote the ledger")
	}
}

// TestIncidentBusyRefusalIsTyped proves a ledger another attach holds refuses
// as a retryable store state, not as a failed write.
func TestIncidentBusyRefusalIsTyped(t *testing.T) {
	st := &state{root: "/repo"}
	ref := st.incidentRefusal(incident.ErrBusy)
	want := "incident ledger /repo/.verdict/incidents.jsonl is being written by another attach (retry in a moment)"
	if ref == nil || ref.Code != "store-busy" || ref.Message != want {
		t.Fatalf("refusal = %v; want store-busy %q", ref, want)
	}
}

// TestLedgerTooLargeRefusalIsTyped proves an append past the ledger's read cap
// refuses as its own code: a generic failed write would leave the operator
// re-running an attach that can never fit.
func TestLedgerTooLargeRefusalIsTyped(t *testing.T) {
	st := &state{root: "/repo"}
	ref := st.incidentRefusal(fmt.Errorf("upsert: %w", incident.ErrTooLarge))
	want := "refusing learn: /repo/.verdict/incidents.jsonl would pass the 8388608-byte read cap " +
		"(trim rows from the ledger first)"
	if ref == nil || ref.Code != "ledger-too-large" || ref.Message != want {
		t.Fatalf("refusal = %v; want ledger-too-large %q", ref, want)
	}
}

// TestIncidentDryRunWritesNothing proves --dry-run reports the append it would
// make and touches no file.
func TestIncidentDryRunWritesNothing(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	resp, ref := Run(t.Context(), incidentRequest(root, func(req *Request) { req.DryRun = true }))
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "attached"); got != "appended" {
		t.Fatalf("attached = %q; want appended", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict", "incidents.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote the ledger (stat err %v)", err)
	}
}

// TestIncidentNoteStaysOnOneLine proves a control-bearing note cannot break
// the ledger's one-row-per-line contract: the note is stripped, and the stored
// line is exactly the row an operator would have written.
func TestIncidentNoteStaysOnOneLine(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	resp, ref := Run(t.Context(), incidentRequest(root, func(req *Request) {
		req.Note = "reverted\nafter the hour\ttab"
	}))
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "note"); got != "revertedafter the hourtab" {
		t.Fatalf("note = %q; want the controls stripped", got)
	}
	want := `{"schema_version":"1","bundle":"local-a","sha":"` + incidentSha + `",` +
		`"paths":["src/a.ts"],"note":"revertedafter the hourtab","url":null,` +
		`"detected_at":"2026-11-14T00:00:00Z"}` + "\n"
	if got := readIncidentFile(t, filepath.Join(root, ".verdict", "incidents.jsonl")); got != want {
		t.Fatalf("ledger = %q; want %q", got, want)
	}
}

// TestIncidentExistingReportsTheStoredRow proves a repeat attach answers with
// the ledger's row rather than the request's: the second run asks for another
// path, note, url, and time, and both the write and the dry run report what was
// actually stored.
func TestIncidentExistingReportsTheStoredRow(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	if _, ref := Run(t.Context(), incidentRequest(root, nil)); ref != nil {
		t.Fatalf("first attach refusal %s: %s", ref.Code, ref.Message)
	}
	path := filepath.Join(root, ".verdict", "incidents.jsonl")
	first := readIncidentFile(t, path)

	for _, dry := range []bool{false, true} {
		resp, ref := Run(t.Context(), incidentRequest(root, func(req *Request) {
			req.DryRun = dry
			req.Paths = []string{"src/other.ts"}
			req.Note = "changed in a later run"
			req.URL = "https://example.test/inc/2"
			req.At = "2030-01-01T00:00:00Z"
		}))
		if ref != nil {
			t.Fatalf("dry=%v refusal %s: %s", dry, ref.Code, ref.Message)
		}
		assertStoredRow(t, resp, dry)
	}
	if got := readIncidentFile(t, path); got != first {
		t.Fatalf("the repeat attach moved the ledger: %q", got)
	}
}

// assertStoredRow pins one repeat-attach response to the row the first attach
// stored.
func assertStoredRow(t *testing.T, resp *Response, dry bool) {
	t.Helper()
	if got := incidentField(t, resp, "attached"); got != "existing" {
		t.Fatalf("dry=%v attached = %q; want existing", dry, got)
	}
	if got := incidentField(t, resp, "detected_at"); got != "2026-11-14T00:00:00Z" {
		t.Fatalf("dry=%v detected_at = %q; want the stored time", dry, got)
	}
	if got := strings.Join(incidentList(t, resp, "paths"), ","); got != "src/a.ts" {
		t.Fatalf("dry=%v paths = %q; want the stored path", dry, got)
	}
	for _, key := range []string{"note", "url"} {
		if got, ok := resp.Data.Incident.Get(key); !ok || got != nil {
			t.Fatalf("dry=%v %s = %v; want the stored null", dry, key, got)
		}
	}
}

// incidentList reads one array field of data.incident.
func incidentList(t *testing.T, resp *Response, key string) []string {
	t.Helper()
	value, ok := resp.Data.Incident.Get(key)
	if !ok {
		t.Fatalf("data.incident carries no %q", key)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("data.incident.%s = %T; want an array", key, value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("data.incident.%s item = %T; want a string", key, item)
		}
		out = append(out, text)
	}
	return out
}

// TestIncidentDryRunProjectsTheReadCap proves `--dry-run` refuses what the
// write refuses: with the ledger 64 bytes under the 8 MiB read cap, both the
// dry run and the real attach answer `ledger-too-large`, so the preview never
// promises an append the write cannot make.
func TestIncidentDryRunProjectsTheReadCap(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	path := filepath.Join(root, ".verdict", "incidents.jsonl")
	seeded := ledgerNearCap()
	if err := os.WriteFile(path, []byte(seeded), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		_, ref := Run(t.Context(), incidentRequest(root, func(req *Request) { req.DryRun = dry }))
		if ref == nil || ref.Code != "ledger-too-large" {
			t.Fatalf("dry=%v refusal = %v; want ledger-too-large", dry, ref)
		}
	}
	if got := readIncidentFile(t, path); got != seeded {
		t.Fatal("a refused attach moved the ledger")
	}
}

// ledgerNearCap is a readable ledger whose next row passes the read cap: one
// row for another bundle, 64 bytes short of jq.MaxKBBytes.
func ledgerNearCap() string {
	empty := `{"schema_version":"1","bundle":"local-b","sha":"` + strings.Repeat("b", 40) + `",` +
		`"paths":["src/b.ts"],"note":"","url":null,"detected_at":"2026-11-14T00:00:00Z"}` + "\n"
	note := strings.Repeat("n", int(jq.MaxKBBytes)-len(empty)-64)
	return strings.Replace(empty, `"note":""`, `"note":"`+note+`"`, 1)
}

// TestIncidentRecordsTheResolvedBundle proves an alias and the bundle it names
// are one ledger pair: attaching `--incident a` stores `local-a`, so the same
// merge attached by both spellings keeps one row and the join measures it once.
func TestIncidentRecordsTheResolvedBundle(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	resp, ref := Run(t.Context(), incidentRequest(root, func(req *Request) { req.Incident = "a" }))
	if ref != nil {
		t.Fatalf("alias refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "bundle"); got != "local-a" {
		t.Fatalf("alias attach stored bundle %q; want local-a", got)
	}
	if got := incidentField(t, resp, "attached"); got != "appended" {
		t.Fatalf("alias attach = %q; want appended", got)
	}

	resp, ref = Run(t.Context(), incidentRequest(root, func(req *Request) { req.Incident = "local-a" }))
	if ref != nil {
		t.Fatalf("bundle refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "attached"); got != "existing" {
		t.Fatalf("the same pair by its own name = %q; want existing", got)
	}
	want := `{"schema_version":"1","bundle":"local-a","sha":"` + incidentSha + `",` +
		`"paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-14T00:00:00Z"}` + "\n"
	if got := readIncidentFile(t, filepath.Join(root, ".verdict", "incidents.jsonl")); got != want {
		t.Fatalf("ledger = %q; want one local-a row", got)
	}
}

// TestIncidentBringTheLockLineToTheIgnoreFile proves the attach leaves the
// worktree clean: `.verdict/.gitignore` gains the lock-file line a checkout
// fetched before it existed lacks, and a dry run writes nothing.
func TestIncidentBringTheLockLineToTheIgnoreFile(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	ignore := filepath.Join(root, ".verdict", ".gitignore")
	if err := os.WriteFile(ignore, []byte("/state/\n/backtest/\n/index/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ref := Run(t.Context(), incidentRequest(root, func(req *Request) { req.DryRun = true })); ref != nil {
		t.Fatalf("dry-run refusal %s: %s", ref.Code, ref.Message)
	}
	if got := readIncidentFile(t, ignore); got != "/state/\n/backtest/\n/index/\n" {
		t.Fatalf("--dry-run rewrote the ignore file: %q", got)
	}
	if _, ref := Run(t.Context(), incidentRequest(root, nil)); ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	want := "/state/\n/backtest/\n/index/\nincidents.jsonl.lock\nkb.jsonl.lock\n"
	if got := readIncidentFile(t, ignore); got != want {
		t.Fatalf("ignore = %q; want %q", got, want)
	}
}

// TestIncidentSecondAttachIsExisting proves the attach is idempotent: the
// second run reports the existing row and leaves the ledger byte-identical.
func TestIncidentSecondAttachIsExisting(t *testing.T) {
	root := seedIncidentRepo(t)
	writeIncidentBundle(t, root, "local-a", "[]")
	resp, ref := Run(t.Context(), incidentRequest(root, nil))
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "attached"); got != "appended" {
		t.Fatalf("first attach = %q; want appended", got)
	}
	path := filepath.Join(root, ".verdict", "incidents.jsonl")
	first := readIncidentFile(t, path)
	if first != incidentLedgerRow {
		t.Fatalf("ledger = %q; want %q", first, incidentLedgerRow)
	}
	resp, ref = Run(t.Context(), incidentRequest(root, nil))
	if ref != nil {
		t.Fatalf("second refusal %s: %s", ref.Code, ref.Message)
	}
	if got := incidentField(t, resp, "attached"); got != "existing" {
		t.Fatalf("second attach = %q; want existing", got)
	}
	if got := readIncidentFile(t, path); got != first {
		t.Fatalf("second attach moved the ledger bytes: %q", got)
	}
}

// incidentRequest builds one attach of local-a at src/a.ts.
func incidentRequest(root string, mutate func(*Request)) *Request {
	req := &Request{Incident: "local-a", Sha: incidentSha, Paths: []string{"src/a.ts"}}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Now: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }},
		RepoRoot: root,
	}
	if mutate != nil {
		mutate(req)
	}
	return req
}

// seedIncidentRepo makes a repository root carrying the .verdict directory.
func seedIncidentRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeIncidentBundle lands one bundle directory, with findings.json when the
// caller passes a document.
func writeIncidentBundle(t *testing.T, root, name, findings string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if findings == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(findings), 0o600); err != nil {
		t.Fatal(err)
	}
}

func incidentField(t *testing.T, resp *Response, key string) string {
	t.Helper()
	if resp.Data == nil || resp.Data.Incident == nil {
		t.Fatal("response carries no data.incident")
	}
	value, ok := resp.Data.Incident.Get(key)
	if !ok {
		t.Fatalf("data.incident carries no %q", key)
	}
	s, ok := value.(string)
	if !ok {
		t.Fatalf("data.incident.%s = %T; want a string", key, value)
	}
	return s
}

func readIncidentFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
