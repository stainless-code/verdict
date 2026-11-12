package indexcore

import "github.com/stainless-code/verdict/internal/jq"

// List keys shared by every measured list.
const (
	keyCount     = "count"
	keyTruncated = "truncated"
	keyRows      = "rows"
)

// blastIndexDoc renders the queried tree's index state and coverage.
func blastIndexDoc(d *Data) *jq.Obj {
	return jq.NewObj(
		keyTree, d.Tree,
		keyCommit, d.Commit,
		"stale", d.Stale,
		"coverage", jq.NewObj("files_indexed", d.Files, "skipped", d.Skipped),
	)
}

// blastRadiusDoc renders the blast-radius profile: the input, one measured
// object per list, and the queried tree's index state when one answered.
func blastRadiusDoc(d *Data) *jq.Obj {
	b := d.Blast
	o := jq.NewObj(keyKind, QueryBlastRadius)
	if b.Indexed {
		o.Set("index", blastIndexDoc(d))
	}
	o.Set("input", jq.NewObj(
		"pr", nullString(b.Input.Pr),
		"head", nullString(b.Input.Head),
		"paths", stringValues(b.Input.Paths),
	))
	o.Set("limit", d.Limit)
	o.Set("symbols", blastSymbolDoc(b))
	o.Set("callers", blastCallerDoc(b))
	o.Set("tests", blastTestDoc(b))
	o.Set("co_changed", blastPartnerDoc(b))
	o.Set("owners", blastOwnerDoc(b))
	return o
}

// blastSymbolDoc renders the changed-declaration list.
func blastSymbolDoc(b *Blast) *jq.Obj {
	return jq.NewObj(
		keyCount, nullInt(b.Counts.ChangedSymbols),
		keyTruncated, nullInt(b.Truncated.ChangedSymbols),
		keyRows, blastSymbolValues(b.Symbols),
	)
}

// blastCallerDoc renders the caller list and the candidates it could not
// attribute.
func blastCallerDoc(b *Blast) *jq.Obj {
	return jq.NewObj(
		keyCount, nullInt(b.Counts.Callers),
		"unresolved", nullInt(b.Counts.Unresolved),
		keyTruncated, nullInt(b.Truncated.Callers),
		keyRows, blastCallerValues(b.Callers),
	)
}

// blastTestDoc renders the candidate-test list.
func blastTestDoc(b *Blast) *jq.Obj {
	return jq.NewObj(
		keyCount, nullInt(b.Counts.Tests),
		keyTruncated, nullInt(b.Truncated.Tests),
		keyRows, testRowValues(b.Tests),
	)
}

// blastPartnerDoc renders the co-change list.
func blastPartnerDoc(b *Blast) *jq.Obj {
	return jq.NewObj(
		keyCount, b.Counts.CoChanged,
		keyTruncated, b.Truncated.CoChanged,
		keyRows, blastPartnerValues(b.Partners),
	)
}

// blastOwnerDoc renders the ownership list.
func blastOwnerDoc(b *Blast) *jq.Obj {
	return jq.NewObj(
		keyCount, b.Counts.Owners,
		keyTruncated, b.Truncated.Owners,
		keyRows, blastOwnerValues(b.Owners),
	)
}

// nullInt renders a measured count, or JSON null when the list went
// unmeasured.
func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullString renders a non-empty string, or JSON null when absent.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// blastSymbolValues renders the changed-declaration rows.
func blastSymbolValues(rows []SymbolRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(
			keyPath, row.Path, "name", row.Name, "kind", row.Kind, "line", row.Line,
		))
	}
	return out
}

// blastCallerValues renders the caller rows.
func blastCallerValues(rows []CallerRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(
			"symbol", row.Symbol, keyPath, row.Path, "kind", row.Kind,
			"resolution", row.Resolution, "line", row.Line, "depth", row.Depth,
		))
	}
	return out
}

// blastPartnerValues renders the co-change rows.
func blastPartnerValues(rows []BlastPartnerRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(
			"target", row.Target, keyPath, row.Path, "commits", row.Commits, "jaccard", row.Jaccard,
		))
	}
	return out
}

// blastOwnerValues renders one ownership row per changed path.
func blastOwnerValues(rows []Target) []any {
	out := make([]any, 0, len(rows))
	for i := range rows {
		out = append(out, targetDoc(&rows[i]))
	}
	return out
}
