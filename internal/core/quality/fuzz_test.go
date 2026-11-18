package quality

import (
	"regexp"
	"testing"
)

// FuzzMatchFixtureRule keeps the operator-authored regex rules crash-free,
// including invalid patterns that catalog validation must refuse before they
// reach the matcher.
func FuzzMatchFixtureRule(f *testing.F) {
	f.Add("alpha", "ALPHA here")
	f.Add(`\bnext\b`, "run the next command")
	f.Fuzz(func(_ *testing.T, pattern, text string) {
		m := &Matcher{compiled: map[string]*regexp.Regexp{}}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return
		}
		m.compiled[pattern] = re
		_ = m.Matches(&Finding{Path: "x.go", Claim: text}, [][]string{{pattern}})
	})
}
