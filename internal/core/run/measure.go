package run

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/agentusage"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/mcpconfig"
	"github.com/stainless-code/verdict/internal/spend"
)

// meter totals one invocation's measured spend across every spawn its
// launches make, parallel phases included; a two-finder execution settles its
// spend receipt from it.
type meter struct {
	priced     []agentusage.Measurement
	mu         sync.Mutex
	unpriced   int
	notStarted int
}

func (m *meter) add(measured agentusage.Measurement) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.priced = append(m.priced, measured)
}

func (m *meter) miss() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unpriced++
}

func (m *meter) never() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notStarted++
}

// total is the summed spend and how it was priced, false while any spawn is
// unpriced or nothing was spawned. Any token-priced spawn makes the total
// token-priced; launches that all never started spent nothing.
func (m *meter) total() (string, string, bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.unpriced > 0:
		return "", "", false
	case len(m.priced) == 0 && m.notStarted > 0:
		return "0", spend.PricedNotStarted, true
	case len(m.priced) == 0:
		return "", "", false
	}
	return agentusage.Sum(m.priced)
}

// usageGate turns the structured-output request off for the rest of an
// invocation once the agent CLI rejected it, parallel phases included.
type usageGate struct {
	off atomic.Bool
}

// measuring reports whether this invocation measures its spawns' spend: the
// caller asked (the self-review loop's spawns), or it is a two-finder
// execution, whose spend receipt the measurement settles.
func (st *state) measuring() bool {
	return st.req.MeasureSpend || st.twoFinders
}

// usageArgs is the catalog row's structured-output request when this
// invocation measures, the agent has not rejected it, and the operator has
// not already chosen the output mode after `--`; nil otherwise, so a plain
// launch keeps its argv.
func (st *state) usageArgs(def *agentcatalog.Launch) []string {
	if !st.measuring() || def.Usage == nil || len(def.Usage.Args) == 0 {
		return nil
	}
	if st.usage != nil && st.usage.off.Load() {
		return nil
	}
	flag := usageFlag(def.Usage.Args)
	for _, arg := range st.req.PassThrough {
		if name, _, _ := strings.Cut(arg, "="); name == flag {
			return nil
		}
	}
	return slices.Clone(def.Usage.Args)
}

// usageFlag is the option name a structured-output request starts with.
func usageFlag(args []string) string {
	name, _, _ := strings.Cut(args[0], "=")
	return name
}

// fixed is the argv group between the row's own flags and the operator's
// pass-through: the structured-output request, then the discovery
// restriction.
func (a *launchArgs) fixed() []string {
	return append(append(slices.Clone(a.usage), a.restriction...), a.grant...)
}

// spawnAgent runs one agent process for prompt, tapping its stdout when the
// launch measures. An agent CLI too old for the structured-output flag
// rejects it: that spawn is retried once without it, and the invocation stops
// measuring rather than failing because of the measuring flag.
func (st *state) spawnAgent(ctx context.Context, args *launchArgs, prompt string) (spawned, error) {
	out, err := st.spawnTapped(ctx, args, prompt)
	if err != nil || !usageRejected(args, out.res) {
		return out, err
	}
	flag := usageFlag(args.usage)
	st.warn(args.def.Name + " rejected " + flag + ": this run continues without measuring its spend")
	if st.usage != nil {
		st.usage.off.Store(true)
	}
	if st.req.OnUsageRejected != nil {
		st.req.OnUsageRejected()
	}
	args.usage = nil
	args.argv = argvFor(&args.def, args.model(), args.fixed(), st.req.PassThrough, prompt)
	return st.spawnTapped(ctx, args, prompt)
}

// spawned is one spawn's result and the tap that read its stdout.
type spawned struct {
	res *core.Result
	tap *agentusage.Tap
}

func (st *state) spawnTapped(ctx context.Context, args *launchArgs, prompt string) (spawned, error) {
	argv := argvFor(&args.def, args.model(), args.fixed(), st.req.PassThrough, prompt)
	var tap *agentusage.Tap
	if len(args.usage) > 0 {
		tap = agentusage.NewTap(args.def.Usage.Format)
	}
	res, err := st.spawnLaunch(ctx, args, argv, tap)
	if res != nil && tap != nil && !res.StdoutTapped {
		tap = untapped(tap, res)
	}
	if err != nil {
		// The verdict reads a failed spawn by its error alone; its capture
		// served the measurement above.
		res = nil
	}
	return spawned{res: res, tap: tap}, err
}

// spawnLaunch runs one launch: a review launch in its own view of the
// reviewed tree, its artifacts copied back after it (spawnReview); a
// dispatch — the loop's fix, whose job is editing the checkout — in the
// checkout itself.
func (st *state) spawnLaunch(ctx context.Context, args *launchArgs, argv []string, tap *agentusage.Tap) (*core.Result, error) {
	if args.target.output == "" {
		return st.spawn(ctx, argv, args.timeout, tap, st.workDir(), st.childEnv())
	}
	return st.spawnReview(ctx, args, argv, tap)
}

// untapped fills the tap from a commander that cannot tap: its whole stdout,
// or nothing when the capture was cut — a partial stream is never measured.
func untapped(tap *agentusage.Tap, res *core.Result) *agentusage.Tap {
	if res.StdoutTruncated {
		return nil
	}
	tap.Feed([]byte(res.Stdout))
	return tap
}

// usageRejectionPhrases are how argument parsers refuse an option they do
// not know.
var usageRejectionPhrases = []string{
	"unknown option", "unknown flag", "unknown argument", "unexpected argument",
	"unrecognized option", "unrecognized argument", "invalid option", "no such option",
}

