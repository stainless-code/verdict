package trendcore

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/incident"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// incidentWarn names one incident bundle the join cannot measure, with the
// reason the shared reader gave: the ledger is tracked, so a bundle whose
// store is gone is reported, never counted as a miss.
const incidentWarn = "trend skips incident %s: %s"

// msgIncidentLedger is the refusal message for an unreadable ledger.
const msgIncidentLedger = "incident ledger %s is unreadable (restore from backup or fix the bad line)"

// incidentRollup is the miss rate over attached incidents: only incidents a
// bundle's findings store can be read for enter the denominator.
type incidentRollup struct {
	PerBundle  []incidentBundle
	Bundles    int
	Incidents  int
	Measured   int
	Unmeasured int
	Hit        int
	Missed     int
}

// incidentBundle is one bundle's share of the miss rate.
type incidentBundle struct {
	Bundle    string
	Incidents int
	Measured  int
	Hit       int
	Missed    int
}

// incidentAudit measures every attached incident against the findings its
// bundle stored. A miss is "no non-false-positive finding on an incident
// path": a false-positive finding is a wrong claim, not a catch.
func incidentAudit(st *state) (*incidentRollup, *core.RefusalError) {
	rows, err := incident.Read(st.req.RepoRootPath())
	if errors.Is(err, incident.ErrMissing) {
		return &incidentRollup{}, nil
	}
	if err != nil {
		return nil, &core.RefusalError{
			Code:    "corrupt-store",
			Message: core.Msgf(msgIncidentLedger, filepath.ToSlash(incident.File)),
		}
	}
	slices.SortStableFunc(rows, incident.CompareRows)
	out := &incidentRollup{}
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].Bundle == rows[start].Bundle {
			end++
		}
		out.addBundle(st, rows[start:end])
		start = end
	}
	return out, nil
}

// addBundle folds one bundle's incidents: its findings store is read once, and
// an unreadable one leaves every incident of that bundle unmeasured.
func (out *incidentRollup) addBundle(st *state, rows []incident.Row) {
	bundle := incidentBundle{Bundle: rows[0].Bundle, Incidents: len(rows)}
	if findings, ok := st.incidentFindings(bundle.Bundle); ok {
		bundle.Measured = len(rows)
		for _, row := range rows {
			if incidentHit(findings, row.Paths) {
				bundle.Hit++
			} else {
				bundle.Missed++
			}
		}
	}
	out.PerBundle = append(out.PerBundle, bundle)
	out.Bundles++
	out.Incidents += bundle.Incidents
	out.Measured += bundle.Measured
	out.Unmeasured += bundle.Incidents - bundle.Measured
	out.Hit += bundle.Hit
	out.Missed += bundle.Missed
}

// incidentFindings reads one incident bundle's findings store. An absent or
// unreadable one warns with the reader's own reason, so a corrupt store never
// reads as a missing one.
func (st *state) incidentFindings(name string) ([]any, bool) {
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), name)
	if ref != nil {
		st.warn(fmt.Sprintf(incidentWarn, name, ref.Message))
		return nil, false
	}
	return kb.ReadBundleFindings(dir, "", func(reason string) {
		st.warn(fmt.Sprintf(incidentWarn, name, reason))
	})
}

// incidentHit reports whether any finding catches one of the incident paths,
// exactly or as a directory prefix.
func incidentHit(findings []any, paths []string) bool {
	for _, entry := range findings {
		if readcommon.StatusOf(entry) == statusFalsePositive {
			continue
		}
		file, ok := readcommon.FieldOf(entry, "path").(string)
		if !ok {
			continue
		}
		if coversAnyPath(file, paths) {
			return true
		}
	}
	return false
}

// coversAnyPath reports whether file is one of paths or sits under it.
func coversAnyPath(file string, paths []string) bool {
	for _, p := range paths {
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// emitIncidentsHuman renders the miss-rate summary and one line per bundle,
// after the overlap and corpus lines.
func (st *state) emitIncidentsHuman(out *incidentRollup) {
	st.human(core.Msgf("incidents: %d attached, %d measured, %d unmeasured — %s",
		out.Incidents, out.Measured, out.Unmeasured, missedText(out.Missed, out.Measured)))
	for i := range out.PerBundle {
		row := &out.PerBundle[i]
		st.human(core.Msgf("  %s: %d attached, %d measured, %d unmeasured — %s",
			row.Bundle, row.Incidents, row.Measured, row.Incidents-row.Measured, missedText(row.Missed, row.Measured)))
	}
}

// missedText renders "missed M of D (P%)", with n/a and no percentage when the
// denominator is zero.
func missedText(missed, measured int) string {
	if measured == 0 {
		return "missed 0 of 0 (n/a)"
	}
	return fmt.Sprintf("missed %d of %d (%.1f%%)", missed, measured, float64(missed)/float64(measured)*percentScale)
}

// incidentsDoc renders the incidents envelope fragment in published key order.
func incidentsDoc(out *incidentRollup) *jq.Obj {
	perBundle := make([]any, 0, len(out.PerBundle))
	for i := range out.PerBundle {
		perBundle = append(perBundle, incidentBundleDoc(&out.PerBundle[i]))
	}
	return jq.NewObj(
		"bundles", out.Bundles,
		"incidents", out.Incidents,
		"measured", out.Measured,
		"unmeasured", out.Unmeasured,
		"hit", out.Hit,
		"missed", out.Missed,
		"miss_rate", rateOrNil(out.Missed, out.Measured),
		"per_bundle", perBundle,
	)
}

// incidentBundleDoc renders one bundle row; its miss rate carries the same
// rounding and null-on-zero-denominator rule as the total.
func incidentBundleDoc(row *incidentBundle) *jq.Obj {
	return jq.NewObj(
		"bundle", row.Bundle,
		"incidents", row.Incidents,
		"measured", row.Measured,
		"hit", row.Hit,
		"missed", row.Missed,
		"miss_rate", rateOrNil(row.Missed, row.Measured),
	)
}
