package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	maxPublicationBytes = 64 << 20
	jsonNullValue       = "null"
)

// renderPublication re-renders a published report, applying one adjudication
// record when the operator names it. Nothing here launches an agent: it reads
// the JSON, reclassifies, and writes the Markdown (and, with -out, the
// adjudicated report beside it).
func renderPublication(cfg *Config, w io.Writer) error {
	rep, err := readPublication(cfg.Render)
	if err != nil {
		return err
	}
	if err := applyRecord(rep, cfg.Adjudication); err != nil {
		return err
	}
	if cfg.Out != "" {
		if err := rep.write(cfg.Out); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, renderMarkdown(rep)); err != nil {
		return jq.NewErrf("render: %s", err)
	}
	return nil
}

// applyRecord lifts one record onto the report; an empty path leaves the
// publication strict-only.
func applyRecord(rep *Report, path string) error {
	if path == "" {
		return nil
	}
	adj, err := loadAdjudication(path, rep)
	if err != nil {
		return err
	}
	rep.Adjudication = adj
	return nil
}

// readPublication parses the JSON a run wrote.
func readPublication(path string) (*Report, error) {
	raw, err := jq.CappedOperatorFile(path, maxPublicationBytes)
	if err != nil {
		return nil, jq.NewErrf("publication: %s", err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, jq.NewErrf("publication JSON: %s", err)
	}
	if rep.SchemaVersion != "1" {
		return nil, jq.NewErr("publication schema_version must be 1")
	}
	return &rep, nil
}

func renderMarkdown(rep *Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Benchmark %s\n\n", cell(rep.Date))
	if rep.Seeded {
		out.WriteString("**not a model-quality claim**\n\n")
	}
	renderEligibility(&out, rep)
	fmt.Fprintf(&out, "Corpus commit: `%s` (dirty: %t)  \n"+
		"Prompts hash: `%s`  \nTool version: `%s`  \nDate: %s  \nSampling: k=%d, repeats=%d  \n",
		cell(rep.Corpus.Commit), rep.Corpus.Dirty, cell(rep.PromptsHash), cell(rep.ToolVersion), cell(rep.GeneratedAt), rep.K, rep.Repeats)
	fmt.Fprintf(&out, "Severity weights: critical=%d, important=%d, informational=%d\n\n",
		rep.SeverityWeights.Critical, rep.SeverityWeights.Important, rep.SeverityWeights.Informational)
	renderFailedSamples(&out, rep)
	renderMetricTable(&out, rep)
	renderAdjudication(&out, rep)
	renderSampleSpreads(&out, rep.Cases)
	out.WriteString("\n## Commands\n\nArgv arrays preserve argument boundaries.\n\n```json\n")
	for _, command := range rep.Commands {
		out.WriteString(jq.Compact(command))
		out.WriteByte('\n')
	}
	out.WriteString("```\n")
	return out.String()
}

func renderMetricTable(out *strings.Builder, rep *Report) {
	var data struct {
		Metrics *corpuscore.Metrics `json:"metrics"`
	}
	if json.Unmarshal(rep.TrendCorpus, &data) != nil {
		data.Metrics = nil
	}
	m := data.Metrics
	if m == nil {
		m = corpuscore.Measure(nil)
	}
	rows := []struct {
		name string
		rate corpuscore.Measurement
	}{
		{"precision", m.Precision},
		{"recall", m.Recall},
		{"false_discovery_rate", m.FalseDiscoveryRate},
		{"any_of_k_recall", rep.AnyOfKRecall},
		{"control_fp_rate", m.ControlFPRate},
		{"control_findings_per_case", m.ControlFindingsPerCase},
		{"severity_weighted_pooled_f1", m.SeverityWeightedPooledF1},
		{"signal_to_noise", m.SignalToNoise},
	}
	out.WriteString("## Metrics\n\n<!-- prettier-ignore -->\n" +
		"| Metric | Value | Numerator | Denominator | n | Unit |\n| --- | ---: | ---: | ---: | ---: | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(out, "| %s | %s | %d | %d | %d | %s |\n", r.name, metricValue(r.rate.Value),
			r.rate.Numerator, r.rate.Denominator, r.rate.N, cell(r.rate.Unit))
	}
	fmt.Fprintf(out, "\nIncomplete first-k cohorts: %d.\n\n", rep.IncompleteCohorts)
	fmt.Fprintf(out, "Cost USD: %s (n=%d/%d samples).  \nLatency seconds: %s (n=%d/%d samples).\n",
		recordedValue(m.CostUSD), m.CostN, m.Samples, recordedValue(m.LatencyS), m.LatencyN, m.Samples)
}

// renderAdjudication prints the two figures side by side: the strict rate the
// run measured and the rate its record reclassifies to. Without a record the
// section is absent, so a report never borrows the strict value for it.
func renderAdjudication(out *strings.Builder, rep *Report) {
	adj := rep.Adjudication
	if adj == nil {
		return
	}
	var strict trendRates
	if json.Unmarshal(rep.TrendCorpus, &strict) != nil {
		strict = trendRates{}
	}
	out.WriteString("\n## Adjudication\n\n")
	fmt.Fprintf(out, "Record: `%s` (sha256 `%s`), run %s. %d %s: %d real-unlisted, %d matches-ground-truth, %d false-positive.\n\n",
		cell(adj.Record), cell(adj.SHA256), cell(adj.RunID), adj.Entries, entryWord(adj.Entries),
		adj.RealUnlisted, adj.MatchesGroundTruth, adj.FalsePositives)
	out.WriteString("<!-- prettier-ignore -->\n| Metric | Strict | Adjudicated |\n| --- | ---: | ---: |\n")
	fmt.Fprintf(out, "| precision | %s | %s |\n", metricValue(strict.Precision), metricValue(adj.PrecisionAdjudicated))
	fmt.Fprintf(out, "| recall | %s | %s |\n", metricValue(strict.Recall), metricValue(adj.RecallAdjudicated))
	renderKeywordMisses(out, adj.KeywordMisses)
}

