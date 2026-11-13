package trendcore

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestTrendWithheldSourcesAndLegacyZero(t *testing.T) {
	raw, err := os.ReadFile("../report/testdata/withheld.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := doc.([]any)
	if !ok {
		t.Fatal("fixture must be an array")
	}
	r := buildRow("", "7", entries)
	got := rollupRows([]row{r, categoryRow("8", "logic")})
	if got.withheld["agent"] != 1 || got.withheld["below-severity"] != 0 {
		t.Fatalf("withheld = %v", got.withheld)
	}
	legacy := rollupRows([]row{categoryRow("8", "logic")})
	if withheldTotal(legacy.withheld) != 0 || legacy.withheld["below-severity"] != 0 {
		t.Fatalf("legacy = %v", legacy.withheld)
	}
}
