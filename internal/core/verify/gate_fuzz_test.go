package verifycore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// FuzzGate checks the gate's documented refusal contract.
func FuzzGate(f *testing.F) {
	seedGate(f)
	f.Fuzz(checkGateInput)
}

func checkGateInput(t *testing.T, raw string) {
	t.Helper()
	if len(raw) > 1<<12 {
		return
	}
	detached := verifycore.DetachOptionValues(raw)
	if strings.ReplaceAll(raw, " ", "") != strings.ReplaceAll(detached, " ", "") {
		t.Fatalf("DetachOptionValues(%q) = %q; only separator spaces may differ", raw, detached)
	}
	ref := verifycore.Check(raw)
	if again := verifycore.Check(raw); !sameRefusal(ref, again) {
		t.Fatalf("Check(%q) = %v then %v; want the same refusal", raw, ref, again)
	}
	if ref == nil {
		return
	}
	if !documentedGateCode(ref.Code) {
		t.Fatalf("Check(%q) refused with undocumented code %q", raw, ref.Code)
	}
	const prefix = "refusing verifyCmd: matched '"
	if !strings.HasPrefix(ref.Message, prefix) {
		t.Fatalf("Check(%q) refusal message = %q, want prefix %q", raw, ref.Message, prefix)
	}
}

func sameRefusal(a, b *core.RefusalError) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Code == b.Code && a.Message == b.Message
}

func documentedGateCode(code string) bool {
	switch code {
	case "verify-destructive", "verify-networked", "verify-credential-read", "verify-repo-escape":
		return true
	}
	return false
}

func seedGate(f *testing.F) {
	f.Helper()
	for _, cmd := range gateSeedCommands(f) {
		f.Add(cmd)
	}
}

func gateSeedCommands(f *testing.F) []string {
	f.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "evals", "fixtures", "verify-fuzz-corpus.tsv"))
	if err != nil {
		f.Fatalf("read corpus: %v", err)
	}
	rows, err := parseCorpus(raw)
	if err != nil {
		f.Fatal(err)
	}
	out := make([]string, 0, len(rows)+len(vProbes)+len(gateOnlyExtras()))
	for _, r := range rows {
		out = append(out, r.cmd)
	}
	for _, p := range vProbes {
		out = append(out, p.cmd)
	}
	for _, e := range gateOnlyExtras() {
		out = append(out, e[1])
	}
	return append(out, mutationSeeds()...)
}

func mutationSeeds() []string {
	var out []string
	for _, m := range gateMutationCores {
		out = append(out, m.cmd)
		if upper := strings.ToUpper(m.cmd); upper != m.cmd {
			out = append(out, upper)
		}
		if !strings.Contains(m.cmd, `\`) {
			out = append(out, `\`+m.cmd)
		}
	}
	return out
}
