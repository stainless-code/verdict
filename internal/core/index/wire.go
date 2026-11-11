package indexcore

import "github.com/stainless-code/verdict/internal/jq"

// Published object keys shared by more than one branch.
const (
	keyKind   = "kind"
	keyTree   = "tree"
	keyShards = "shards"
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
			"query", jq.NewObj("symbol", d.Symbol),
			"index", jq.NewObj(
				keyTree, d.Tree,
				"commit", d.Commit,
				"stale", d.Stale,
				"coverage", jq.NewObj("files_indexed", d.Files, "skipped", d.Skipped),
			),
			"counts", jq.NewObj("callers", d.Callers, "unresolved", d.Unresolved),
			"limit", d.Limit,
			"truncated", d.Truncated,
			"rows", rowValues(d.Rows),
		)
	default:
		return jq.NewObj()
	}
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
			"path", row.Path,
			"line", row.Line,
			"kind", row.Kind,
			"resolution", row.Resolution,
		))
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
