package calibratecore

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runProbe ports the self-grading probe: baseline grades regenerated from
// findings.json every run, agent judgments merged sticky, kb-grades.jsonl
// rewritten deterministically.
func (st *state) runProbe(dir string) *core.RefusalError {
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return st.loadRefusal(err)
	}
	now := st.req.TimeUTC()
	bname := strings.TrimPrefix(dir, ".review/")
	known := knownSigs(entries)
	fileJudgments, err := readJudgeFile(st.req.JudgeFile, known, dir)
	if err != nil {
		return st.loadRefusal(err)
	}
	specJudgments, err := parseJudgeSpecs(st.req.Specs, known, dir)
	if err != nil {
		return st.loadRefusal(err)
	}
	merged := mergeJudgments(priorJudgments(dir), append(fileJudgments, specJudgments...))
	grades := buildGrades(entries, bname, now, merged)
	if err := writeGrades(dir, grades); err != nil {
		return st.loadRefusal(err)
	}
	st.emitProbeHuman(dir, bname, grades)
	st.probe = &probeResult{bundle: bname, grades: grades, counts: countGrades(grades)}
	return nil
}

// judge is one agent judgment.
type judge struct {
	helpful any
	sig     string
	verdict string
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
		sig, _ := m.Get(keySig)
		known = append(known, sig)
	}
	return known
}

// judgeUnreadable ports the missing-or-unreadable judge file refusal.
func judgeUnreadable(path string) *core.RefusalError {
	return &core.RefusalError{Code: codeBadJudgment, Message: fmt.Sprintf(
		"judge file '%s' is missing or unreadable (write one {\"sig\": ..., \"agent_verdict\": ...} "+
			"object per line, or a JSON array of them)", path)}
}

// readJudgeFile ports the --judge-file fragment: a JSON array or JSONL of
// judgment objects, validated against the known sigs.
func readJudgeFile(path string, known []any, dir string) ([]judge, error) {
	if path == "" {
		return nil, nil
	}
	docs, err := readJudgeDocs(path)
	if err != nil {
		return nil, err
	}
	var out []judge
	for i, doc := range docs {
		j, err := checkJudgeEntry(path, i, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return checkJudgeDocs(path, out, known, dir)
}

// readJudgeDocs ports the fragment file read and decode.
func readJudgeDocs(path string) ([]any, error) {
	if !isRegular(path) || !readable(path) {
		return nil, judgeUnreadable(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, judgeUnreadable(path)
	}
	return slurpJudgeDocs(path, raw)
}

// checkJudgeDocs validates the fragment entries against the known sigs.
func checkJudgeDocs(path string, out []judge, known []any, dir string) ([]judge, error) {
	if unknown := firstUnknown(out, known); unknown != "" {
		return nil, &core.RefusalError{Code: codeUnknownJudgment, Message: fmt.Sprintf(
			"judge file '%s' sig '%s' matches no finding in %s/findings.json (copy the full sig from report output)", path, unknown, dir)}
	}
	return out, nil
}

// slurpJudgeDocs ports the fragment decode: a single JSON array unwraps to
// its elements, otherwise one judgment per JSONL row.
func slurpJudgeDocs(path string, raw []byte) ([]any, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(string(raw)))
	if err != nil {
		return nil, &core.RefusalError{Code: codeBadJudgment, Message: fmt.Sprintf(
			"judge file '%s' is not valid JSON or JSONL (one {\"sig\": ..., \"agent_verdict\": ...} per line, or a JSON array)", path)}
	}
	if len(docs) == 1 {
		if arr, ok := docs[0].([]any); ok {
			docs = arr
		}
	}
	return docs, nil
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
		return judge{}, &core.RefusalError{Code: codeBadJudgment, Message: fmt.Sprintf("judge file '%s' entry %d: %s", path, i, detail)}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return fail("not an object (want {\"sig\", \"agent_verdict\"})")
	}
	sig, ok := judgeSig(m)
	if !ok {
		return fail("sig must be a non-empty string")
	}
	verdict, ok := judgeVerdict(m)
	if !ok {
		return fail(fmt.Sprintf("(%s): agent_verdict must be real|false|unknown", sig))
	}
	helpful, ok := judgeHelpful(m)
	if !ok {
		return fail(fmt.Sprintf("(%s): agent_helpful must be true|false|null", sig))
	}
	return judge{sig: sig, verdict: verdict, helpful: helpful}, nil
}

// judgeSig ports the sig gate: a non-empty string.
func judgeSig(m map[string]any) (string, bool) {
	sig, ok := m[keySig].(string)
	return sig, ok && sig != ""
}

// judgeVerdict ports the agent_verdict gate.
func judgeVerdict(m map[string]any) (string, bool) {
	verdict, ok := m[keyAgentVerdict].(string)
	return verdict, ok && (verdict == verdictReal || verdict == verdictFalse || verdict == verdictUnknown)
}

// judgeHelpful ports the agent_helpful gate: missing reads null.
func judgeHelpful(m map[string]any) (any, bool) {
	helpful, ok := m[keyAgentHelpful]
	if !ok || helpful == nil {
		return nil, true
	}
	return helpful, helpful == true || helpful == false
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
		return judge{}, &core.RefusalError{Code: codeBadJudgment, Message: detail}
	}
	sig, rest, found := strings.Cut(spec, ":")
	if !found || sig == "" {
		return bad(fmt.Sprintf("malformed --judge '%s' (want SIG:VERDICT[:HELPFUL], e.g. --judge abc123:real:true)", spec))
	}
	verdict, helpful := splitVerdict(rest)
	if verdict != verdictReal && verdict != verdictFalse && verdict != verdictUnknown {
		return bad(fmt.Sprintf("malformed --judge '%s': verdict '%s' (want real|false|unknown)", spec, verdict))
	}
	if helpful != helpfulTrue && helpful != helpfulFalse && helpful != helpfulNull {
		return bad(fmt.Sprintf("malformed --judge '%s': helpful '%s' (want true|false|null)", spec, helpful))
	}
	if !containsSig(known, sig) {
		return judge{}, &core.RefusalError{Code: codeUnknownJudgment, Message: fmt.Sprintf(
			"judgment sig '%s' matches no finding in %s/findings.json (copy the full sig from report output)", sig, dir)}
	}
	return buildSpecJudge(sig, verdict, helpful), nil
}

