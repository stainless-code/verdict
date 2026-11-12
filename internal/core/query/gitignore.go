package query

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
)

const (
	maxGitignoreBytes           = 1 << 20
	maxIgnoreDoubleStarSegments = 2
	gitPathSeparator            = "/"
	gitDoubleStar               = "**"
)

type ignoreRule struct {
	base, pattern   string
	negate, dirOnly bool
}

func readIgnore(root *os.Root, rootPath, file, base string, warnings *[]string, skipped *int) []ignoreRule {
	f, err := root.Open(file)
	if err != nil {
		return nil
	}
	raw, readErr := cappedIgnore(f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return nil
	}
	var rules []ignoreRule
	for i, source := range strings.Split(string(raw), "\n") {
		rule, ok := parseIgnoreLine(source, i+1, file, rootPath, base, warnings, skipped)
		if ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func parseIgnoreLine(source string, line int, file, rootPath, base string, warnings *[]string, skipped *int) (ignoreRule, bool) {
	lineText := strings.TrimSuffix(source, "\r")
	if lineText == "" || strings.HasPrefix(lineText, "#") {
		return ignoreRule{}, false
	}
	original := lineText
	negate := strings.HasPrefix(lineText, "!")
	if negate {
		lineText = lineText[1:]
	}
	lineText = unescapeCommentOrNegation(lineText)
	anchored := strings.HasPrefix(lineText, gitPathSeparator)
	lineText = strings.TrimPrefix(lineText, gitPathSeparator)
	dirOnly := strings.HasSuffix(lineText, gitPathSeparator)
	lineText = strings.TrimSuffix(lineText, gitPathSeparator)
	if !validIgnorePattern(lineText) {
		warnSkippedPattern(file, rootPath, line, original, warnings, skipped)
		return ignoreRule{}, false
	}
	if !anchored && !strings.Contains(lineText, gitPathSeparator) {
		lineText = gitDoubleStar + gitPathSeparator + lineText
	}
	if ignoreDoubleStarSegments(lineText, dirOnly) > maxIgnoreDoubleStarSegments {
		warnSkippedPattern(file, rootPath, line, original, warnings, skipped)
		return ignoreRule{}, false
	}
	return ignoreRule{base: base, pattern: lineText, negate: negate, dirOnly: dirOnly}, true
}

func unescapeCommentOrNegation(line string) string {
	if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		return line[1:]
	}
	return line
}

func warnSkippedPattern(file, rootPath string, line int, pattern string, warnings *[]string, skipped *int) {
	*skipped++
	if *skipped <= maxIgnoreWarnings {
		*warnings = append(*warnings, fmt.Sprintf("%s:%d: skipped gitignore pattern %q",
			displayPath(rootPath, file), line, pattern))
	}
}

func cappedIgnore(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxGitignoreBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read gitignore: %w", err) //nolint:wrapcheck // keeps the underlying reader cause.
	}
	if len(b) > maxGitignoreBytes {
		return nil, errors.New("gitignore exceeds size cap") //nolint:wrapcheck // caller treats capped files as unsupported patterns.
	}
	return b, nil
}

func validIgnorePattern(pattern string) bool {
	if !config.ValidGlob(pattern) || strings.ContainsAny(pattern, "?[]\\") {
		return false
	}
	for segment := range strings.SplitSeq(pattern, gitPathSeparator) {
		if segment == "" || (strings.Contains(segment, gitDoubleStar) && segment != gitDoubleStar) {
			return false
		}
	}
	return true
}

func ignoreDoubleStarSegments(pattern string, dirOnly bool) int {
	count := 0
	for segment := range strings.SplitSeq(pattern, gitPathSeparator) {
		if segment == gitDoubleStar {
			count++
		}
	}
	if dirOnly {
		count++
	}
	return count
}

func ignored(rules []ignoreRule, rel string) bool {
	excluded := false
	for _, rule := range rules {
		if matchesIgnoreRule(rule, rel) {
			excluded = !rule.negate
		}
	}
	return excluded
}

func matchesIgnoreRule(rule ignoreRule, rel string) bool {
	sub := rel
	if rule.base != "." {
		prefix := rule.base + gitPathSeparator
		if !strings.HasPrefix(rel, prefix) {
			return false
		}
		sub = strings.TrimPrefix(rel, prefix)
	}
	if config.Match(rule.pattern, sub) {
		return true
	}
	return rule.dirOnly && config.Match(path.Join(rule.pattern, gitDoubleStar), sub)
}
