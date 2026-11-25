package reportcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func hiddenFindings(entries []any) []any {
	var hidden []any
	for _, e := range entries {
		if readcommon.WithheldSource(e) != "" {
			hidden = append(hidden, e)
		}
	}
	return hidden
}

func quietEntries(st *state) []any {
	if !st.flags.IncludeWithheld {
		return nil
	}
	ranked := readcommon.Rank(filterCategory(hiddenFindings(st.entries), st.flags.Category), st.flags.Status, st.flags.Kind)
	var out []any
	for _, r := range ranked {
		out = append(out, r.Entry)
	}
	return out
}

func quietLines(st *state) []string {
	entries := quietEntries(st)
	if len(entries) == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("## Quiet by policy (%d)", len(entries))}
	for _, e := range entries {
		w := readcommon.FieldOf(e, "withheld")
		source := readcommon.WithheldSource(e)
		if id := jq.StrOf(readcommon.FieldOf(w, "exclusion_id")); id != "" {
			source += " " + id
		}
		out = append(out, fmt.Sprintf("- %s — withheld:%s — %s", readcommon.Locator(e), source, jq.StrOf(readcommon.FieldOf(w, "reason"))))
	}
	return out
}

func withholdCounts(st *state) {
	n := len(hiddenFindings(st.entries))
	st.counts.Withheld = n
	if n > 0 || st.flags.IncludeWithheld {
		st.counts.WithheldBySource = readcommon.WithheldCounts(st.entries)
	}
}

func sarifWithheld(st *state, run *jq.Obj) {
	if len(hiddenFindings(st.entries)) > 0 {
		run.Set("properties", jq.NewObj("withheld", readcommon.WithheldCounts(st.entries)))
	}
}
