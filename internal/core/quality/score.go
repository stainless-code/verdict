package quality

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const statusPass = "PASS"

// Matcher is the compiled, deterministic scorer. Every pattern is
// case-insensitive and applied to lowercased path + "\n" + claim with no
// whitespace folding, matching the harness the hand scores came from.
type Matcher struct {
	compiled   map[string]*regexp.Regexp
	praise     *regexp.Regexp
	defectVerb *regexp.Regexp
}

// NewMatcher compiles every pattern in the catalog once. Catalog validation
// has already refused invalid patterns.
func NewMatcher(c *Catalog) *Matcher {
	m := &Matcher{compiled: map[string]*regexp.Regexp{}}
	for i := range c.Fixtures {
		compileDefects(m, c.Fixtures[i].Known)
		compileDefects(m, c.Fixtures[i].Bonus)
	}
	m.praise = regexp.MustCompile("(?i)" + c.ReviewSignals.PraiseRE)
	m.defectVerb = regexp.MustCompile("(?i)" + c.ReviewSignals.DefectVerbRE)
	return m
}

func compileDefects(m *Matcher, defects []Defect) {
	for i := range defects {
		for _, rule := range defects[i].Matchers {
			compileRule(m, rule)
		}
	}
}

func compileRule(m *Matcher, rule []string) {
	for _, pattern := range rule {
		if _, ok := m.compiled[pattern]; !ok {
			m.compiled[pattern] = regexp.MustCompile("(?i)" + pattern)
		}
	}
}

// Matches applies one defect's rules to one finding: all patterns in a rule
// must match, and any rule may match.
func (m *Matcher) Matches(f *Finding, rules [][]string) bool {
	text := strings.ToLower(f.Path + "\n" + f.Claim)
	for _, rule := range rules {
		if m.ruleMatches(rule, text) {
			return true
		}
	}
	return false
}

func (m *Matcher) ruleMatches(rule []string, text string) bool {
	for _, pattern := range rule {
		if !m.compiled[pattern].MatchString(text) {
			return false
		}
	}
	return true
}

// Hit reports whether any finding satisfies a defect's rules. Findings are
// not consumed: one finding may hit several related defects.
func (m *Matcher) Hit(findings []Finding, d Defect) bool {
	for i := range findings {
		if m.Matches(&findings[i], d.Matchers) {
			return true
		}
	}
	return false
}

func (m *Matcher) isPraise(f *Finding) bool {
	text := strings.ToLower(f.Claim)
	return m.praise.MatchString(text) && !m.defectVerb.MatchString(text)
}

// RunScore is one scored recording: per-defect hits plus the diagnostic
// columns the calibration table prints.
type RunScore struct {
	Hits          map[string]bool
	BonusHits     map[string]bool
	GatePassed    *bool
	Recording     string
	Fixture       string
	Driver        string
	InfraError    string
	RefusalCode   string
	Praise        int
	NullSeverity  int
	OtherFindings int
	Scored        bool
}

// Score grades one recording against one fixture's defects.
func Score(m *Matcher, f *Fixture, findings []Finding, meta *RecordingMeta,
	gate *bool, recording string,
) *RunScore {
	run := &RunScore{
		Recording:   recording,
		Fixture:     f.ID,
		Driver:      meta.Driver,
		Hits:        map[string]bool{},
		BonusHits:   map[string]bool{},
		GatePassed:  gate,
		InfraError:  meta.InfraError,
		RefusalCode: meta.RefusalCode,
		Scored:      meta.InfraError == "",
	}
	if !run.Scored {
		return run
	}
	matched := map[string][][]string{}
	collectHits(m, f.Known, findings, run.Hits, matched)
	collectHits(m, f.Bonus, findings, run.BonusHits, matched)
	scoreDiagnostics(m, run, findings, matched)
	return run
}

func collectHits(m *Matcher, defects []Defect, findings []Finding,
	hits map[string]bool, matched map[string][][]string,
) {
	for i := range defects {
		d := &defects[i]
		hits[d.ID] = m.Hit(findings, *d)
		if hits[d.ID] {
			matched[d.ID] = d.Matchers
		}
	}
}

func scoreDiagnostics(m *Matcher, run *RunScore, findings []Finding, matched map[string][][]string) {
	for i := range findings {
		f := &findings[i]
		if f.Severity == "" {
			run.NullSeverity++
		}
		if m.isPraise(f) {
			run.Praise++
			continue
		}
		if !anyMatch(m, f, matched) {
			run.OtherFindings++
		}
	}
}

