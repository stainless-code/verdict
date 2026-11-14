package readcommon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// writeStore writes one fixture file into dir.
func writeStore(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// checkBundleViews reads every accessor once and checks the fixture values.
func checkBundleViews(t *testing.T, b *Bundle) {
	t.Helper()
	checkPrFields(t, b, prFields{head: "aaaa", base: "bbbb", state: "open", auth: "me"})
	if got := b.Provider(); got != "" {
		t.Errorf("Provider = %q, want empty for a GitHub record", got)
	}
	checkReceipt(t, b, receipt{head: "aaaa", at: "2026-01-01T00:00:00Z", headOK: true, atOK: true})
}

// TestBundleReadsEachRecordOnce proves a Bundle decodes PR.json and
// post.json at most once however many fields a run reads: the seam every
// verb now shares, so post/record no longer pay one decode per field.
func TestBundleReadsEachRecordOnce(t *testing.T) {
	dir := t.TempDir()
	writeStore(t, dir, "PR.json", `{"headRefOid":"aaaa","baseRefOid":"bbbb","state":"OPEN",`+
		`"author":{"login":"me"}}`)
	writeStore(t, dir, "post.json", `{"head":"aaaa","posted_at":"2026-01-01T00:00:00Z"}`)

	counts := map[string]int{}
	original := readStoreFile
	readStoreFile = func(path string, maxBytes int64) ([]byte, error) {
		// Key by file name: the bundle joins paths with "/", which differs
		// from filepath.Join's separator on Windows.
		counts[filepath.Base(filepath.FromSlash(path))]++
		return original(path, maxBytes)
	}
	t.Cleanup(func() { readStoreFile = original })

	b := OpenBundle(dir)
	for range 3 {
		checkBundleViews(t, b)
	}

	if got := counts["PR.json"]; got != 1 {
		t.Errorf("PR.json reads = %d, want 1", got)
	}
	if got := counts["post.json"]; got != 1 {
		t.Errorf("post.json reads = %d, want 1", got)
	}
}

// prFields is one expected provider-aware PR record view.
type prFields struct {
	head  string
	base  string
	state string
	auth  string
}

// checkPrFields compares one bundle view against want.
func checkPrFields(t *testing.T, b *Bundle, want prFields) {
	t.Helper()
	if got := b.Head(); got != want.head {
		t.Errorf("Head = %q, want %q", got, want.head)
	}
	if got := b.Base(); got != want.base {
		t.Errorf("Base = %q, want %q", got, want.base)
	}
	if got := b.State(); got != want.state {
		t.Errorf("State = %q, want %q", got, want.state)
	}
	if got := b.Author(); got != want.auth {
		t.Errorf("Author = %q, want %q", got, want.auth)
	}
}

// TestBundleProviderAwareFields proves the provider switch: GitHub records
// read the gh field names, normalized records read the neutral ones, missing
// records read empty, and only the GitHub state is lowercased.
func TestBundleProviderAwareFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record string
		want   prFields
	}{
		{
			name:   "github",
			record: `{"headRefOid":"h","baseRefOid":"b","state":"MERGED","author":{"login":"octo"}}`,
			want:   prFields{head: "h", base: "b", state: "merged", auth: "octo"},
		},
		{
			name:   "neutral",
			record: `{"provider":"gitlab","head_sha":"h","base_sha":"b","state":"Open","author":"gl"}`,
			want:   prFields{head: "h", base: "b", state: "Open", auth: "gl"},
		},
		{name: "missing-record"},
		{name: "non-object", record: `["not","an","object"]`},
		{name: "garbage", record: "not json{{{"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.record != "" {
				writeStore(t, dir, "PR.json", tc.record)
			}
			checkPrFields(t, OpenBundle(dir), tc.want)
		})
	}
}

