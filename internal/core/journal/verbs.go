package journal

import "strings"

// VerbPolicy records whether one CLI verb journals to a bundle and why.
type VerbPolicy struct {
	Reason   string
	Journals bool
}

const reasonDistribution = "distribution surface; writes no bundle"

// VerbTable classifies every verb in the CLI synopsis. Bundle-writing verbs
// journal (J2); read-only, multi-bundle, and non-bundle verbs do not.
// [internal/archtest/journal_test.go] pins totality, MCP registry parity, and
// the literal journaling set.
var VerbTable = map[string]VerbPolicy{
	// Bundle-writing verbs (14).
	"adopt":       {Journals: true, Reason: "writes target PR bundle findings store (localWrites)"},
	"calibrate":   {Journals: true, Reason: "resolves a bundle and writes .verdict/kb-grades.jsonl locally (localWrites)"},
	"drain":       {Journals: true, Reason: "CLI-only localWrites-equivalent: writes drain.json and drain.replies.json"},
	"fetch":       {Journals: true, Reason: "writes PR bundle artifacts (localWrites)"},
	"fetch-local": {Journals: true, Reason: "writes local bundle artifacts (localWrites)"},
	"fix":         {Journals: true, Reason: "writes fix ledger in bundle store (localWrites)"},
	"ingest":      {Journals: true, Reason: "writes ingested SARIF findings into bundle store (localWrites)"},
	"post":        {Journals: true, Reason: "writes post.json and updates bundle findings store (localWrites)"},
	"record":      {Journals: true, Reason: "writes findings.json and record.json (localWrites; dry-run journals too)"},
	"report":      {Journals: true, Reason: "writes bundle outcomes/artifacts (localWrites in MCP registry, Q2)"},
	"run":         {Journals: true, Reason: "CLI-only localWrites-equivalent: writes launcher.json, procedure.md, provenance.json"},
	"threads":     {Journals: true, Reason: "writes refreshed thread state into bundle store (localWrites)"},
	"triage":      {Journals: true, Reason: "writes triage state into bundle findings store (localWrites)"},
	"verify":      {Journals: true, Reason: "writes verification proofs into bundle store (localWrites)"},

	// Read-only bundle or repo verbs (noLocalWrites: must never write).
	"check-tree": {Journals: false, Reason: "noLocalWrites: read-only git worktree gate"},
	"demo": {Journals: false, Reason: "no local writes in the caller's tree: every artifact lands " +
		"in a disposable temp directory removed on exit"},
	"config":  {Journals: false, Reason: "noLocalWrites: read-only policy explanation"},
	"doctor":  {Journals: false, Reason: "noLocalWrites: read-only environment preflight"},
	"gate":    {Journals: false, Reason: "noLocalWrites: CI exit code; must never mutate the checkout"},
	"handoff": {Journals: false, Reason: "noLocalWrites: read-only finding handoff"},
	"index":   {Journals: false, Reason: "noLocalWrites: read-only code index query"},
	"journal": {Journals: false, Reason: "noLocalWrites: read-only journal reader; reading must never write"},
	"next":    {Journals: false, Reason: "noLocalWrites: read-only next-finding cursor"},
	"pack":    {Journals: false, Reason: "noLocalWrites: read-only lens pack inspection"},
	"pr-wait": {Journals: false, Reason: "noLocalWrites: read-only foreground PR wait"},
	"ready":   {Journals: false, Reason: "noLocalWrites: read-only merge readiness check"},
	"risk":    {Journals: false, Reason: "noLocalWrites: read-only risk queue"},
	"show":    {Journals: false, Reason: "noLocalWrites: read-only single-finding display"},

	// Multi-bundle verbs (no single bundle home, Q4).
	"backtest": {Journals: false, Reason: "multi-bundle replay under .verdict/backtest/ (Q4)"},
	"loop": {Journals: false, Reason: "multi-bundle self-review rounds: one fresh local bundle per round " +
		"plus receipts outside the store, with no single bundle home (Q4)"},
	"panel": {Journals: false, Reason: "multi-bundle orchestration with no single bundle home (Q4)"},
	"trend": {Journals: false, Reason: "noLocalWrites and multi-bundle portfolio rollup (Q4)"},

	// Non-bundle verbs (config, learnings, distribution, transport, listener).
	"init":      {Journals: false, Reason: "scaffolds repo-level .verdict/ controls; owns no bundle"},
	"learn":     {Journals: false, Reason: "writes repo-level .verdict/learnings.md; owns no bundle"},
	"mcp":       {Journals: false, Reason: "stdio transport entry point; each tool call journals individually"},
	"acp":       {Journals: false, Reason: "stdio ACP transport; read-only queries journal nothing"},
	"lsp":       {Journals: false, Reason: "stdio LSP transport; publishes diagnostics and writes nothing"},
	"serve":     {Journals: false, Reason: "local HTTP listener; owns no bundle"},
	"skill":     {Journals: false, Reason: reasonDistribution},
	"uninstall": {Journals: false, Reason: reasonDistribution},
	"upgrade":   {Journals: false, Reason: reasonDistribution},
}

// CanonicalVerb normalizes an MCP tool spelling (such as `pr_wait`) to the
// canonical CLI verb spelling (`pr-wait`).
func CanonicalVerb(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

// ShouldJournal reports whether invocations of verb append to a bundle journal.
func ShouldJournal(verb string) bool {
	return VerbTable[CanonicalVerb(verb)].Journals
}
