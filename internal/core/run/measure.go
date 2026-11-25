package run

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/agentusage"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// meter totals one invocation's measured spend across every spawn its
// launches make, parallel phases included; a two-finder execution settles its
// spend receipt from it once every spawn was measured.
type meter struct {
	priced   []agentusage.Measurement
	unpriced int
	mu       sync.Mutex
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

// total is the summed spend and how it was priced, false unless every spawn
// was measured. Any token-priced spawn makes the total token-priced.
func (m *meter) total() (string, string, bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unpriced > 0 || len(m.priced) == 0 {
		return "", "", false
	}
	return agentusage.Sum(m.priced)
}

// measuring reports whether this invocation measures its spawns' spend: the
// caller asked (the self-review loop's spawns), or it is a two-finder
// execution, whose spend receipt the measurement settles.
func (st *state) measuring() bool {
	return st.req.MeasureSpend || st.twoFinders
}

// usageArgs is the catalog row's structured-output request when this
// invocation measures and the operator has not already chosen the output
// mode after `--`; nil otherwise, so a plain launch keeps its argv.
func (st *state) usageArgs(def *agentcatalog.Launch) []string {
	if !st.measuring() || def.Usage == nil {
		return nil
	}
	flag, _, _ := strings.Cut(def.Usage.Args[0], "=")
	for _, arg := range st.req.PassThrough {
		if name, _, _ := strings.Cut(arg, "="); name == flag {
			return nil
		}
	}
	return slices.Clone(def.Usage.Args)
}

// fixed is the argv group between the row's own flags and the operator's
// pass-through: the structured-output request, then the discovery
// restriction.
func (a *launchArgs) fixed() []string {
	return append(slices.Clone(a.usage), a.restriction...)
}

// recordSpend measures one finished spawn from its captured output and
// reports it. A spawn that never started spent nothing and is not counted; a
// measuring launch whose output carried no usable cost counts as unpriced.
func (st *state) recordSpend(args *launchArgs, attempt int, res *core.Result) (agentusage.Measurement, bool) {
	if len(args.usage) == 0 || res == nil {
		return agentusage.Measurement{}, false
	}
	measured, ok := agentusage.Measure(args.def.Usage.Format, []byte(res.Stdout), st.modelOf(args))
	if !ok {
		st.meter.miss()
		return agentusage.Measurement{}, false
	}
	st.meter.add(measured)
	if st.req.OnSpend != nil {
		st.req.OnSpend(attempt, measured.USD, measured.PricedBy)
	}
	return measured, true
}

// agentText is the agent's own text out of its captured stdout: the
// structured stream's final message when the launch requested one, else the
// stream unchanged, so salvage reads the review the agent printed either way.
func (st *state) agentText(args *launchArgs, stdout string) string {
	if len(args.usage) == 0 {
		return stdout
	}
	if text, ok := agentusage.Text(args.def.Usage.Format, []byte(stdout)); ok {
		return text
	}
	return stdout
}

// modelOf is the model a launch names: the stage's declared hint, or the
// operator's --model/-m after `--`; empty when the agent picks its default.
func (st *state) modelOf(args *launchArgs) string {
	if model := args.model(); model != "" {
		return model
	}
	return modelArg(st.req.PassThrough)
}

// modelArg reads a --model/-m value from pass-through arguments, in either
// the spaced or the inline spelling.
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