// TestPrFieldNonStringReadsEmpty pins the coercion every bundle reader now
// shares: only a string field reads a value. A missing or non-string head
// reads empty rather than rendering "null" or the raw number.
func TestPrFieldNonStringReadsEmpty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record string
		want   string
	}{
		{name: "string", record: `{"headRefOid":"abc"}`, want: "abc"},
		{name: "missing", record: `{"state":"OPEN"}`},
		{name: "null", record: `{"headRefOid":null}`},
		{name: "number", record: `{"headRefOid":123}`},
		{name: "object", record: `{"headRefOid":{"sha":"abc"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeStore(t, dir, "PR.json", tc.record)
			if got := PrField(dir, "head_sha"); got != tc.want {
				t.Errorf("PrField head_sha = %q, want %q", got, tc.want)
			}
		})
	}
}

// receipt is one expected post-receipt view.
type receipt struct {
	head   string
	at     string
	headOK bool
	atOK   bool
}

// checkReceipt compares one bundle's post receipt against want.
func checkReceipt(t *testing.T, b *Bundle, want receipt) {
	t.Helper()
	head, headOK := b.PostedHead()
	if head != want.head || headOK != want.headOK {
		t.Errorf("PostedHead = %q, %v; want %q, %v", head, headOK, want.head, want.headOK)
	}
	at, atOK := b.PostedAt()
	if at != want.at || atOK != want.atOK {
		t.Errorf("PostedAt = %q, %v; want %q, %v", at, atOK, want.at, want.atOK)
	}
}

// TestBundlePostedReceipt proves the post receipt accessors keep the frozen
// `// empty` and `// "unknown time"` shapes: a JSON null record is readable
// with no head, a non-object or garbage record is unreadable, and an explicit
// (even empty) posted_at is present so the caller keeps its own default.
func TestBundlePostedReceipt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record string
		want   receipt
	}{
		{name: "missing"},
		{name: "garbage", record: "not json{{{"},
		{name: "null", record: "null", want: receipt{headOK: true}},
		{name: "headless", record: "{}", want: receipt{headOK: true}},
		{name: "head", record: `{"head":"abc"}`, want: receipt{head: "abc", headOK: true}},
		{
			name:   "posted",
			record: `{"head":"abc","posted_at":"t"}`,
			want:   receipt{head: "abc", at: "t", headOK: true, atOK: true},
		},
		{
			name:   "explicit-empty",
			record: `{"posted_at":""}`,
			want:   receipt{headOK: true, atOK: true},
		},
		{name: "false", record: `{"posted_at":false}`, want: receipt{headOK: true}},
		{name: "array", record: "[]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.record != "" {
				writeStore(t, dir, "post.json", tc.record)
			}
			checkReceipt(t, OpenBundle(dir), tc.want)
		})
	}
}

// marker is one expected provider-marker view.
type marker struct {
	value   string
	str     string
	present bool
}

// checkMarker compares one bundle's provider reads against want.
func checkMarker(t *testing.T, b *Bundle, want marker) {
	t.Helper()
	got, present := b.ProviderMarker()
	if got != want.value || present != want.present {
		t.Errorf("ProviderMarker = %q, %v; want %q, %v", got, present, want.value, want.present)
	}
	if got := b.Provider(); got != want.str {
		t.Errorf("Provider = %q, want %q", got, want.str)
	}
}

// TestBundleProviderMarker proves the routing marker distinguishes absent
// from present-but-not-a-string: post and adopt default only a missing marker
// to github, while record's string-only read refuses anything exotic.
func TestBundleProviderMarker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record string
		want   marker
	}{
		{name: "missing", record: "{}"},
		{name: "null", record: `{"provider":null}`},
		{name: "false", record: `{"provider":false}`},
		{name: "github", record: `{"provider":"github"}`, want: marker{value: "github", str: "github", present: true}},
		{name: "number", record: `{"provider":123}`, want: marker{value: "123", present: true}},
		{name: "explicit-empty", record: `{"provider":""}`, want: marker{present: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeStore(t, dir, "PR.json", tc.record)
			checkMarker(t, OpenBundle(dir), tc.want)
		})
	}
}

// TestBundleOverCapReadsEmpty proves PRE-01's cap still applies through the
// shared Bundle: an over-cap PR record is unreadable, not allocated.
func TestBundleOverCapReadsEmpty(t *testing.T) {
	// A sparse file keeps the >8 MiB fixture cheap on disk.
	dir := t.TempDir()
	path := filepath.Join(dir, "PR.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(jq.MaxPRBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b := OpenBundle(dir)
	if got := b.Head(); got != "" {
		t.Errorf("Head over cap = %q, want empty", got)
	}
	if got, present := b.ProviderMarker(); got != "" || present {
		t.Errorf("ProviderMarker over cap = %q, %v; want absent", got, present)
	}
}

func TestReceiptIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "concrete-head", raw: `{"head":"aaaa"}`, want: "aaaa"},
		{name: "missing-head-with-digest", raw: `{"diff_sha256":"deadbeef"}`, want: "diff:deadbeef"},
		{name: "null-head-with-digest", raw: `{"head":null,"diff_sha256":"deadbeef"}`, want: "diff:deadbeef"},
		{name: "dirty-head-missing-digest", raw: `{"head":"dirty"}`, want: "dirty"},
		{name: "dirty-head-null-digest", raw: `{"head":"dirty","diff_sha256":null}`, want: "dirty"},
		{name: "missing-both", raw: `{}`, want: ""},
		{name: "null-both", raw: `{"head":null,"diff_sha256":null}`, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkReceiptIdentity(t, tc.raw, tc.want)
		})
	}
}

func checkReceiptIdentity(t *testing.T, raw, want string) {
	t.Helper()
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatal("expected map")
	}
	if got := ReceiptIdentity(m); got != want {
		t.Errorf("ReceiptIdentity(%s) = %q, want %q", raw, got, want)
	}
}
