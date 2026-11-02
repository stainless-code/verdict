package readcommon

import (
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Store refusal codes and messages, byte-identical to the shell.
const (
	codeMissingFindings = "missing-findings"
	codeCorruptStore    = "corrupt-store"
	msgMissingFindings  = "missing %s/findings.json (run post or record first)"
	msgCorruptStore     = "findings store %s/findings.json is unreadable or" +
		" not a findings array (re-run post/record to rebuild, or restore the file)"
)

// LoadFindings ports the findings-store preamble shared by the readers:
// the file must exist (missing-findings) and hold a JSON array
// (corrupt-store). R1: entries must additionally be objects-or-null with a
// null-or-string sig — anything else crashed bash with a jq exit-5 and no
// envelope, so it now refuses typed corrupt-store instead (the shell gained
// the same guard with this port; see evals/cases/read-refusals.sh).
func LoadFindings(dir string) ([]any, error) {
	raw, err := os.ReadFile(dir + "/findings.json")
	if err != nil {
		return nil, &cli.RefusalError{Code: codeMissingFindings, Message: fmt.Sprintf(msgMissingFindings, dir)}
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, corruptStore(dir)
	}
	entries, ok := doc.([]any)
	if !ok {
		return nil, corruptStore(dir)
	}
	if !validEntries(entries) {
		return nil, corruptStore(dir)
	}
	return entries, nil
}

// corruptStore builds the corrupt-store refusal for dir.
func corruptStore(dir string) *cli.RefusalError {
	return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptStore, dir)}
}

// validEntries enforces the R1 entry shape shared by the readers: every
// entry is null or an object whose string-indexed fields (sig, body,
// heading, kind) read null-or-string and whose indexed-map fields
// (severity, rigor) never read object-or-array. Anything else crashed the
// shell's rank, resolve, display, or SARIF filters with a jq exit-5 and no
// envelope, so it now refuses typed corrupt-store instead (the shell
// gained the same guard with this port; see evals/cases/read-refusals.sh).
// Fields the readers only compare or interpolate (status, path, line,
// anchored, inBounds, kb) stay unguarded: exotic shapes flow crash-free.
func validEntries(entries []any) bool {
	for _, e := range entries {
		if e == nil {
			continue
		}
		m, ok := e.(*jq.Obj)
		if !ok {
			return false
		}
		if !validEntryStrings(m) || !validEntryMaps(m) {
			return false
		}
	}
	return true
}

// validEntryStrings gates the string-indexed entry fields.
func validEntryStrings(m *jq.Obj) bool {
	for _, key := range []string{"sig", "body", "heading", "kind", "suggestion"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

// validEntryMaps gates the indexed-map entry fields: containers would crash
// the severity/rigor rank lookups.
func validEntryMaps(m *jq.Obj) bool {
	for _, key := range []string{"severity", "rigor"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		switch v.(type) {
		case map[string]any, *jq.Obj, []any:
			return false
		default:
		}
	}
	return true
}

// ResolveSig ports resolve_sig: the unique full sig with the given prefix,
// or the NONE/MANY sentinels. Non-string sigs never match (the entry guard
// above already refused exotic stores).
func ResolveSig(entries []any, prefix string) string {
	var match string
	n := 0
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sig, _ := m.Get("sig")
		s, ok := sig.(string)
		if !ok {
			continue
		}
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			n++
			match = s
		}
	}
	switch n {
	case 0:
		return "NONE"
	case 1:
		return match
	default:
		return "MANY"
	}
}
