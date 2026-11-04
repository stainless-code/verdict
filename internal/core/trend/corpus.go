package trendcore

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/statedir"
)

type corpusPoint struct {
	id             string
	model          string
	panel          string
	bugs           int
	reported       int
	matched        int
	falsePositives int
	missed         int
}

type corpusCounts struct {
	bundles        int
	bugs           int
	reported       int
	matched        int
	falsePositives int
	missed         int
}

type corpusRollup struct {
	cases   map[string]bool
	byModel map[string]*corpusCounts
	byPanel map[string]*corpusCounts
	total   corpusCounts
}

func (st *state) readCorpus(e os.DirEntry) {
	dir := filepath.Join(statedir.Resolve().Root, e.Name())
	c, ref := corpuscore.Load(dir)
	if ref != nil {
		st.warn("trend skips corpus bundle " + dir + "/ (" + ref.Message + ")")
		return
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		st.warn("trend skips corpus bundle " + dir + "/ (" + err.Error() + ")")
		return
	}
	report := corpuscore.Evaluate(c, entries)
	st.corpora = append(st.corpora, corpusPoint{
		id:             c.ID,
		model:          corpuscore.ModelOf(dir),
		panel:          corpuscore.PanelLabel(corpuscore.PanelOf(dir)),
		bugs:           len(c.Bugs),
		reported:       report.Reported,
		matched:        report.Matched,
		falsePositives: report.FalsePositives,
		missed:         len(report.Missed),
	})
}

func corpusRollupOf(points []corpusPoint) *corpusRollup {
	if len(points) == 0 {
		return nil
	}
	r := &corpusRollup{
		cases:   map[string]bool{},
		byModel: map[string]*corpusCounts{},
		byPanel: map[string]*corpusCounts{},
	}
	for i := range points {
		p := &points[i]
		r.cases[p.id] = true
		r.total.add(p)
		cell(r.byModel, p.model).add(p)
		cell(r.byPanel, p.panel).add(p)
	}
	return r
}

func (c *corpusCounts) add(p *corpusPoint) {
	c.bundles++
	c.bugs += p.bugs
	c.reported += p.reported
	c.matched += p.matched
	c.falsePositives += p.falsePositives
	c.missed += p.missed
}

func cell(m map[string]*corpusCounts, key string) *corpusCounts {
	if c, ok := m[key]; ok {
		return c
	}
	c := &corpusCounts{}
	m[key] = c
	return c
}

// corpusDoc renders the corpus block in published key order.
func corpusDoc(r *corpusRollup) *jq.Obj {
	models := make([]any, 0, len(r.byModel))
	for _, key := range slices.Sorted(maps.Keys(r.byModel)) {
		models = append(models, corpusCountsDoc("model", key, r.byModel[key]))
	}
	panels := make([]any, 0, len(r.byPanel))
	for _, key := range slices.Sorted(maps.Keys(r.byPanel)) {
		panels = append(panels, corpusCountsDoc("panel", key, r.byPanel[key]))
	}
	return jq.NewObj(
		"cases", len(r.cases),
		"bundles", r.total.bundles,
		"ground_truth", r.total.bugs,
		"reported", r.total.reported,
		"matched", r.total.matched,
		"false_positives", r.total.falsePositives,
		"missed", r.total.missed,
		"precision", corpuscore.RateOrNil(r.total.matched, r.total.reported),
		"recall", corpuscore.RateOrNil(r.total.matched, r.total.bugs),
		"false_positive_rate", corpuscore.RateOrNil(r.total.falsePositives, r.total.reported),
		"by_model", models,
		"by_panel", panels,
	)
}

func corpusCountsDoc(key, label string, c *corpusCounts) *jq.Obj {
	return jq.NewObj(
		key, label,
		"bundles", c.bundles,
		"ground_truth", c.bugs,
		"reported", c.reported,
		"matched", c.matched,
		"false_positives", c.falsePositives,
		"missed", c.missed,
		"precision", corpuscore.RateOrNil(c.matched, c.reported),
		"recall", corpuscore.RateOrNil(c.matched, c.bugs),
		"false_positive_rate", corpuscore.RateOrNil(c.falsePositives, c.reported),
	)
}

func (st *state) emitCorpusHuman(r *corpusRollup) {
	st.human(fmt.Sprintf("corpus: %d bundle(s) over %d case(s) — ground_truth=%d reported=%d "+
		"matched=%d false_positives=%d missed=%d",
		r.total.bundles, len(r.cases), r.total.bugs, r.total.reported,
		r.total.matched, r.total.falsePositives, r.total.missed))
	st.human(fmt.Sprintf("corpus rates: precision=%s recall=%s false-positive-rate=%s",
		corpuscore.RateText(corpuscore.RateOrNil(r.total.matched, r.total.reported)),
		corpuscore.RateText(corpuscore.RateOrNil(r.total.matched, r.total.bugs)),
		corpuscore.RateText(corpuscore.RateOrNil(r.total.falsePositives, r.total.reported))))
	for _, key := range slices.Sorted(maps.Keys(r.byModel)) {
		st.human(corpusCountsLine("model", key, r.byModel[key]))
	}
	for _, key := range slices.Sorted(maps.Keys(r.byPanel)) {
		st.human(corpusCountsLine("panel", key, r.byPanel[key]))
	}
}

func corpusCountsLine(key, label string, c *corpusCounts) string {
	return fmt.Sprintf("corpus %s %s: bundles=%d ground_truth=%d reported=%d matched=%d "+
		"false_positives=%d missed=%d precision=%s recall=%s false-positive-rate=%s",
		key, label, c.bundles, c.bugs, c.reported, c.matched, c.falsePositives, c.missed,
		corpuscore.RateText(corpuscore.RateOrNil(c.matched, c.reported)),
		corpuscore.RateText(corpuscore.RateOrNil(c.matched, c.bugs)),
		corpuscore.RateText(corpuscore.RateOrNil(c.falsePositives, c.reported)))
}
