package config

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// slashSep splits portable globs into path segments.
const slashSep = "/"

// Portable path globs: repository-relative, `/`-separated patterns where
// `*` stays inside one path segment and `**` spans directories. Matching
// is case-sensitive on the Git path spelling. Only `*` is special; every
// other character (including `?` and `[`) matches literally.

// ValidGlob reports whether pattern is a portable relative glob: non-empty,
// relative, with no `..` escape, no empty segment, and no backslash (kept
// portable across separators).
func ValidGlob(pattern string) bool {
	return checkGlob(pattern, "glob") == nil
}

// checkGlob refuses a non-portable glob, naming the path and the rule.
func checkGlob(pattern, path string) *core.RefusalError {
	if pattern == "" {
		return refuse(CodeBadConfig, "%s: want a non-empty glob", path)
	}
	if strings.HasPrefix(pattern, slashSep) {
		return refuse(CodeBadConfig, "%s: glob %q is absolute, want a repository-relative glob", path, pattern)
	}
	if strings.Contains(pattern, "\\") {
		return refuse(CodeBadConfig, "%s: glob %q carries a backslash, want `/`-separated portable globs", path, pattern)
	}
	for seg := range strings.SplitSeq(pattern, slashSep) {
		switch seg {
		case "":
			return refuse(CodeBadConfig, "%s: glob %q has an empty segment", path, pattern)
		case "..":
			return refuse(CodeBadConfig, "%s: glob %q escapes with `..`", path, pattern)
		}
	}
	return nil
}

// Match reports whether the repository-relative path matches the portable
// glob. A `**` segment spans zero or more directories, except in trailing
// position, where it matches everything inside but not the directory
// itself (the gitignore convention: `vendor/**` selects inside vendor).
func Match(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, slashSep), strings.Split(path, slashSep))
}

// matchSegments matches pattern segments against path segments.
func matchSegments(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if pattern[0] == "**" {
		return matchSpan(pattern[1:], segments, len(pattern) == 1)
	}
	if len(segments) == 0 {
		return false
	}
	return matchSegment(pattern[0], segments[0]) && matchSegments(pattern[1:], segments[1:])
}

// matchSpan matches a `**` segment against zero or more path segments,
// except in trailing position, where it consumes at least one.
func matchSpan(rest, segments []string, trailing bool) bool {
	start := 0
	if trailing {
		start = 1
	}
	for i := start; i <= len(segments); i++ {
		if matchSegments(rest, segments[i:]) {
			return true
		}
	}
	return false
}

// matchSegment matches one segment with `*` wildcards (byte-wise; `*`
// itself is ASCII so multibyte text compares exactly).
func matchSegment(pattern, name string) bool {
	px, nx := 0, 0
	star, mark := -1, 0
	for nx < len(name) {
		switch {
		case px < len(pattern) && pattern[px] == name[nx]:
			px++
			nx++
		case px < len(pattern) && pattern[px] == '*':
			star, mark = px, nx
			px++
		case star != -1:
			px = star + 1
			mark++
			nx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}