// renderKeywordMisses lists every finding a record called the ground-truth
// defect the keywords missed: the case whose vocabulary the next corpus
// version widens.
func renderKeywordMisses(out *strings.Builder, misses []KeywordMiss) {
	fmt.Fprintf(out, "\n### Keyword misses\n\n%d finding(s) named a ground-truth bug the case's keywords missed.\n\n", len(misses))
	if len(misses) == 0 {
		return
	}
	out.WriteString("<!-- prettier-ignore -->\n| Case | Sample | Finding | Bug |\n| --- | ---: | --- | --- |\n")
	for _, m := range misses {
		fmt.Fprintf(out, "| %s | %d | %s %s | %d %s |\n", cell(m.Case), m.Sample,
			cell(m.Sig), cell(locator(m.Path, m.Line)), m.Bug, cell(locator(m.BugPath, m.BugLine)))
	}
}

func entryWord(n int) string {
	if n == 1 {
		return "entry"
	}
	return "entries"
}

func renderSampleSpreads(out *strings.Builder, cases []CaseSpread) {
	ordered := slices.Clone(cases)
	slices.SortFunc(ordered, func(a, b CaseSpread) int { return strings.Compare(a.Case, b.Case) })
	out.WriteString("\n## Per-case sample spreads\n\n<!-- prettier-ignore -->\n" +
		"| Case | Metric | Median | Min | Max | n |\n| --- | --- | ---: | ---: | ---: | ---: |\n")
	for i := range ordered {
		c := &ordered[i]
		for _, name := range slices.Sorted(maps.Keys(c.Metrics)) {
			s := c.Metrics[name]
			fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %d |\n", cell(c.Case), cell(name),
				recordedValue(s.Median), recordedValue(s.Min), recordedValue(s.Max), s.N)
		}
	}
}

func metricValue(v *float64) string {
	if v == nil {
		return jsonNullValue
	}
	return corpuscore.RateText(*v)
}

func recordedValue(v *float64) string {
	if v == nil {
		return jsonNullValue
	}
	return fmt.Sprintf("%.6f", *v)
}

func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(s)
}

func renderEligibility(out *strings.Builder, rep *Report) {
	if rep.Citable {
		out.WriteString("**Citable: yes**\n\n")
	} else {
		reason := rep.CitableReason
		if reason == "" {
			reason = "eligibility not recorded"
		}
		fmt.Fprintf(out, "**Citable: no — %s**\n\n", cell(reason))
	}
	total := "not recorded"
	if rep.Corpus.TotalCases > 0 {
		total = strconv.Itoa(rep.Corpus.TotalCases)
	}
	fmt.Fprintf(out, "Selected cases: %d; total corpus cases: %s. Successful samples: %d.\n\n",
		rep.Corpus.Cases, total, len(rep.Bundles))
}

func renderFailedSamples(out *strings.Builder, rep *Report) {
	failures := publicationFailures(rep)
	fmt.Fprintf(out, "Failed or incomplete samples: %d.\n\n", len(failures))
	if len(failures) == 0 {
		return
	}
	out.WriteString("<!-- prettier-ignore -->\n| Case | Sample | Reason |\n| --- | ---: | --- |\n")
	for _, f := range failures {
		fmt.Fprintf(out, "| %s | %d | %s |\n", cell(f.Case), f.Run, cell(f.Error))
	}
	out.WriteByte('\n')
}

func publicationFailures(rep *Report) []Failure {
	failures := slices.Clone(rep.Failures)
	seen := map[string]int{}
	for _, b := range rep.Bundles {
		seen[sampleKey(b.Case, b.Run)]++
	}
	failed := map[string]bool{}
	for _, f := range failures {
		failed[sampleKey(f.Case, f.Run)] = true
	}
	repeats := rep.Repeats
	if repeats == 0 {
		repeats = rep.Agent.Runs
	}
	for _, id := range rep.Corpus.CaseIDs {
		failures = append(failures, missingSamples(id, repeats, seen, failed)...)
	}
	slices.SortFunc(failures, func(a, b Failure) int {
		if n := strings.Compare(a.Case, b.Case); n != 0 {
			return n
		}
		return a.Run - b.Run
	})
	return failures
}

func sampleKey(id string, sample int) string { return id + "\x00" + strconv.Itoa(sample) }
func sampleProblem(count int) string {
	if count == 0 {
		return "sample result missing"
	}
	if count > 1 {
		return "duplicate sample results; cohort is incomplete"
	}
	return ""
}

func missingSamples(id string, repeats int, seen map[string]int, failed map[string]bool) []Failure {
	var out []Failure
	for sample := 1; sample <= repeats; sample++ {
		key := sampleKey(id, sample)
		if failed[key] {
			continue
		}
		if reason := sampleProblem(seen[key]); reason != "" {
			out = append(out, Failure{Case: id, Run: sample, Error: reason})
		}
	}
	return out
}
