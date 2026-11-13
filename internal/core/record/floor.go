package recordcore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
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
	if st.minSeverity != "" {
		return st.minSeverity
	}
	return config.PostFloorFor(st.run, suppressionString(e, fieldPath))
}

func (st *state) applyPostFloor() {
	unlabelled := 0
	for _, entry := range st.entries {
		e, ok := entry.(*jq.Obj)
		if !ok {
			continue
		}
		floor := st.findingFloor(e)
		severity := suppressionString(e, fieldSeverity)
		rank := config.SeverityRank(severity)
		if config.SeverityRank(floor) > 0 && rank == 0 {
			unlabelled++
		}
		st.floorEntry(e, floor, severity, rank)
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

func (st *state) hideWithheldSections(body string) string {
	hidden := withheldIndex(st.entries, func(e *jq.Obj) string { return suppressionString(e, fieldHeading) })
	var out strings.Builder
	dropping := false
	for line := range strings.SplitAfterSeq(body, "\n") {
		dropping = dropSectionLine(line, hidden, dropping)
		if !dropping {
			out.WriteString(line)
		}
	}
	return out.String()
}

func dropSectionLine(line string, hidden map[string]bool, dropping bool) bool {
	if strings.HasPrefix(line, "### ") {
		return hidden[strings.TrimSpace(line)]
	}
	if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "---") {
		return false
	}
	return dropping
}
