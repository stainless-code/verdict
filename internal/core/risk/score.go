package risk

import (
	"encoding/json"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The published score weights. They are code constants on purpose: a row's
// points and weight are echoed, so every value is recomputable without a
// config read, and a score already stored never changes meaning.
const (
	WeightReach    = 35
	WeightChurn    = 25
	WeightCategory = 25
	WeightRung     = 15
	scoreScale     = WeightReach + WeightChurn + WeightCategory + WeightRung
)

const (
	impactSuffix   = "/impact.json"
	findingsSuffix = "/findings.json"
	statusOpen     = "open"
	// bandTop bounds the last bucket: any count above it falls through to the
	// same points, so no input escapes a table.
	bandTop = 1 << 30
)

// band is one bucket row: a value at or under upTo earns points.
type band struct {
	upTo   int
	points int
}

var (
	reachBands = []band{{0, 0}, {3, 25}, {10, 50}, {25, 75}, {bandTop, 100}}
	churnBands = []band{{20, 0}, {100, 25}, {300, 50}, {800, 75}, {bandTop, 100}}
)

// categoryPoints and rungPoints are the fixed per-label rows of the published
// tables. Untagged reads with "other": an unlabelled finding is still a
// finding, so it never scores as style.
var (
	categoryPoints = map[string]int{
		"security":            100,
		"logic":               75,
		"performance":         50,
		"edge-case":           50,
		"accidental-commit":   50,
		"other":               25,
		core.CategoryUntagged: 25,
		"style":               0,
		"docs":                0,
	}
	rungPoints = map[string]int{
		core.RungSaid:       100,
		core.RungPointed:    75,
		core.RungWalked:     25,
		core.RungRan:        0,
		core.RungReproduced: 0,
	}
)

// Score computes the advisory 0..100 triage score for one bundle from its own
// artifacts: impact.json for reach, full.diff for churn, and the open entries
// for category and rung. entries are the bundle's findings; a component whose
// artifact was not read stays unmeasured at 0 points rather than vanishing
// into a clean-looking zero.
func Score(dir string, entries []any) *core.RiskScore {
	findingsRead := isRegular(dir + findingsSuffix)
	out := &core.RiskScore{
		Reach:    reachScore(dir),
		Churn:    churnScore(dir),
		Category: categoryScore(entries, findingsRead),
		Rung:     rungScore(entries, findingsRead),
	}
	for _, measured := range []bool{out.Reach.Measured, out.Churn.Measured, out.Category.Measured, out.Rung.Measured} {
		if measured {
			out.Measured++
		}
	}
	out.Value = (WeightReach*out.Reach.Points + WeightChurn*out.Churn.Points +
		WeightCategory*out.Category.Points + WeightRung*out.Rung.Points) / scoreScale
	return out
}

// reachScore reads impact.json: the changed symbols plus their callers, and
// the index's own stale flag. A count that is not a non-negative integer
// leaves reach unmeasured, so a hostile artifact can never read as 0 points.
func reachScore(dir string) core.RiskReach {
	out := core.RiskReach{Weight: WeightReach}
	raw, err := jq.CappedFile(dir+impactSuffix, jq.MaxFindingsBytes)
	if err != nil {
		return out
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return out
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return out
	}
	out.Symbols = impactCount(m, "symbols")
	out.Callers = impactCount(m, "callers")
	if stale, ok := m["stale"].(bool); ok {
		out.Stale = &stale
	}
	if out.Symbols == nil || out.Callers == nil {
		return out
	}
	out.Measured = true
	out.Points = bandPoints(*out.Symbols+*out.Callers, reachBands)
	return out
}

// impactCount reads one list receipt's count, nil unless it is a non-negative
// integer literal.
func impactCount(m map[string]any, key string) *int {
	list, ok := jq.AsMap(m[key])
	if !ok {
		return nil
	}
	lit, ok := list["count"].(json.Number)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(lit.String())
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// churnScore sums full.diff's files, hunks, additions, and deletions. The read
// cap is the store cap, far above the fetched-diff cap, so a produced bundle
// always reads while a hostile artifact cannot allocate without bound.
func churnScore(dir string) core.RiskChurn {
	out := core.RiskChurn{Weight: WeightChurn}
	raw, err := jq.CappedFile(dir+fullDiffSuffix, jq.MaxFindingsBytes)
	if err != nil {
		return out
	}
	out.Measured = true
	files := hunks.Files(string(raw))
	for i := range files {
		out.Files++
		out.Additions += files[i].Additions
		out.Deletions += files[i].Deletions
		out.Hunks += files[i].HunkCount
	}
	out.Points = bandPoints(out.Additions+out.Deletions, churnBands)
	return out
}

// categoryScore picks the worst category among the open findings. The first
// open finding always names the worst, so a measured style-only store reads
// worst=style at 0 points instead of reading like an empty one.
func categoryScore(entries []any, measured bool) core.RiskCategory {
	out := core.RiskCategory{Measured: measured, Weight: WeightCategory}
	for _, e := range openEntries(entries) {
		out.Open++
		category := core.CategoryOf(e)
		points := categoryPoints[category]
		if out.Worst == nil || points > out.Points {
			worst := category
			out.Worst = &worst
			out.Points = points
		}
	}
	return out
}

// rungScore picks the weakest rung among the open findings: the rung a bundle
// still rests on, so one said-grade finding drags the whole bundle down.
func rungScore(entries []any, measured bool) core.RiskRung {
	out := core.RiskRung{Measured: measured, Weight: WeightRung}
	weakest := ""
	for _, e := range openEntries(entries) {
		out.Open++
		rung := core.RungOf(e)
		if rung == "" {
			rung = core.RungSaid
		}
		if weakest == "" || rungOrder(rung) > rungOrder(weakest) {
			weakest = rung
		}
	}
	if weakest != "" {
		out.Weakest = &weakest
		out.Points = rungPoints[weakest]
	}
	return out
}

func rungOrder(rung string) int {
	for i, name := range core.RungValues {
		if name == rung {
			return i
		}
	}
	return len(core.RungValues)
}

func openEntries(entries []any) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		if readcommon.StatusOf(e) == statusOpen {
			out = append(out, e)
		}
	}
	return out
}

// bandPoints returns the points of the first bucket the value sits under; the
// last bucket holds every larger value.
func bandPoints(value int, bands []band) int {
	for _, b := range bands {
		if value <= b.upTo {
			return b.points
		}
	}
	return bands[len(bands)-1].points
}
