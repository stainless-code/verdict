package quality

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

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
	for _, f := range c.Fixtures {
		for _, d := range append(append([]Defect{}, f.Known...), f.Bonus...) {
			for _, rule := range d.Matchers {
				for _, pattern := range rule {
					if _, ok := m.compiled[pattern]; !ok {
						m.compiled[pattern] = regexp.MustCompile("(?i)" + pattern)
					}
				}
			}
		}
	}
	m.praise = regexp.MustCompile("(?i)" + c.ReviewSignals.PraiseRE)
	m.defectVerb = regexp.MustCompile("(?i)" + c.ReviewSignals.DefectVerbRE)
	return m
}

// Matches applies one defect's rules to one finding: all patterns in a rule
// must match, and any rule may match.
func (m *Matcher) Matches(f Finding, rules [][]string) bool {
	text := strings.ToLower(f.Path + "\n" + f.Claim)
	for _, rule := range rules {
		all := true
		for _, pattern := range rule {
			if !m.compiled[pattern].MatchString(text) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// Hit reports whether any finding satisfies a defect's rules. Findings are
// not consumed: one finding may hit several related defects.
func (m *Matcher) Hit(findings []Finding, d Defect) bool {
	for _, f := range findings {
		if m.Matches(f, d.Matchers) {
			return true
		}
	}
	return false
}

func (m *Matcher) isPraise(f Finding) bool {
	text := strings.ToLower(f.Claim)
	return m.praise.MatchString(text) && !m.defectVerb.MatchString(text)
}

// RunScore is one scored recording: per-defect hits plus the diagnostic
// columns the calibration table prints.
type RunScore struct {
	Recording     string
	Fixture       string
	Driver        string
	Hits          map[string]bool
	BonusHits     map[string]bool
	Praise        int
	NullSeverity  int
	OtherFindings int
	GatePassed    *bool
	InfraError    string
	RefusalCode   string
	Scored        bool
}

// Score grades one recording against one fixture's defects.
func Score(m *Matcher, f *Fixture, findings []Finding, meta RecordingMeta, recording string) *RunScore {
	run := &RunScore{
		Recording:   recording,
		Fixture:     f.ID,
		Driver:      meta.Driver,
		Hits:        map[string]bool{},
		BonusHits:   map[string]bool{},
		GatePassed:  meta.GatePassed,
		InfraError:  meta.InfraError,
		RefusalCode: meta.RefusalCode,
		Scored:      meta.InfraError == "",
	}
	if !run.Scored {
		return run
	}
	matched := map[string][][]string{}
	for _, d := range f.Known {
		run.Hits[d.ID] = m.Hit(findings, d)
		if run.Hits[d.ID] {
			matched[d.ID] = d.Matchers
		}
	}
	for _, d := range f.Bonus {
		run.BonusHits[d.ID] = m.Hit(findings, d)
		if run.BonusHits[d.ID] {
			matched[d.ID] = d.Matchers
		}
	}
	for _, finding := range findings {
		if finding.Severity == "" {
			run.NullSeverity++
		}
		if m.isPraise(finding) {
			run.Praise++
			continue
		}
		other := true
		for _, rules := range matched {
			if m.Matches(finding, rules) {
				other = false
				break
			}
		}
		if other {
			run.OtherFindings++
		}
	}
	return run
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
	Praise       int
	NullSeverity int
	Status       string
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
	byName := make(map[string]RunScore, len(runs))
	for _, run := range runs {
		byName[run.Recording] = run
	}
	result := &CalibrationResult{Reproduced: true}
	fixtures := map[string]*Fixture{}
	for i := range c.Fixtures {
		fixtures[c.Fixtures[i].ID] = &c.Fixtures[i]
	}
	for _, row := range c.Calibration {
		run, ok := byName[row.Recording]
		if !ok {
			result.Reproduced = false
			continue
		}
		hand := set(row.Hand)
		harness := hits(run.Hits)
		fixture := fixtures[row.Fixture]
		if fixture == nil {
			result.Reproduced = false
			continue
		}
		status := "FAIL"
		if sameSet(hand, harness) {
			status = "PASS"
		} else {
			result.Reproduced = false
		}
		result.Rows = append(result.Rows, CalibrationRowResult{
			Recording:    row.Recording,
			Fixture:      row.Fixture,
			Driver:       row.Driver,
			Hand:         row.HandLabel,
			Harness:      RecallHits(&run, len(fixture.Known)),
			HandHits:     orDash(joinSet(hand)),
			HarnessHits:  orDash(joinSet(harness)),
			Praise:       run.Praise,
			NullSeverity: run.NullSeverity,
			Status:       status,
		})
	}
	return result
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

// CalibrationTable renders the seven-row table the harness published.
func CalibrationTable(result *CalibrationResult) string {
	var sb strings.Builder
	sb.WriteString("| bundle | fixture | driver | hand | harness | hand hits | harness hits | praise | null sev | status |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- | --- | ---: | ---: | --- |\n")
	for _, row := range result.Rows {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s | %d | %d | %s |\n",
			row.Recording, row.Fixture, row.Driver, row.Hand, row.Harness,
			orDash(row.HandHits), orDash(row.HarnessHits), row.Praise, row.NullSeverity, row.Status)
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
