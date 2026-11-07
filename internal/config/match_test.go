package config

import (
	"testing"
)

// TestMatch pins the portable glob semantics: `*` stays inside one
// segment, `**` spans directories, and matching is case-sensitive on the
// Git path spelling.
func TestMatch(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*", "a.go", true},
		{"*", "a/b.go", false},
		{"**", "a/b.go", true},
		{"**", "a.go", true},
		{"payments/**", "payments/api.go", true},
		{"payments/**", "payments", false},
		{"payments/**", "other/x.go", false},
		{"**", "anything/at/all.go", true},
		{"*.go", "a.go", true},
		{"*.go", "a/b.go", false},
		{"db/migrations/**", "db/migrations/0007.sql", true},
		{"vendor/**", "vendor/keep/x.go", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y/c", false},
		{"A/**", "a/x.go", false},
		{"payments/*.go", "payments/api.go", true},
		{"payments/*.go", "payments/deep/api.go", false},
	}
	for _, tc := range tests {
		if got := Match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// TestValidGlob pins portability: relative only, no `..` escape, no
// empty segments, no backslashes.
func TestValidGlob(t *testing.T) {
	valid := []string{"**", "a/**", "*.go", "db/migrations/**", "vendor/keep/**", "a/**/b"}
	for _, pattern := range valid {
		if !ValidGlob(pattern) {
			t.Errorf("ValidGlob(%q) = false, want true", pattern)
		}
	}
	invalid := []string{"", "/abs/path", "../evil", "a/../b", "a//b", `a\b`, "/"}
	for _, pattern := range invalid {
		if ValidGlob(pattern) {
			t.Errorf("ValidGlob(%q) = true, want false", pattern)
		}
	}
}