// splitVerdict ports the VERDICT[:HELPFUL] split: a missing helpful reads
// null.
func splitVerdict(rest string) (string, string) {
	verdict, helpful, _ := strings.Cut(rest, ":")
	if helpful == "" {
		helpful = helpfulNull
	}
	return verdict, helpful
}

// buildSpecJudge ports the validated spec assembly.
func buildSpecJudge(sig, verdict, helpful string) judge {
	var helpfulVal any
	switch helpful {
	case helpfulTrue:
		helpfulVal = true
	case helpfulFalse:
		helpfulVal = false
	}
	return judge{sig: sig, verdict: verdict, helpful: helpfulVal}
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
		if j, ok := priorJudge(doc); ok {
			out = append(out, j)
		}
	}
	return out
}

// priorJudge ports one sticky prior: objects with a string sig and a valid
// agent verdict; agent_helpful passes through unvalidated.
func priorJudge(doc any) (judge, bool) {
	m, ok := doc.(map[string]any)
	if !ok {
		return judge{}, false
	}
	sig, ok := m[keySig].(string)
	if !ok {
		return judge{}, false
	}
	verdict, ok := m[keyAgentVerdict].(string)
	if !ok || (verdict != verdictReal && verdict != verdictFalse && verdict != verdictUnknown) {
		return judge{}, false
	}
	helpful, ok := m[keyAgentHelpful]
	if !ok || helpful == nil {
		helpful = nil
	}
	return judge{sig: sig, verdict: verdict, helpful: helpful}, true
}

// mergeJudgments merges priors with new judgments: later wins per sig.
func mergeJudgments(prior, latest []judge) map[string]judge {
	merged := map[string]judge{}
	for _, j := range prior {
		merged[j.sig] = j
	}
	for _, j := range latest {
		merged[j.sig] = j
	}
	return merged
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
	case readcommon.StatusOf(entry) == statusAddressed:
		helpful = true
	case readcommon.StatusOf(entry) == statusFalsePositive:
		helpful = false
	}
	sigKey := ""
	if s, ok := entrySig(entry).(string); ok {
		sigKey = s
	}
	agentVerdict, agentHelpful, agreement := gradeAgent(merged, sigKey, baseline)
	return jq.NewObj(
		"at", now,
		"bundle", bname,
		keySig, entrySig(entry),
		"verdict", baseline,
		"rule", rule,
		"helpful", helpful,
		keyAgentVerdict, agentVerdict,
		"agent_helpful", agentHelpful,
		"agreement", agreement,
		"grader", "calibrate-probe",
	)
}

// gradeAgent folds the merged judgment and baseline agreement for one sig.
// agent_helpful mirrors the shell's `// null` default: false reads null
// (jq's // replaces false as well as null), true passes through.
func gradeAgent(merged map[string]judge, sigKey, baseline string) (agentVerdict, agentHelpful, agreement any) {
	j, judged := merged[sigKey]
	if !judged {
		return nil, nil, nil
	}
	agentVerdict = j.verdict
	agentHelpful = j.helpful
	if agentHelpful == false {
		agentHelpful = nil
	}
	if (j.verdict == verdictReal || j.verdict == verdictFalse) && (baseline == verdictReal || baseline == verdictFalse) {
		agreement = j.verdict == baseline
	}
	return agentVerdict, agentHelpful, agreement
}

