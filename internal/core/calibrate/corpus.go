package calibratecore

import (
	"fmt"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (st *state) runCorpus(dir string) *core.RefusalError {
	c, ref := corpuscore.Load(dir)
	if ref != nil {
		return st.withWarnings(ref)
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return st.loadRefusal(err)
	}
	report := corpuscore.Evaluate(c, entries)
	st.corpus = &corpusResult{
		dir:    dir,
		report: report,
		model:  corpuscore.ModelOf(dir),
		panel:  corpuscore.PanelOf(dir),
	}
	st.emitCorpusHuman(st.corpus)
	return nil
}

type corpusResult struct {
	dir    string
	report *corpuscore.Report
	model  string
	panel  []string
}

func (st *state) corpusResponse() *Response {
	return &Response{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       st.req.TimeUTC(),
		Next:     "trend --json for per-model and per-panel corpus rates",
		Warnings: st.warnings,
		Data:     &Data{corpus: st.corpus},
	}
}

// Wire renders the corpus data in published key order.
func (r *corpusResult) Wire() *jq.Obj {
	rep := r.report
	outcomes := make([]any, 0, len(rep.Outcomes))
	for i := range rep.Outcomes {
		outcomes = append(outcomes, outcomeCorpusDoc(&rep.Outcomes[i]))
	}
	missed := make([]any, 0, len(rep.Missed))
	for i := range rep.Missed {
		missed = append(missed, corpusBugDoc(&rep.Missed[i]))
	}
	panel := make([]any, 0, len(r.panel))
	for _, p := range r.panel {
		panel = append(panel, p)
	}
	return jq.NewObj(
		"bundle", filepath.Base(r.dir),
		"case", rep.Case.ID,
		"kind", rep.Case.Kind,
		"model", r.model,
		"panel", panel,
		"line_tolerance", corpuscore.Tolerance(rep.Case),
		"ground_truth", len(rep.Case.Bugs),
		"reported", rep.Reported,
		"matched", rep.Matched,
		"false_positives", rep.FalsePositives,
		"missed", len(rep.Missed),
		"precision", corpuscore.RateOrNil(rep.Matched, rep.Reported),
		"recall", corpuscore.RateOrNil(rep.Matched, len(rep.Case.Bugs)),
		"false_positive_rate", corpuscore.RateOrNil(rep.FalsePositives, rep.Reported),
		"outcomes", outcomes,
		"missed_bugs", missed,
	)
}

func outcomeCorpusDoc(o *corpuscore.Outcome) *jq.Obj {
	var path, line any
	if o.HasPath {
		path = o.Path
	}
	if o.HasLine {
		line = o.Line
	}
	var matched any
	if o.HasBugHit {
		matched = o.BugLine
	}
	return jq.NewObj(
		"sig", o.Sig,
		"path", path,
		"line", line,
		"result", o.Result,
		"matched_line", matched,
	)
}

// corpusBugDoc renders one missed ground-truth bug.
func corpusBugDoc(b *core.CorpusBug) *jq.Obj {
	var note any
	if b.Note != "" {
		note = b.Note
	}
	return jq.NewObj(
		"path", b.Path,
		"line", b.Line,
		"kind", b.Kind,
		"severity", b.Severity,
		"note", note,
	)
}

func (st *state) emitCorpusHuman(r *corpusResult) {
	rep := r.report
	st.human(fmt.Sprintf("calibrate: corpus %s — %s (model %s, panel %s)",
		rep.Case.ID, filepath.Base(r.dir), r.model, corpuscore.PanelLabel(r.panel)))
	tol := corpuscore.Tolerance(rep.Case)
	for i := range rep.Outcomes {
		o := &rep.Outcomes[i]
		loc := corpusLocator(o)
		if o.Result == corpuscore.ResultTruePositive {
			continue
		}
		st.human(fmt.Sprintf("%s [false-positive] %s — no ground-truth bug within ±%d line(s)",
			corpusSig(o.Sig), loc, tol))
	}
	for i := range rep.Missed {
		b := &rep.Missed[i]
		st.human(fmt.Sprintf("missed: %s:%d %s (%s)", b.Path, b.Line, b.Kind, b.Severity))
	}
	st.human(fmt.Sprintf("rates: bugs=%d reported=%d matched=%d missed=%d false-positives=%d "+
		"precision=%s recall=%s false-positive-rate=%s",
		len(rep.Case.Bugs), rep.Reported, rep.Matched, len(rep.Missed), rep.FalsePositives,
		corpuscore.RateText(corpuscore.RateOrNil(rep.Matched, rep.Reported)),
		corpuscore.RateText(corpuscore.RateOrNil(rep.Matched, len(rep.Case.Bugs))),
		corpuscore.RateText(corpuscore.RateOrNil(rep.FalsePositives, rep.Reported))))
}

func corpusLocator(o *corpuscore.Outcome) string {
	if !o.HasPath {
		return "?"
	}
	if !o.HasLine {
		return o.Path
	}
	return fmt.Sprintf("%s:%d", o.Path, o.Line)
}

func corpusSig(sig string) string {
	if sig == "" {
		return corpusSigNull
	}
	return readcommon.Prefix(sig)
}

const corpusSigNull = "null"
