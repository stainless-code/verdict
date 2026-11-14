package learncore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/incident"
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
