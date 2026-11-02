package calibrate

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runProbe ports the self-grading probe: baseline grades regenerated from
// findings.json every run, agent judgments merged sticky, kb-grades.jsonl
// rewritten deterministically.
func runProbe(ctx context.Context, r *cli.Runner, req Request, dir string, specs []string, judgeFile string) int {
	_ = ctx
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return r.Refuse(err)
	}
	now := cli.NowUTC(r.Now())
	bname := strings.TrimPrefix(dir, ".review/")
	known := knownSigs(entries)
	fileJudgments, err := readJudgeFile(judgeFile, known, dir)
	if err != nil {
		return r.Refuse(err)
	}
	specJudgments, err := parseJudgeSpecs(specs, known, dir)
	if err != nil {
		return r.Refuse(err)
	}
	merged := mergeJudgments(priorJudgments(dir), append(fileJudgments, specJudgments...))
	grades := buildGrades(entries, bname, now, merged)
	if err := writeGrades(dir, grades); err != nil {
		return r.Refuse(err)
	}
	emitProbeHuman(r, req, dir, bname, grades)
	if r.JSONMode {
		emitProbeEnvelope(r, req, bname, now, grades)
	}
	return 0
}

// knownSigs ports the known-sigs array: every entry's raw sig (strings
// and nulls alike, like `jq -c '[.[].sig]'`).
func knownSigs(entries []any) []any {
	known := make([]any, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			known = append(known, nil)
			continue
		}
		sig, _ := m.Get("sig")
		known = append(known, sig)
	}
	return known
}

