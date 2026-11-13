package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestFindingRuleIDsSkipsEmptyIDs(t *testing.T) {
	finding := jq.NewObj("kb", []any{"", "r1", "r1", 7, "r2"})
	got := FindingRuleIDs(finding)
	if strings.Join(got, ",") != "r1,r2" {
		t.Fatalf("FindingRuleIDs = %q, want [r1 r2]", got)
	}
}

func TestReadJoinsRefusesOversizeFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(Joins))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	row := `{"rule":"r1"}` + "\n"
	padded := row + strings.Repeat(" ", int(jq.MaxKBBytes)-len(row)+1)
	if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	got := ReadJoins(root, func(m string) { warned = append(warned, m) })
	if len(got) != 0 || len(warned) != 1 {
		t.Fatalf("ReadJoins over the cap = %d rows, warnings %q; want 0 rows and one warning", len(got), warned)
	}
}
