package recordcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const sourceBelowSeverity = "below-severity"

func (st *state) applySilence() error {
	if err := st.applySuppressions(); err != nil {
		return err
	}
	if st.cmd == "post" {
		st.applyPostFloor()
		n := len(st.entries) - len(readcommon.VisibleFindings(st.entries))
		if n > 0 {
			st.warn(fmt.Sprintf("%d finding(s) withheld by policy", n))
		}
	}
	return nil
}

func (st *state) findingFloor(e *jq.Obj) string {
	return config.EffectiveFloor(st.minSeverity, st.run, suppressionString(e, fieldPath))
}

func (st *state) applyPostFloor() {
	unlabelled := 0
	for _, entry := range st.entries {
		unlabelled += st.floorFinding(entry)
	}

	if unlabelled > 0 {
		st.warn(fmt.Sprintf("%d finding(s) carry no severity; the floor does not apply", unlabelled))
	}
}

func (st *state) floorEntry(e *jq.Obj, floor, severity string, rank int) {
	source := readcommon.WithheldSource(e)
	if source != "" && source != sourceBelowSeverity {
		return
	}
	if rank > 0 && rank < config.SeverityRank(floor) {
		st.stampSuppression(e, jq.NewObj(sourceKey, sourceBelowSeverity, reasonKey, "severity "+severity+" below floor "+floor))
	} else if source == sourceBelowSeverity {
		e.Delete(withheldKey)
		e.Set(fieldHistory, appendHistory(e, jq.NewObj(fieldAt, st.now, "event", "reopened", "actor", st.cmd, "note", "withheld cleared")))
	}
}

func (st *state) floorFinding(entry any) int {
	e, ok := entry.(*jq.Obj)
	if !ok || suppressionString(e, fieldHeading) == core.MovedSectionHeading {
		return 0
	}
	floor := st.findingFloor(e)
	severity := suppressionString(e, fieldSeverity)
	rank := config.SeverityRank(severity)
	st.floorEntry(e, floor, severity, rank)
	if readcommon.UnlabelledUnderFloor(e, floor) {
		return 1
	}
	return 0
}