// usageRejected reports an agent that refused the structured-output flag: a
// failing exit whose output names the flag beside an unknown-option refusal.
func usageRejected(args *launchArgs, res *core.Result) bool {
	if len(args.usage) == 0 || res == nil || res.ExitCode == 0 || res.TimedOut {
		return false
	}
	out := strings.ToLower(res.Stdout + res.Stderr)
	if !strings.Contains(out, strings.ToLower(usageFlag(args.usage))) {
		return false
	}
	for _, phrase := range usageRejectionPhrases {
		if strings.Contains(out, phrase) {
			return true
		}
	}
	return false
}

// recordSpend measures one finished spawn and reports it. A spawn that
// provably never started (its process never existed) spent nothing; one that
// started but whose stdout carried no usable cost counts as unpriced. Only a
// measuring launch reports.
func (st *state) recordSpend(args *launchArgs, attempt int, out spawned, err error) (agentusage.Measurement, bool) {
	if !st.measuring() {
		return agentusage.Measurement{}, false
	}
	if neverStarted(out.res, err) {
		st.meter.never()
		st.reportSpend(attempt, "0", spend.PricedNotStarted)
		return agentusage.Measurement{USD: "0", PricedBy: spend.PricedNotStarted}, true
	}
	if out.tap == nil {
		st.meter.miss()
		return agentusage.Measurement{}, false
	}
	model, priceable := st.spawnModel(&args.def, args.model())
	if !priceable {
		model = ""
	}
	measured, ok := agentusage.Measure(out.tap, model)
	if !ok {
		st.meter.miss()
		return agentusage.Measurement{}, false
	}
	st.meter.add(measured)
	st.reportSpend(attempt, measured.USD, measured.PricedBy)
	return measured, true
}

func (st *state) reportSpend(attempt int, usd, pricedBy string) {
	if st.req.OnSpend != nil {
		st.req.OnSpend(attempt, usd, pricedBy)
	}
}

// neverStarted reports a launch whose process never existed: the commander's
// start failure, the one error that proves no spend.
func neverStarted(res *core.Result, err error) bool {
	if res != nil || err == nil {
		return false
	}
	var start *core.StartError
	return errors.As(err, &start)
}

// agentText is the agent's own text out of its stdout: the structured
// stream's messages when the launch tapped one, else the capture unchanged,
// so salvage reads the review the agent printed either way.
func (st *state) agentText(out spawned) string {
	if out.res == nil {
		return ""
	}
	if out.tap == nil {
		return out.res.Stdout
	}
	if text, ok := agentusage.Text(out.tap); ok {
		return text
	}
	return out.res.Stdout
}

// spawnModel is the model one launch runs, and whether its usage can be
// priced against that model: a stage's declared hint, an explicit
// --model/-m in the row's arguments or after `--`, or — for Codex, whose
// stream never names it — the model its own config.toml selects.
func (st *state) spawnModel(def *agentcatalog.Launch, stage string) (string, bool) {
	return spawnModel(st.req.Env, def, stage, st.req.PassThrough)
}

func spawnModel(env map[string]string, def *agentcatalog.Launch, stage string, passthrough []string) (string, bool) {
	if stage != "" {
		return stage, true
	}
	args := append(slices.Clone(def.Args), passthrough...)
	if def.Usage != nil && def.Usage.Format == agentcatalog.UsageCodexJSONL {
		return agentusage.CodexModel(mcpconfig.CodexHome(env), args)
	}
	return modelArg(args), true
}

// SpawnModel is the model a launch of the request's agent runs — the same
// resolution its spend measurement uses — so a receipt and a projection name
// the model the measurement priced. Empty when the agent picks its default.
func SpawnModel(req *core.RunRequest, agents []config.PersonalAgent) string {
	st := &state{req: req, defs: agents}
	def, ok := st.lookupAgent(req.Agent)
	if !ok {
		return ""
	}
	model, _ := spawnModel(req.Env, &def, "", req.PassThrough)
	return model
}

// modelArg reads a --model/-m value from arguments, in either the spaced or
// the inline spelling.
func modelArg(args []string) string {
	for i, arg := range args {
		if value, ok := modelValue(args, i, arg); ok {
			return value
		}
	}
	return ""
}

// modelValue reads one argument as a --model/-m option's value.
func modelValue(args []string, i int, arg string) (string, bool) {
	name, value, inline := strings.Cut(arg, "=")
	switch {
	case name != modelFlag && name != "-m":
		return "", false
	case inline:
		return value, true
	case i+1 < len(args):
		return args[i+1], true
	}
	return "", false
}

// settleExecution prices this invocation's spend receipt from what its spawns
// measured, once every one of them was measured: the month-to-date scan then
// counts the agents' own cost instead of the authorization's projection. A
// receipt with any unmeasured spawn keeps its projection.
func (st *state) settleExecution(ctx context.Context, dir string) {
	if st.receiptID == "" {
		return
	}
	usd, pricedBy, ok := st.meter.total()
	if !ok {
		return
	}
	ref := st.updateLedger(ctx, dir, func(ledger []any) []any {
		for _, row := range ledger {
			settleRow(row, st.receiptID, usd, pricedBy)
		}
		return ledger
	})
	if ref != nil {
		st.warn("could not settle the measured spend on the spend receipt: " + ref.Message)
	}
}

// settleRow sets the measured amount on the receipt this invocation wrote.
func settleRow(row any, receiptID, usd, pricedBy string) {
	obj, ok := row.(*jq.Obj)
	if !ok {
		return
	}
	if id, _ := obj.Get("invocation_id"); id == receiptID {
		obj.Set("projected_usd", json.Number(usd))
		obj.Set("priced_by", pricedBy)
	}
}
