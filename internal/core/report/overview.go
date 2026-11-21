package reportcore

import (
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/overrides"
)

// renderOverview renders the collapsed file table plus its topology half, or
// "" when the feature is off or the bundle stays below the size gate. The
// rows, the gate, and the block all come from internal/diagram, so the report
// and the posted body read one bundle the same way.
func renderOverview(st *state) string {
	show, forced := overviewGate(st)
	if !show {
		return ""
	}
	b := diagram.LoadBundle(st.dir)
	if !forced && !diagram.Large(b, overrides.At(st.req.OverridesDir)) {
		return ""
	}
	lines := overviewTableLines(b.Files)
	if st.req.Effective.DiagramsStructural == strTrue {
		if block := diagram.Block(b, st.req.Pr); block != "" {
			lines = append(lines, overviewLines(block)...)
		}
	}
	return strings.Join(lines, newline)
}

// overviewGate keeps the --overview force apart from the resolved toggle: only
// the report bypasses the size gate with it.
func overviewGate(st *state) (show, forced bool) {
	switch forces := st.req.ForcesOrEmpty(); forces.PublishOverviewForce {
	case strFalse:
		return false, false
	case strTrue:
		return true, true
	}
	return st.req.Effective.PublishOverview == strTrue, false
}

func overviewTableLines(rows []diagram.File) []string {
	lines := []string{
		"<details>", "<summary>Files changed (" + strconv.Itoa(len(rows)) + ")</summary>", "",
		"| # | File | +/- | Hunks | Role |", "|---|---|---|---|---|",
	}
	for _, row := range rows {
		lines = append(lines, overviewTableRow(row))
	}
	return append(lines, "", "</details>", "")
}

func overviewTableRow(row diagram.File) string {
	return "| " + row.ID + " | `" + diagram.Sanitize(row.Path) + "` | +" +
		strconv.Itoa(row.Adds) + "/-" + strconv.Itoa(row.Dels) +
		" | " + strconv.Itoa(row.Hunks) + " | " + row.Role + " |"
}
