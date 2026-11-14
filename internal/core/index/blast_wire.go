package indexcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// List keys shared by every measured list.
const (
	keyCount = "count"
	keyRows  = "rows"
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
	for _, key := range []string{"symbols", "callers", "tests", "co_changed", "owners"} {
		child, _ := o.Get(key)
		if why := listTruncationReason(child); why != "" {
			returned := len(b.Symbols) + len(b.Callers) + len(b.Tests) + len(b.Partners) + len(b.Owners)
			return core.ResultCut(why, returned, nil, nil).Apply(o)
		}
	}

	return o
}

// blastSymbolDoc renders the changed-declaration list.
func blastSymbolDoc(b *Blast) *jq.Obj {
	return listReceipt(jq.NewObj(
		keyCount, nullInt(b.Counts.ChangedSymbols),
		keyRows, blastSymbolValues(b.Symbols),
	), len(b.Symbols), measuredHidden(b.Truncated.ChangedSymbols), measuredHidden(nil))
}

// blastCallerDoc renders the caller list and the candidates it could not
// attribute.
func blastCallerDoc(b *Blast) *jq.Obj {
	return listReceipt(jq.NewObj(
		keyCount, nullInt(b.Counts.Callers),
		"unresolved", nullInt(b.Counts.Unresolved),
		keyRows, blastCallerValues(b.Callers),
	), len(b.Callers), measuredHidden(b.Truncated.Callers), measuredHidden(nil))
}

// blastTestDoc renders the candidate-test list and the candidates the depth cap
// hid.
func blastTestDoc(b *Blast) *jq.Obj {
	return listReceipt(jq.NewObj(
		keyCount, nullInt(b.Counts.Tests),
		keyRows, testRowValues(b.Tests),
	), len(b.Tests), measuredHidden(b.Truncated.Tests), measuredHidden(b.DepthTruncated))
}

// blastPartnerDoc renders the co-change list with its pair and partner counts.
func blastPartnerDoc(b *Blast) *jq.Obj {
	return listReceipt(jq.NewObj(
		keyCount, b.Counts.CoChanged,
		"partners", b.Counts.Partners,
		keyRows, blastPartnerValues(b.Partners),
	), len(b.Partners), measuredHidden(new(b.Truncated.CoChanged)), measuredHidden(nil))
}

// blastOwnerDoc renders the ownership list.
func blastOwnerDoc(b *Blast) *jq.Obj {
	return listReceipt(jq.NewObj(
		keyCount, b.Counts.Owners,
		keyRows, blastOwnerValues(b.Owners),
	), len(b.Owners), measuredHidden(new(b.Truncated.Owners)), measuredHidden(nil))
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

func measuredHidden(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func listTruncationReason(data any) string {
	row, ok := data.(*jq.Obj)
	if !ok {
		return ""
	}
	value, _ := row.Get("truncation_reason")
	reason, ok := value.(string)
	if !ok {
		return ""
	}
	return reason
}
