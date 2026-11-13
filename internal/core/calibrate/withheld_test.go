package calibratecore

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestWithheldCalibrationStaysUnknown(t *testing.T) {
	raw, err := os.ReadFile("../report/testdata/withheld.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := doc.([]any)
	if !ok || len(entries) != 1 {
		t.Fatal("fixture requires one finding")
	}
	host := liveHost{posted: func(any) string { return "finding" }, match: func(any, string, any) bool { return true }, resolved: func(any) bool { return true }}
	out := joinOne(entries[0], []any{jq.NewObj()}, []any{jq.NewObj("filename", "notes.txt")}, host)
	if out.result != outcomeUnknown {
		t.Fatalf("outcome = %q; want unknown", out.result)
	}
	p := countPrecision([]outcome{out})
	if p.judged != 0 || p.fixed != 0 || p.unknown != 1 {
		t.Fatalf("precision = %+v", p)
	}
}
