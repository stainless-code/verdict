package run

import (
	"strconv"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

const finderRenderRemedy = "use --finders 1 to render one command; launch normally for the full two-finder run"

// selectFinders resolves the launch shape in one place: the review-budget
// decision, then the finder count the precedence chain settles on (flag, then
// budget, then captured defaults, then the built-in), then the render refusal a
// two-finder resolution earns.
func (st *state) selectFinders(dir string) *core.RefusalError {
	if st.drainDigest != "" {
		return nil
	}
	captured, ref := readFinderCaptureForPolicy(dir, st.req)
	if ref != nil {
		return ref
	}
	if st.budget, ref = st.resolveBudget(dir, captured); ref != nil {
		return ref
	}
	count, _, ref := st.finderPolicy(captured)
	if ref != nil {
		return ref
	}
	st.recordBudgetCount(count)
	if st.budget != nil {
		st.human(budgetLine(st.budget))
	}
	return st.applyFinderCount(count)
}

// applyFinderCount pins the resolved count on the state and refuses a render
// that would have to name two launches instead of one.
func (st *state) applyFinderCount(count int) *core.RefusalError {
	st.twoFinders = st.twoFinders || count == 2
	if st.twoFinders && (st.req.Print || st.req.StageRaw != "") {
		return st.refuse(codeBadFlags, finderRenderRemedy)
	}
	return nil
}

// finderPolicy resolves the finder count in precedence order: an explicit
// --finders wins outright, a review budget next, the captured defaults.finders
// after it, and the built-in two-finder launch last.
func (st *state) finderPolicy(captured *config.RunState) (int, string, *core.RefusalError) {
	if st.req.FindersRaw != "" {
		return parseFinderCount(st.req.FindersRaw)
	}
	if st.budget != nil {
		return st.budget.Finders, budgetFinderSource, nil
	}
	if captured != nil && captured.Resolved != nil {
		return capturedFinderPolicy(captured)
	}
	return config.BuiltinFinders, "built-in", nil
}

func capturedFinderPolicy(captured *config.RunState) (int, string, *core.RefusalError) {
	defaults := captured.Resolved.Defaults
	if defaults.Values.Finders == 0 {
		return config.BuiltinFinders, "built-in", nil
	}
	if defaults.Values.Finders != 1 && defaults.Values.Finders != 2 {
		return 0, "", &core.RefusalError{Code: codeBadFlags, Message: "captured finders must be 1 or 2; re-fetch the bundle"}
	}
	return defaults.Values.Finders, defaults.Origins["finders"].Layer, nil
}

func parseFinderCount(raw string) (int, string, *core.RefusalError) {
	count, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(count) != raw || (count != 1 && count != 2) {
		return 0, "", &core.RefusalError{Code: codeBadFlags, Message: "--finders must be 1 or 2"}
	}
	return count, "flag", nil
}

// readFinderCaptureForPolicy reads the captured run record only when the
// resolution needs it: an explicit --finders with no --budget decides without
// the capture, exactly as it always has, so a bundle whose run.json cannot be
// verified never refuses a launch that asks for neither a depth nor a default.
func readFinderCaptureForPolicy(dir string, req *core.RunRequest) (*config.RunState, *core.RefusalError) {
	if req.FindersRaw != "" && req.BudgetRaw == "" {
		return nil, nil
	}
	return readFinderCapture(dir)
}

func readFinderCapture(dir string) (*config.RunState, *core.RefusalError) {
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		return nil, &core.RefusalError{Code: codeRecordFailed, Message: core.Msgf("%v", err)}
	}
	if captured != nil && captured.RequiresEvidence {
		return config.VerifyRunState(dir)
	}
	return captured, nil
}

func (st *state) launchBundle() (string, *core.RefusalError) {
	dir, ref := st.bundle()
	if ref != nil {
		return "", ref
	}
	return dir, st.selectFinders(dir)
}
