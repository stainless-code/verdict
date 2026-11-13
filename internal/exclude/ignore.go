// Package exclude matches ordered review exclusions without reading files.
package exclude

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxBytes    = 64 << 10
	maxPatterns = 1000
	// ErrInvalid identifies an invalid ignore file.
	ErrInvalid parseError = "invalid ignore file"
	// ErrTooLarge identifies an oversized ignore file.
	ErrTooLarge parseError = "ignore file too large"
)

type parseError string

func (e parseError) Error() string { return string(e) }

// Rule identifies the source and pattern of one exclusion decision.
type Rule struct {
	ID      string
	Pattern string
	Reason  string
	Negate  bool
}

// ParseIgnore parses up to 1,000 ordered patterns in a 64 KiB UTF-8 file.
// IDs retain physical line numbers, including comments and empty lines.
func ParseIgnore(raw []byte, source string) ([]Rule, error) {
	if len(raw) > maxBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	return parseLines(strings.Split(string(raw), "\n"), source)
}

func parseLines(lines []string, source string) ([]Rule, error) {
	var rules []Rule
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if ignoredLine(line) {
			continue
		}
		rule, err := parseRule(line, source, i+1)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
		if len(rules) > maxPatterns {
			return nil, ErrTooLarge
		}
	}
	return rules, nil
}

func parseRule(pattern, source string, line int) (Rule, error) {
	negate := strings.HasPrefix(pattern, "!")
	if negate {
		pattern = pattern[1:]
	}
	if !validPattern(pattern) {
		return Rule{}, ErrInvalid
	}
	return Rule{ID: fmt.Sprintf("%s:%d", source, line), Pattern: pattern, Reason: source, Negate: negate}, nil
}

func validPattern(pattern string) bool {
	if pattern == "" || strings.ContainsRune(pattern, 0) {
		return false
	}
	for i := 0; i < len(pattern); {
		n, ok := patternToken(pattern[i:])
		if !ok {
			return false
		}
		i += n
	}
	return true
}

func patternToken(pattern string) (int, bool) {
	switch pattern[0] {
	case '\\':
		return 2, len(pattern) > 1
	case '[':
		j, _ := classNeg(pattern)
		if j < len(pattern) && pattern[j] == ']' {
			j++
		}
		_, closed, end := classItems(pattern, j, 0)
		return end, closed
	default:
		return 1, true
	}
}

// Excluded returns the last matching rule for either side of a rename.
// A matching negation re-includes the entire section.
func Excluded(rules []Rule, path, oldPath string) (Rule, bool) {
	var result Rule
	excluded := false
	for _, rule := range rules {
		if Match(rule.Pattern, path) || (oldPath != "" && Match(rule.Pattern, oldPath)) {
			result, excluded = rule, !rule.Negate
		}
	}
	return result, excluded
}

func ignoredLine(line string) bool { return line == "" || strings.HasPrefix(line, "#") }
