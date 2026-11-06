package reportcore

import (
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// loadRunPolicy reads the persisted run bundle for the display floor:
// absent reads nil policy (legacy runs render everything), present
// parses strictly. The state root anchors to the working directory
// like the bundle itself.
func loadRunPolicy(st *state) *core.RefusalError {
	rs, found, serr := config.ReadRunState("", st.req.Pr)
	if serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: "report-failed", Message: "report failed: " + serr.Error()}
	}
	if found {
		st.policy = rs
	}
	return nil
}

// applyDisplayFloor derives the filtered view after the gates load: the
// effective display_min_severity withholds below-floor findings from
// the rendered view while every gate keeps reading the full ranked
// set. Unknown severities stay visible; the unset floor views
// everything.
func applyDisplayFloor(st *state) {
	st.view = st.ranked
	floor := displayFloor(st.policy)
	if floor == "" {
		return
	}
	kept := make([]readcommon.Ranked, 0, len(st.ranked))
	for _, item := range st.ranked {
		if displayWithheld(item.Entry, floor) {
			continue
		}
		kept = append(kept, item)
	}
	st.view = kept
}

// displayFloor reads the effective display floor from the run policy.
func displayFloor(rs *config.RunState) string {
	if rs == nil || rs.Resolved == nil || rs.Resolved.Defaults.Values.DisplayMinSeverity == nil {
		return ""
	}
	return *rs.Resolved.Defaults.Values.DisplayMinSeverity
}

// displayWithheld reports whether one stored entry sits below the view
// floor: a legible severity under the floor rank hides it, while a
// missing or unknown severity stays visible.
func displayWithheld(entry any, floor string) bool {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	severity, _ := m.Get("severity")
	rank := config.SeverityRank(jq.StrOf(severity))
	if rank == 0 {
		return false
	}
	return rank < config.SeverityRank(floor)
}

// observeReportGates appends head-current and no-live-threads
// observations for the gates this run evaluated: the completeness
// dimension observes head currency, the live dimension observes thread
// freshness. Observations append only on change (the latest matching
// outcome decides), so repeated identical reports do not spam the
// ledger, and append failures warn instead of refusing the report.
func observeReportGates(st *state, dims []core.GateDimension) {
	if st.policy == nil {
		return
	}
	for _, dim := range dims {
		// Skipped and unknown dimensions carry no verdict: only a
		// decided gate becomes execution evidence.
		if dim.Status != dimPass && dim.Status != dimFail {
			continue
		}
		var check string
		switch dim.ID {
		case dimHead:
			check = config.CheckHeadCurrent
		case dimLive:
			check = config.CheckNoLiveThreads
		default:
			continue
		}
		obs := config.CheckObservation{
			Check:  check,
			Passed: dim.Status == dimPass,
			At:     st.now,
			Detail: dim.Detail,
		}
		if !config.OutcomeChanged(st.policy, &obs) {
			continue
		}
		if ok, err := config.AppendOutcome("", st.req.Pr, &obs); !ok {
			st.warn(outcomeWarning(err))
		} else {
			st.policy.Outcomes = append(st.policy.Outcomes, obs)
		}
	}
}

// outcomeWarning renders an append failure as a warning.
func outcomeWarning(err error) string {
	if err != nil {
		return "check outcome not recorded: " + err.Error()
	}
	return "run bundle absent — check outcome not recorded"
}