// entrySig reads one entry's raw sig (nil for non-objects).
func entrySig(entry any) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	sig, _ := m.Get(keySig)
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
	case statusAddressed:
		return verdictReal
	case statusFalsePositive:
		return verdictFalse
	default:
		return verdictUnknown
	}
}

// gradesFilePerm stages the probe output readable by the owner: the rows
// carry no secrets, but 0644 is never needed.
const gradesFilePerm = 0o600

// writeGrades rewrites kb-grades.jsonl deterministically through the shared
// atomic path: one compact row per grade plus a trailing newline, so an
// interrupted write keeps the last good grades byte-identical (audit 19).
func writeGrades(dir string, grades []*jq.Obj) error {
	var sb strings.Builder
	for _, g := range grades {
		sb.WriteString(g.MarshalCompact())
		sb.WriteByte('\n')
	}
	if err := atomic.WriteFileMode(dir+"/kb-grades.jsonl", []byte(sb.String()), gradesFilePerm); err != nil {
		return &core.RefusalError{Code: codeCalibrateFailed, Message: fmt.Sprintf("could not write %s/kb-grades.jsonl: %v", dir, err)}
	}
	return nil
}

// probeCounts carries the probe tallies.
type probeCounts struct {
	rate                                any
	real, falseVerdicts, unknown, total int
	sampled, judged, agreed, disagreed  int
}

// countGrades tallies the grades.
func countGrades(grades []*jq.Obj) probeCounts {
	var c probeCounts
	c.total = len(grades)
	for _, g := range grades {
		verdict, _ := g.Get("verdict")
		switch verdict {
		case verdictReal:
			c.real++
		case verdictFalse:
			c.falseVerdicts++
		default:
			c.unknown++
		}
		tallyGradeAgent(&c, g, verdict)
	}
	if c.judged > 0 {
		c.rate = float64(c.agreed) / float64(c.judged)
	}
	return c
}

// tallyGradeAgent folds one grade's agent verdict and agreement.
func tallyGradeAgent(c *probeCounts, g *jq.Obj, verdict any) {
	agent, _ := g.Get(keyAgentVerdict)
	av, ok := agent.(string)
	if !ok {
		av = ""
	}
	if av == verdictReal || av == verdictFalse {
		c.sampled++
		tallyAgreement(c, av, verdict, agreementOf(g))
	}
}

// agreementOf reads the agreement flag (nil when absent).
func agreementOf(g *jq.Obj) any {
	agreement, _ := g.Get("agreement")
	return agreement
}

// tallyAgreement folds a sampled grade's agreement.
func tallyAgreement(c *probeCounts, av string, verdict, agreement any) {
	if (av == verdictReal || av == verdictFalse) && (verdict == verdictReal || verdict == verdictFalse) {
		c.judged++
		if agreement == true {
			c.agreed++
		} else {
			c.disagreed++
		}
	}
}

// probeResult carries the completed probe for the typed data profile.
type probeResult struct {
	bundle string
	grades []*jq.Obj
	counts probeCounts
}

// nextProbe ports the probe next step: scored pairs feed the evidence
// gate, else nothing weighs.
func nextProbe(c *probeCounts) string {
	next := "no real findings graded — nothing to weigh"
	if c.real > 0 {
		next = "adopted-pair signal present — feed X3 into the H2 evidence gate"
	}
	return next
}

// Wire renders the probe data in published wire order.
func (r *probeResult) Wire() *jq.Obj {
	c := r.counts
	items := make([]any, 0, len(r.grades))
	for _, g := range r.grades {
		items = append(items, g)
	}
	return jq.NewObj(
		"bundle", r.bundle,
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
	)
}

// emitProbeHuman ports the probe human lines.
func (st *state) emitProbeHuman(dir, bname string, grades []*jq.Obj) {
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
			c.sampled, c.judged, c.agreed, c.disagreed, rateText)
	}
	st.human(fmt.Sprintf("probe: %s#%s — %d finding(s): real=%d false=%d unknown=%d%s (grades: %s/kb-grades.jsonl)",
		st.req.Repo, bname, c.total, c.real, c.falseVerdicts, c.unknown, judgeText, dir))
	for _, g := range grades {
		st.human(gradeLine(g))
	}
}

// gradeLine ports one per-grade line.
func gradeLine(g *jq.Obj) string {
	sig, _ := g.Get(keySig)
	verdict, _ := g.Get("verdict")
	line := readcommon.Prefix(sigString(sig)) + " [" + jq.Str(verdict) + "]"
	rule, _ := g.Get("rule")
	if rule == nil {
		line += " uncited"
	} else {
		helpful, _ := g.Get("helpful")
		line += " rule " + jq.Str(rule) + " helpful=" + readcommon.Tostring(helpful)
	}
	agent, _ := g.Get(keyAgentVerdict)
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

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
