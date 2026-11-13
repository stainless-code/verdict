package postcore

import (
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (st *state) validateFloor() *core.RefusalError {
	floor := st.flags.MinSeverity
	if floor == "" || floor == "off" || config.SeverityRank(floor) > 0 {
		return nil
	}
	return &core.RefusalError{Code: "bad-min-severity", Message: "pass critical|important|informational|off"}
}

func (st *state) postFloor(entry any) string {
	if st.flags.MinSeverity != "" {
		return st.flags.MinSeverity
	}
	return config.PostFloorFor(postRunState(st), jq.StrOf(readcommon.FieldOf(entry, "path")))
}

func (st *state) globalPostFloor() string {
	floor := st.postFloor(nil)
	if floor == "off" {
		return ""
	}
	return floor
}

func (st *state) withholdingSummary() *core.WithheldSummary {
	out := &core.WithheldSummary{ByReason: readcommon.WithheldCounts(st.merged.Entries)}
	for _, n := range out.ByReason {
		out.Total += n
	}
	for _, e := range st.merged.Entries {
		if config.SeverityRank(st.postFloor(e)) > 0 && config.SeverityRank(jq.StrOf(readcommon.FieldOf(e, "severity"))) == 0 {
			out.Unlabelled++
		}
	}
	if out.Total == 0 && out.Unlabelled == 0 && st.globalPostFloor() == "" {
		return nil
	}
	return out
}

func (st *state) floorScope(entry any, floor string) core.FloorScope {
	if st.flags.MinSeverity != "" {
		return core.FloorScope{Floor: floor}
	}
	return scopedFloorOf(postRunState(st), entry, floor)
}

func (st *state) belowFloor(entry any) bool {
	severity := config.SeverityRank(jq.StrOf(readcommon.FieldOf(entry, "severity")))
	return severity > 0 && severity < config.SeverityRank(st.postFloor(entry))
}
