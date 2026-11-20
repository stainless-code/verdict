package recordcore

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestReproductionBudget(t *testing.T) {
	dir := t.TempDir()
	_, _, err := captureReproduction(dir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		jq.NewObj("path", "", "command", "false"), false, 65535)
	if err == nil {
		t.Fatal("accepted artifact beyond the bundle budget")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("budget refusal wrote files: %v %v", entries, err)
	}
}

func TestReproductionWarningRedactsSourcePath(t *testing.T) {
	st := &state{dir: t.TempDir(), cmd: recordCommand}
	entry := jq.NewObj("sig", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "rigor", "reproduced",
		"reproduction", jq.NewObj("path", "ghp_abcdefghijklmnopqrstuvwxyz0123456789.py", "command", "python3 {artifact}"))
	st.captureEntryReproduction(entry, 1048576)
	warning := strings.Join(st.warnings, " ")
	if strings.Contains(warning, "ghp_") || !strings.Contains(warning, "***REDACTED***") {
		t.Fatalf("warning = %q", warning)
	}
}
