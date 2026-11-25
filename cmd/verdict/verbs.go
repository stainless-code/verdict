package main

import (
	"context"
	"strings"

	"github.com/stainless-code/verdict/internal/adopt"
	backtestverb "github.com/stainless-code/verdict/internal/backtest"
	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/configcmd"
	demo "github.com/stainless-code/verdict/internal/demo"
	"github.com/stainless-code/verdict/internal/doctor"
	drainverb "github.com/stainless-code/verdict/internal/drain"
	"github.com/stainless-code/verdict/internal/fetch"
	"github.com/stainless-code/verdict/internal/fix"
	"github.com/stainless-code/verdict/internal/gate"
	handoffverb "github.com/stainless-code/verdict/internal/handoff"
	"github.com/stainless-code/verdict/internal/index"
	"github.com/stainless-code/verdict/internal/ingest"
	journalverb "github.com/stainless-code/verdict/internal/journal"
	"github.com/stainless-code/verdict/internal/learn"
	"github.com/stainless-code/verdict/internal/loop"
	"github.com/stainless-code/verdict/internal/pack"
	"github.com/stainless-code/verdict/internal/panel"
	"github.com/stainless-code/verdict/internal/prwait"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/record"
	"github.com/stainless-code/verdict/internal/report"
	"github.com/stainless-code/verdict/internal/risk"
	runverb "github.com/stainless-code/verdict/internal/run"
	"github.com/stainless-code/verdict/internal/scaffold"
	"github.com/stainless-code/verdict/internal/threads"
	"github.com/stainless-code/verdict/internal/trend"
	"github.com/stainless-code/verdict/internal/triage"
	"github.com/stainless-code/verdict/internal/verify"
)

// arity is the positional shape a verb takes, which decides how splitCommand
// reads its arguments and whether a missing positional is a usage error.
type arity uint8

const (
	// arityPair verbs take `<owner/repo> <pr>` (fetch-local's second positional
	// is a bundle name); a missing half is a usage error.
	arityPair arity = iota
	// arityRepo verbs take one `<owner/repo>` and nothing else positional: every
	// other argument is one of their own flags.
	arityRepo
	// arityOptional verbs take at most one optional positional and no repo/pr
	// pair, so the split must not read the first token as a repository and the
	// second as a PR.
	arityOptional
	// arityBare verbs take no positionals: every argument is one of their flags.
	arityBare
)

// verbSpec describes one verb once: its name, its positional shape, and the
// handler the journal-guarded dispatch runs. A nil Run marks a verb served
// before the repository root is resolved or before argument parsing (the
// transports, serve, the dashboard, and the upgrade installer): the table
// still names it so the usage hint, the arity gate, and the parity test see it.
type verbSpec struct {
	Run   readHandler
	Name  string
	Arity arity
}

// verbTable is the one ordered list of verbs. Its order is the order the
// unknown-command hint names them. Hidden transport verbs (the upgrade
// helper and the schema checker) and the meta flags are not verbs and stay in
// run.
var verbTable = []verbSpec{
	{Name: "demo", Arity: arityOptional, Run: demo.Run},
	{Name: "fetch", Arity: arityPair, Run: fetch.Run},
	{Name: cmdFetchLocal, Arity: arityPair, Run: fetch.RunLocal},
	{Name: "run", Arity: arityPair, Run: runverb.Run},
	{Name: "loop", Arity: arityRepo, Run: loop.Run},
	{Name: "panel", Arity: arityPair, Run: panel.Run},
	{Name: "record", Arity: arityPair, Run: record.Run},
	{Name: "adopt", Arity: arityPair, Run: adopt.Run},
	{Name: "ingest", Arity: arityPair, Run: ingest.Run},
	{Name: "fix", Arity: arityPair, Run: fix.Run},
	{Name: "post", Arity: arityPair, Run: record.RunPost},
	{Name: "triage", Arity: arityPair, Run: triage.Run},
	{Name: "report", Arity: arityPair, Run: report.Run},
	{Name: "next", Arity: arityPair, Run: report.RunNext},
	{Name: cmdShow, Arity: arityPair, Run: report.RunShow},
	{Name: "check-tree", Arity: arityBare, Run: checktree.Run},
	{Name: "verify", Arity: arityPair, Run: verify.Run},
	{Name: verbDoctor, Arity: arityBare, Run: doctor.Run},
	{Name: "calibrate", Arity: arityPair, Run: calibrate.Run},
	{Name: cmdReady, Arity: arityPair, Run: ready.Run},
	{Name: cmdPRWait, Arity: arityPair, Run: prwait.Run},
	{Name: "gate", Arity: arityPair, Run: gate.Run},
	{Name: cmdThreads, Arity: arityPair, Run: threads.Run},
	{Name: "trend", Arity: arityRepo, Run: trend.Run},
	{Name: "risk", Arity: arityRepo, Run: risk.Run},
	{Name: "handoff", Arity: arityPair, Run: handoffverb.Run},
	{Name: "index", Arity: arityBare, Run: index.Run},
	{Name: "journal", Arity: arityPair, Run: journalverb.Run},
	{Name: "backtest", Arity: arityOptional, Run: backtestverb.Run},
	{Name: "learn", Arity: arityBare, Run: learn.Run},
	{Name: "pack", Arity: arityBare, Run: pack.Run},
	{Name: "init", Arity: arityBare, Run: scaffold.Run},
	{Name: verbSkill, Arity: arityBare, Run: runSkillVerb},
	{Name: "upgrade", Arity: arityBare, Run: nil},
	{Name: "uninstall", Arity: arityBare, Run: nil},
	{Name: verbMCP, Arity: arityBare, Run: nil},
	{Name: "serve", Arity: arityBare, Run: nil},
	{Name: verbDashboard, Arity: arityOptional, Run: nil},
	{Name: "config", Arity: arityPair, Run: configcmd.Run},
	{Name: cmdDrain, Arity: arityPair, Run: drainverb.Run},
	{Name: verbACP, Arity: arityBare, Run: nil},
	{Name: verbLSP, Arity: arityBare, Run: nil},
}

// verbIndex finds a verb's spec by name.
var verbIndex = indexVerbs(verbTable)

// verbsHint names every verb in table order, the hint of the unknown-command
// refusal.
var verbsHint = strings.Join(verbNames(), "|")

// bareVerbs take no positionals, so splitCommand reads every argument as
// a flag.
var bareVerbs = verbsWith(arityBare)

func indexVerbs(table []verbSpec) map[string]*verbSpec {
	byName := make(map[string]*verbSpec, len(table))
	for i := range table {
		byName[table[i].Name] = &table[i]
	}
	return byName
}

func verbNames() []string {
	names := make([]string, len(verbTable))
	for i := range verbTable {
		names[i] = verbTable[i].Name
	}
	return names
}

func verbsWith(want arity) map[string]bool {
	set := make(map[string]bool)
	for i := range verbTable {
		if verbTable[i].Arity == want {
			set[verbTable[i].Name] = true
		}
	}
	return set
}

// arityOf is a verb's positional shape; an unknown command reads as a pair,
// so the gate asks for the pair the way it always has.
func arityOf(cmd string) arity {
	if spec, ok := verbIndex[cmd]; ok {
		return spec.Arity
	}
	return arityPair
}

// runSkillVerb adapts the skill distributor, which reads the environment and
// the tool checkout rather than a target, to the table's handler shape.
func runSkillVerb(ctx context.Context, r *cli.Runner, rest []string, req readcommon.Request) int {
	return runSkill(ctx, r, rest, req.Env, req.RootPath())
}
