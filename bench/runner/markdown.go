package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	maxPublicationBytes = 64 << 20
	jsonNullValue       = "null"
)

func renderFile(path string, w io.Writer) error {
	raw, err := jq.CappedFile(path, maxPublicationBytes)
	if err != nil {
		return jq.NewErrf("publication: %s", err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return jq.NewErrf("publication JSON: %s", err)
	}
	if rep.SchemaVersion != "1" {
		return jq.NewErr("publication schema_version must be 1")
	}
	if _, err := io.WriteString(w, renderMarkdown(&rep)); err != nil {
		return jq.NewErrf("render: %s", err)
	}
	return nil
}

func renderMarkdown(rep *Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Benchmark %s\n\n", cell(rep.Date))
	if rep.Seeded {
		out.WriteString("**not a model-quality claim**\n\n")
	}
	fmt.Fprintf(&out, "Corpus commit: `%s` (dirty: %t)  \n"+
		"Prompts hash: `%s`  \nTool version: `%s`  \nDate: %s  \nSampling: k=%d, repeats=%d  \n",
		cell(rep.Corpus.Commit), rep.Corpus.Dirty, cell(rep.PromptsHash), cell(rep.ToolVersion), cell(rep.GeneratedAt), rep.K, rep.Repeats)
	fmt.Fprintf(&out, "Severity weights: critical=%d, important=%d, informational=%d\n\n",
		rep.SeverityWeights.Critical, rep.SeverityWeights.Important, rep.SeverityWeights.Informational)
	renderMetricTable(&out, rep)
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
		{"false_positive_rate", m.FalsePositiveRate},
		{"any_of_k_recall", rep.AnyOfKRecall},
		{"control_fp_rate", m.ControlFPRate},
		{"severity_weighted_f1", m.SeverityWeightedF1},
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

func metricValue(v any) string {
	if v == nil {
		return jsonNullValue
	}
	return corpuscore.RateText(v)
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
