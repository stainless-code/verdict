package jq_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// benchFindingsBudget is the document the canonicalization benchmark holds: an
// eighth of [jq.MaxFindingsBytes], the cap every store read accepts. A document
// at the full cap decodes for seconds, which is a profile, not a benchmark.
const benchFindingsBudget = int(jq.MaxFindingsBytes / 8)

// benchFinding renders one findings record at review scale.
func benchFinding(i int) string {
	return `{"kind":"finding","path":"internal/core/report/overview.go","side":"RIGHT",` +
		`"line":` + strconv.Itoa(40+i) + `,"severity":"high","rigor":"logical",` +
		`"body":"The loop walks every hunk once per file, so the collapsed table costs quadratic time in the diff size.",` +
		`"suggestion":null,"inBounds":true,"status":"open","sig":"abcdef0","history":[]}`
}

// BenchmarkCanonicalizeFindings measures both halves of the store round trip:
// decoding a findings document into insertion-ordered values, and re-rendering
// it compact. One fixture backs both, held to the byte budget above, so the
// reported bytes/op is a document the product accepts.
func BenchmarkCanonicalizeFindings(b *testing.B) {
	raw, ordered := benchStoreDoc(b)
	b.Run("decode", func(b *testing.B) { benchDecode(b, raw) })
	b.Run("compact", func(b *testing.B) { benchCompact(b, ordered, len(raw)) })
}

// benchStoreDoc builds the fixture and decodes it once, so neither timed loop
// pays for the other's work.
func benchStoreDoc(b *testing.B) ([]byte, *jq.Obj) {
	b.Helper()
	raw := benchFindingsDoc(b, benchFindingsBudget)
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		b.Fatalf("decode the fixture: %v", err)
	}
	ordered, ok := doc.(*jq.Obj)
	if !ok {
		b.Fatalf("fixture decoded to %T, want the ordered object the store reader returns", doc)
	}
	return raw, ordered
}

// benchDecode times the read half: one document into ordered values.
func benchDecode(b *testing.B, raw []byte) {
	b.Helper()
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jq.DecodeOrdered(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// benchCompact times the write half: ordered values back to compact bytes.
func benchCompact(b *testing.B, ordered *jq.Obj, size int) {
	b.Helper()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for b.Loop() {
		if ordered.MarshalCompact() == "" {
			b.Fatal("MarshalCompact rendered nothing")
		}
	}
}

// benchFindingsDoc renders a findings-shaped document of exactly budget bytes:
// real records up to the budget, then a note carrying whatever padding the
// budget leaves, so the fixture's size is arithmetic rather than a guess.
func benchFindingsDoc(tb testing.TB, budget int) []byte {
	tb.Helper()
	head, closer := `{"findings":[`, len(`],"note":""}`)
	body := benchFindingsBody(tb, budget-len(head)-closer)
	pad := budget - len(head) - len(body) - closer
	if pad < 0 {
		tb.Fatalf("budget %d leaves no room for one record", budget)
	}
	doc := head + body + `],"note":"` + strings.Repeat("x", pad) + `"}`
	if len(doc) != budget {
		tb.Fatalf("fixture is %d bytes, want exactly the %d-byte budget", len(doc), budget)
	}
	return []byte(doc)
}

// benchFindingsBody renders comma-separated records that fit in room bytes.
func benchFindingsBody(tb testing.TB, room int) string {
	tb.Helper()
	var sb strings.Builder
	for i := 0; ; i++ {
		entry := benchFinding(i)
		if i > 0 {
			entry = "," + entry
		}
		if sb.Len()+len(entry) > room {
			break
		}
		sb.WriteString(entry)
	}
	if sb.Len() == 0 {
		tb.Fatalf("%d bytes of room hold no record", room)
	}
	return sb.String()
}