func anyMatch(m *Matcher, f *Finding, matched map[string][][]string) bool {
	for _, rules := range matched {
		if m.Matches(f, rules) {
			return true
		}
	}
	return false
}

// RecallHits renders one run's exact hit count, as the harness table does.
func RecallHits(run *RunScore, known int) string {
	if !run.Scored {
		return "n/a"
	}
	return strconv.Itoa(countTrue(run.Hits)) + "/" + strconv.Itoa(known)
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

// CalibrationRowResult is one row of the hand-score table. Hand and harness
// expectations are compared as hit-id sets, as the harness calibrate did.
type CalibrationRowResult struct {
	Recording    string
	Fixture      string
	Driver       string
	Hand         string
	Harness      string
	HandHits     string
	HarnessHits  string
	Status       string
	Reason       string
	Praise       int
	NullSeverity int
}

// CalibrationResult holds every row and the single reproduced verdict.
type CalibrationResult struct {
	Rows       []CalibrationRowResult
	Reproduced bool
}

// Calibrate compares each recorded run with the hand score pinned in the
// catalog. The hand scores are literals from the archived RUN.md; the scorer
// never supplies its own expectation.
func Calibrate(c *Catalog, runs []RunScore) *CalibrationResult {
	byName := make(map[string]*RunScore, len(runs))
	for i := range runs {
		byName[runs[i].Recording] = &runs[i]
	}
	result := &CalibrationResult{Reproduced: true}
	fixtures := map[string]*Fixture{}
	for i := range c.Fixtures {
		fixtures[c.Fixtures[i].ID] = &c.Fixtures[i]
	}
	for i := range c.Calibration {
		row := calibrationRow(byName, fixtures, &c.Calibration[i])
		result.Rows = append(result.Rows, *row)
		if row.Status != statusPass {
			result.Reproduced = false
		}
	}
	return result
}

func calibrationRow(byName map[string]*RunScore, fixtures map[string]*Fixture,
	row *CalibrationRow,
) *CalibrationRowResult {
	hand := set(row.Hand)
	missing := &CalibrationRowResult{
		Recording: row.Recording, Fixture: row.Fixture, Driver: row.Driver,
		Hand: row.HandLabel, Harness: notApplicable, HandHits: orDash(joinSet(hand)),
		HarnessHits: "-", Status: "FAIL",
	}
	run := byName[row.Recording]
	fixture := fixtures[row.Fixture]
	if run == nil || fixture == nil {
		return missing
	}
	harness := hits(run.Hits)
	status := "FAIL"
	reason := ""
	switch {
	case !run.Scored:
		reason = "unscored (infra error: " + run.InfraError + ")"
	case run.Fixture != row.Fixture:
		reason = "recording names fixture " + run.Fixture + ", calibration row names " + row.Fixture
	case sameSet(hand, harness):
		status = "PASS"
	}
	return &CalibrationRowResult{
		Recording:    row.Recording,
		Fixture:      row.Fixture,
		Driver:       row.Driver,
		Hand:         row.HandLabel,
		Harness:      RecallHits(run, len(fixture.Known)),
		HandHits:     orDash(joinSet(hand)),
		HarnessHits:  orDash(joinSet(harness)),
		Praise:       run.Praise,
		NullSeverity: run.NullSeverity,
		Status:       status,
		Reason:       reason,
	}
}

func set(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func hits(values map[string]bool) map[string]bool {
	out := map[string]bool{}
	for id, hit := range values {
		if hit {
			out[id] = true
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func joinSet(values map[string]bool) string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

// CalibrationTable renders the eight-row table the harness published.
func CalibrationTable(result *CalibrationResult) string {
	var sb strings.Builder
	sb.WriteString("| bundle | fixture | driver | hand | harness | hand hits | harness hits | praise | null sev | status | reason |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- | --- | ---: | ---: | --- | --- |\n")
	for i := range result.Rows {
		row := &result.Rows[i]
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s | %d | %d | %s | %s |\n",
			row.Recording, row.Fixture, row.Driver, row.Hand, row.Harness,
			orDash(row.HandHits), orDash(row.HarnessHits), row.Praise, row.NullSeverity,
			row.Status, orDash(row.Reason))
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
