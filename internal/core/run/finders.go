package run

import (
	"strconv"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

const finderRenderRemedy = "use --finders 1 to render one command; launch normally for the full two-finder run"

func (st *state) selectFinders(dir string) *core.RefusalError {
	if st.drainDigest != "" {
		return nil
	}
	count, _, ref := st.finderPolicy(dir)
	if ref != nil {
		return ref
	}
	st.twoFinders = st.twoFinders || count == 2
	if st.twoFinders && (st.req.Print || st.req.StageRaw != "") {
		return st.refuse(codeBadFlags, finderRenderRemedy)
	}
	return nil
}

func (st *state) finderPolicy(dir string) (int, string, *core.RefusalError) {
	if st.req.FindersRaw != "" {
		return parseFinderCount(st.req.FindersRaw)
	}
	captured, ref := readFinderCapture(dir)
	if ref != nil {
		return 0, "", ref
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

func readFinderCapture(dir string) (*config.RunState, *core.RefusalError) {
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		return nil, &core.RefusalError{Code: codeRecordFailed, Message: err.Error()}
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