// readJudgeFile ports the --judge-file fragment: a JSON array or JSONL of
// judgment objects, validated against the known sigs.
func readJudgeFile(path string, known []any, dir string) ([]judge, error) {
	if path == "" {
		return nil, nil
	}
	if !isRegular(path) || !readable(path) {
		return nil, &cli.RefusalError{Code: "bad-judgment", Message: fmt.Sprintf(
			"judge file '%s' is missing or unreadable (write one {\"sig\": ..., \"agent_verdict\": ...} object per line, or a JSON array of them)", path)}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &cli.RefusalError{Code: "bad-judgment", Message: fmt.Sprintf(
			"judge file '%s' is missing or unreadable (write one {\"sig\": ..., \"agent_verdict\": ...} object per line, or a JSON array of them)", path)}
	}
	docs, err := jq.DecodeSlurp(strings.NewReader(string(raw)))
	if err != nil {
		return nil, &cli.RefusalError{Code: "bad-judgment", Message: fmt.Sprintf(
			"judge file '%s' is not valid JSON or JSONL (one {\"sig\": ..., \"agent_verdict\": ...} per line, or a JSON array)", path)}
	}
	if len(docs) == 1 {
		if arr, ok := docs[0].([]any); ok {
			docs = arr
		}
	}
	var out []judge
	for i, doc := range docs {
		j, err := checkJudgeEntry(path, i, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if unknown := firstUnknown(out, known); unknown != "" {
		return nil, &cli.RefusalError{Code: "unknown-judgment", Message: fmt.Sprintf(
			"judge file '%s' sig '%s' matches no finding in %s/findings.json (copy the full sig from report output)", path, unknown, dir)}
	}
	return out, nil
}

// readable reports whether path can be opened for reading.
func readable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// checkJudgeEntry validates one fragment entry.
func checkJudgeEntry(path string, i int, doc any) (judge, error) {
	fail := func(detail string) (judge, error) {
		return judge{}, &cli.RefusalError{Code: "bad-judgment", Message: fmt.Sprintf("judge file '%s' entry %d: %s", path, i, detail)}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return fail("not an object (want {\"sig\", \"agent_verdict\"})")
	}
	sig, ok := m["sig"].(string)
	if !ok || sig == "" {
		return fail("sig must be a non-empty string")
	}
	verdict, ok := m["agent_verdict"].(string)
	if !ok || (verdict != "real" && verdict != "false" && verdict != "unknown") {
		return fail(fmt.Sprintf("(%s): agent_verdict must be real|false|unknown", sig))
	}
	helpful, ok := m["agent_helpful"]
	if !ok || helpful == nil {
		helpful = nil
	} else if helpful != true && helpful != false {
		return fail(fmt.Sprintf("(%s): agent_helpful must be true|false|null", sig))
	}
	return judge{sig: sig, verdict: verdict, helpful: helpful}, nil
}

// firstUnknown returns the first judgment sig outside the known set.
func firstUnknown(judgments []judge, known []any) string {
	for _, j := range judgments {
		if !containsSig(known, j.sig) {
			return j.sig
		}
	}
	return ""
}

// containsSig reports jq == membership for sigs.
func containsSig(known []any, sig string) bool {
	for _, k := range known {
		if s, ok := k.(string); ok && s == sig {
			return true
		}
	}
	return false
}

// parseJudgeSpecs validates --judge specs against the known sigs, in order.
func parseJudgeSpecs(specs []string, known []any, dir string) ([]judge, error) {
	var out []judge
	for _, spec := range specs {
		j, err := checkJudgeSpec(spec, known, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// checkJudgeSpec validates one --judge SIG:VERDICT[:HELPFUL] spec.
func checkJudgeSpec(spec string, known []any, dir string) (judge, error) {
	bad := func(detail string) (judge, error) {
		return judge{}, &cli.RefusalError{Code: "bad-judgment", Message: detail}
	}
	colon := strings.Index(spec, ":")
	sig := spec
	rest := ""
	if colon >= 0 {
		sig = spec[:colon]
		rest = spec[colon+1:]
	}
	if colon < 0 || sig == "" {
		return bad(fmt.Sprintf("malformed --judge '%s' (want SIG:VERDICT[:HELPFUL], e.g. --judge abc123:real:true)", spec))
	}
	verdict := rest
	helpful := "null"
	if i := strings.Index(rest, ":"); i >= 0 {
		verdict = rest[:i]
		helpful = rest[i+1:]
	}
	if helpful == "" {
		helpful = "null"
	}
	if verdict != "real" && verdict != "false" && verdict != "unknown" {
		return bad(fmt.Sprintf("malformed --judge '%s': verdict '%s' (want real|false|unknown)", spec, verdict))
	}
	if helpful != "true" && helpful != "false" && helpful != "null" {
		return bad(fmt.Sprintf("malformed --judge '%s': helpful '%s' (want true|false|null)", spec, helpful))
	}
	if !containsSig(known, sig) {
		return judge{}, &cli.RefusalError{Code: "unknown-judgment", Message: fmt.Sprintf(
			"judgment sig '%s' matches no finding in %s/findings.json (copy the full sig from report output)", sig, dir)}
	}
	var helpfulVal any
	switch helpful {
	case "true":
		helpfulVal = true
	case "false":
		helpfulVal = false
	}
	return judge{sig: sig, verdict: verdict, helpful: helpfulVal}, nil
}

// priorJudgments ports the sticky prior judgments from kb-grades.jsonl:
// object rows with a string sig and a valid agent verdict; agent_helpful
// passes through unvalidated. Invalid files read as no priors.
func priorJudgments(dir string) []judge {
	raw, err := os.ReadFile(dir + "/kb-grades.jsonl")
	if err != nil {
		return nil
	}
	docs, err := jq.DecodeSlurp(strings.NewReader(string(raw)))
	if err != nil {
		return nil
	}
	var out []judge
	for _, doc := range docs {
		m, ok := doc.(map[string]any)
		if !ok {
			continue
		}
		sig, ok := m["sig"].(string)
		if !ok {
			continue
		}
		verdict, ok := m["agent_verdict"].(string)
		if !ok || (verdict != "real" && verdict != "false" && verdict != "unknown") {
			continue
		}
		helpful, ok := m["agent_helpful"]
		if !ok || helpful == nil {
			helpful = nil
		}
		out = append(out, judge{sig: sig, verdict: verdict, helpful: helpful})
	}
	return out
}

// mergeJudgments merges priors with new judgments: later wins per sig.
func mergeJudgments(prior, new []judge) map[string]judge {
	merged := map[string]judge{}
	for _, j := range prior {
		merged[j.sig] = j
	}
	for _, j := range new {
		merged[j.sig] = j
	}
	return merged
}

// grade is one kb-grades.jsonl row.
type grade struct {
	doc *jq.Obj
}

// buildGrades ports the grades computation over findings in store order.
func buildGrades(entries []any, bname, now string, merged map[string]judge) []*jq.Obj {
	grades := make([]*jq.Obj, 0, len(entries))
	for _, e := range entries {
		grades = append(grades, buildGrade(e, bname, now, merged))
	}
	return grades
}

// buildGrade ports one grade row.
func buildGrade(entry any, bname, now string, merged map[string]judge) *jq.Obj {
	cited := citedStrings(entry)
	baseline := baselineVerdict(entry)
	var rule any
	if len(cited) > 0 {
		rule = cited[0]
	}
	var helpful any
	switch {
	case len(cited) == 0:
		helpful = nil
	case readcommon.StatusOf(entry) == "addressed":
		helpful = true
	case readcommon.StatusOf(entry) == "false-positive":
		helpful = false
	}
	sigKey := ""
	if s, ok := entrySig(entry).(string); ok {
		sigKey = s
	}
	j, judged := merged[sigKey]
	var agentVerdict, agentHelpful, agreement any
	if judged {
		agentVerdict = j.verdict
		agentHelpful = j.helpful
		if (j.verdict == "real" || j.verdict == "false") && (baseline == "real" || baseline == "false") {
			agreement = j.verdict == baseline
		}
	}
	return jq.NewObj(
		"at", now,
		"bundle", bname,
		"sig", entrySig(entry),
		"verdict", baseline,
		"rule", rule,
		"helpful", helpful,
		"agent_verdict", agentVerdict,
		"agent_helpful", agentHelpful,
		"agreement", agreement,
		"grader", "calibrate-probe",
	)
}

// entrySig reads one entry's raw sig (nil for non-objects).
func entrySig(entry any) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	sig, _ := m.Get("sig")
	return sig
}

// citedStrings ports the baseline cite extraction: string ids from array
// kb values, nothing otherwise.
func citedStrings(entry any) []string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	kb, _ := m.Get("kb")
	list, ok := kb.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, id := range list {
		if s, ok := id.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// baselineVerdict ports the triage baseline: addressed reads real,
// false-positive reads false, everything else reads unknown.
func baselineVerdict(entry any) string {
	switch readcommon.StatusOf(entry) {
	case "addressed":
		return "real"
	case "false-positive":
		return "false"
	default:
		return "unknown"
	}
}

// writeGrades rewrites kb-grades.jsonl deterministically: one compact row
// per grade plus a trailing newline.
func writeGrades(dir string, grades []*jq.Obj) error {
	var sb strings.Builder
	for _, g := range grades {
		sb.WriteString(g.MarshalCompact())
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(dir+"/kb-grades.jsonl", []byte(sb.String()), 0o644); err != nil {
		return &cli.RefusalError{Code: "calibrate-failed", Message: fmt.Sprintf("could not write %s/kb-grades.jsonl: %v", dir, err)}
	}
	return nil
}

// probeCounts carries the probe tallies.
type probeCounts struct {
	real, falseVerdicts, unknown, total int
	sampled, judged, agreed, disagreed  int
	rate                                any
}

// countGrades tallies the grades.
func countGrades(grades []*jq.Obj) probeCounts {
	var c probeCounts
	c.total = len(grades)
	for _, g := range grades {
		verdict, _ := g.Get("verdict")
		switch verdict {
		case "real":
			c.real++
		case "false":
			c.falseVerdicts++
		default:
			c.unknown++
		}
		agent, _ := g.Get("agent_verdict")
		av, _ := agent.(string)
		agreement, _ := g.Get("agreement")
		if av == "real" || av == "false" {
			c.sampled++
		}
		if (av == "real" || av == "false") && (verdict == "real" || verdict == "false") {
			c.judged++
			if agreement == true {
				c.agreed++
			} else {
				c.disagreed++
			}
		}
	}
	if c.judged > 0 {
		c.rate = float64(c.agreed) / float64(c.judged)
	}
	return c
}

// emitProbeHuman ports the probe human lines.
func emitProbeHuman(r *cli.Runner, req Request, dir, bname string, grades []*jq.Obj) {
	c := countGrades(grades)
	judgeText := ""
	if c.sampled != 0 {
		rateText := "n/a"
		if c.judged != 0 {
			if rate, ok := c.rate.(float64); ok {
				rateText = readcommon.CanonFloat(rate)
			}
		}
		judgeText = fmt.Sprintf(" judgments: sampled=%d judged=%d agreed=%d disagreed=%d rate=%s",
			c.sampled, c.judged, c.agreed, c.disagreed, rateText) //nolint:forcetypeassert // guarded by c.judged
	}
	fmt.Fprintf(r.Human, "probe: %s#%s — %d finding(s): real=%d false=%d unknown=%d%s (grades: %s/kb-grades.jsonl)\n",
		req.Repo, bname, c.total, c.real, c.falseVerdicts, c.unknown, judgeText, dir)
	for _, g := range grades {
		fmt.Fprintln(r.Human, gradeLine(g))
	}
}

// gradeLine ports one per-grade line.
func gradeLine(g *jq.Obj) string {
	sig, _ := g.Get("sig")
	verdict, _ := g.Get("verdict")
	line := readcommon.Prefix(sigString(sig)) + " [" + jq.Str(verdict) + "]"
	rule, _ := g.Get("rule")
	if rule == nil {
		line += " uncited"
	} else {
		helpful, _ := g.Get("helpful")
		line += " rule " + jq.Str(rule) + " helpful=" + readcommon.Tostring(helpful)
	}
	agent, _ := g.Get("agent_verdict")
	if agent == nil {
		return line
	}
	agreement, _ := g.Get("agreement")
	switch agreement {
	case true:
		line += " agent=" + jq.Str(agent) + " agree"
	case false:
		line += " agent=" + jq.Str(agent) + " DISAGREE"
	default:
		line += " agent=" + jq.Str(agent) + " (no baseline)"
	}
	return line
}

// sigString renders a sig for prefixing (missing reads jq-style).
func sigString(sig any) string {
	if s, ok := sig.(string); ok {
		return s
	}
	return jq.Str(sig)
}

// emitProbeEnvelope ports the probe --json envelope.
func emitProbeEnvelope(r *cli.Runner, req Request, bname, now string, grades []*jq.Obj) {
	c := countGrades(grades)
	items := make([]any, 0, len(grades))
	for _, g := range grades {
		items = append(items, g)
	}
	next := "no real findings graded — nothing to weigh"
	if c.real > 0 {
		next = "adopted-pair signal present — feed X3 into the H2 evidence gate"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "calibrate",
		"repo", req.Repo,
		"pr", cli.PrJSON(req.Pr),
		"at", now,
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"bundle", bname,
			"grades", items,
			"counts", jq.NewObj(
				"findings", c.total,
				"real", c.real,
				"false", c.falseVerdicts,
				"unknown", c.unknown,
			),
			"judgment", jq.NewObj(
				"sampled", c.sampled,
				"judged", c.judged,
				"agreed", c.agreed,
				"disagreed", c.disagreed,
				"agreement_rate", c.rate,
			),
		),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
