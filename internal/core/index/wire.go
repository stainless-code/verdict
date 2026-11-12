package indexcore

import "github.com/stainless-code/verdict/internal/jq"

// Published object keys shared by more than one branch.
const (
	keyKind   = "kind"
	keyTree   = "tree"
	keyPath   = "path"
	keyShards = "shards"
	keyQuery  = "query"
	keyCommit = "commit"
)

// Wire renders the index data profile in published order. Each subcommand
// publishes only the keys it measured.
func (d *Data) Wire() *jq.Obj {
	switch d.Kind {
	case QueryBuild:
		return jq.NewObj(
			keyKind, QueryBuild,
			keyTree, d.Tree,
			"commit", d.Commit,
			"files_indexed", d.Files,
			"skipped", d.Skipped,
			"parsed", d.Parsed,
			"reused", d.Reused,
			"tree_bytes", d.TreeBytes,
			"languages", languageValues(d.Languages),
			keyShards, shardDoc(d),
		)
	case QueryStatus:
		return jq.NewObj(
			keyKind, QueryStatus,
			keyTree, d.Tree,
			"commit", d.Commit,
			"built", d.Built,
			"stale", d.Stale,
			"trees", treeDocs(d.Trees),
			keyShards, shardDoc(d),
		)
	case QueryPrune:
		return jq.NewObj(
			keyKind, QueryPrune,
			"kept", stringValues(d.Kept),
			"removed", jq.NewObj("trees", d.RemovedTree, "shards", d.GoneShards),
			keyShards, shardDoc(d),
		)
	case QueryCallers:
		return jq.NewObj(
			keyKind, QueryCallers,
			"query", queryDoc(d),
			"index", indexDoc(d),
			"counts", jq.NewObj("callers", d.Callers, "unresolved", d.Unresolved),
			"limit", d.Limit,
			"truncated", d.Truncated,
			"rows", rowValues(d.Rows),
		)
	case QueryTestsFor:
		return testsForDoc(d)
	case QueryCoChanged:
		return coChangedDoc(d)
	case QueryOwners:
		return jq.NewObj(
			keyKind, QueryOwners,
			"query", queryDoc(d),
			keyTree, d.Tree,
			"commit", d.Commit,
			"stale", d.Stale,
			"target", targetDoc(d.Target),
		)
	case QueryBlastRadius:
		return blastRadiusDoc(d)
	default:
		return jq.NewObj()
	}
}

// testsForDoc renders the candidate-test profile.
func testsForDoc(d *Data) *jq.Obj {
	o := jq.NewObj(keyKind, QueryTestsFor, "query", queryDoc(d), "index", indexDoc(d))
	if d.Target != nil {
		o.Set("target", targetDoc(d.Target))
	}
	o.Set("counts", jq.NewObj("tests", d.Tests))
	o.Set("limit", d.Limit)
	o.Set("truncated", d.Truncated)
	o.Set("rows", testRowValues(d.TestRows))
	return o
}

// coChangedDoc renders the co-change profile with its echoed window.
func coChangedDoc(d *Data) *jq.Obj {
	return jq.NewObj(
		keyKind, QueryCoChanged,
		keyQuery, queryDoc(d),
		"window", windowDoc(d.Window),
		"target", targetDoc(d.Target),
		"counts", jq.NewObj("partners", d.Partners, "target_commits", d.TargetCommits),
		"limit", d.Limit,
		"truncated", d.Truncated,
		"rows", coChangeRowValues(d.CoChangeRows),
	)
}

// queryDoc renders the query input the subcommand read.
func queryDoc(d *Data) *jq.Obj {
	if d.Path != "" {
		return jq.NewObj(keyPath, d.Path)
	}
	return jq.NewObj("symbol", d.Symbol)
}

// indexDoc renders the queried tree's state and file coverage.
func indexDoc(d *Data) *jq.Obj {
	return jq.NewObj(
		keyTree, d.Tree,
		keyCommit, d.Commit,
		"stale", d.Stale,
		"coverage", jq.NewObj("files_indexed", d.Files, "skipped", d.Skipped),
	)
}

// targetDoc renders one path's CODEOWNERS answer. A nil owner list is a JSON
// null, never an owner named nobody.
func targetDoc(t *Target) *jq.Obj {
	var owners any
	if t.Owners != nil {
		owners = stringValues(t.Owners)
	}
	var source any
	if t.OwnersSource != "" {
		source = t.OwnersSource
	}
	return jq.NewObj(keyPath, t.Path, "owners", owners, "owners_source", source)
}

// windowDoc renders the echoed history window; an empty window omits its
// bounds and keeps its zero count.
func windowDoc(w Window) *jq.Obj {
	o := jq.NewObj()
	if w.First != "" {
		o.Set("first", w.First)
	}
	if w.Last != "" {
		o.Set("last", w.Last)
	}
	o.Set("commits", w.Commits)
	return o
}

// shardDoc renders the shard store's count and byte total.
func shardDoc(d *Data) *jq.Obj {
	return jq.NewObj("count", d.ShardCount, "bytes", d.ShardBytes)
}

// treeDocs renders the stored tree rows.
func treeDocs(rows []TreeRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(keyTree, row.Tree, "files", row.Files, "bytes", row.Bytes))
	}
	return out
}

// rowValues renders the caller rows.
func rowValues(rows []Row) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(
			keyPath, row.Path,
			"line", row.Line,
			"kind", row.Kind,
			"resolution", row.Resolution,
			"language", row.Language,
		))
	}
	return out
}

// languageValues renders the scanned languages and their file counts.
func languageValues(rows []LangRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj("language", row.Language, "files", row.Files))
	}
	return out
}

// testRowValues renders the candidate-test rows.
func testRowValues(rows []TestRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(keyPath, row.Path, "via", row.Via))
	}
	return out
}

// coChangeRowValues renders the partner rows.
func coChangeRowValues(rows []CoChangeRow) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, jq.NewObj(keyPath, row.Path, "commits", row.Commits, "jaccard", row.Jaccard))
	}
	return out
}

// stringValues renders a string slice as a non-nil JSON array.
func stringValues(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
