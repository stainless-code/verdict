package archtest

import "testing"

// TestForbiddenDep pins the deny predicate's matching forms, so a boundary row
// cannot silently stop matching when an import path moves: the exact path, the
// historical suffix form, and the module-prefix subpackage form all deny.
func TestForbiddenDep(t *testing.T) {
	t.Parallel()
	deny := []string{"internal/readcommon"}
	tests := []struct {
		name string
		dep  string
		want bool
	}{
		{name: "module path", dep: modulePath + "internal/readcommon", want: true},
		{name: "bare path", dep: "internal/readcommon", want: true},
		{name: "subpackage", dep: modulePath + "internal/readcommon/sub", want: true},
		{name: "sibling prefix", dep: modulePath + "internal/readcommonly", want: false},
		{name: "unrelated", dep: modulePath + "internal/jq", want: false},
		{name: "standard library", dep: "os", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := forbiddenDep(tc.dep, deny); got != tc.want {
				t.Errorf("forbiddenDep(%q) = %v; want %v", tc.dep, got, tc.want)
			}
		})
	}
}

// TestMatchPattern pins the row granularity: "./internal/core" is the root
// package only, while "./internal/core/..." is the subtree.
func TestMatchPattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		rel     string
		want    bool
	}{
		{name: "root matches", pattern: "./internal/core", rel: "internal/core", want: true},
		{name: "root skips subtree", pattern: "./internal/core", rel: "internal/core/report"},
		{name: "subtree matches root", pattern: "./internal/core/...", rel: "internal/core", want: true},
		{name: "subtree matches child", pattern: "./internal/core/...", rel: "internal/core/report", want: true},
		{name: "subtree skips sibling", pattern: "./internal/core/...", rel: "internal/corex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchPattern(tc.pattern, tc.rel); got != tc.want {
				t.Errorf("matchPattern(%q, %q) = %v; want %v", tc.pattern, tc.rel, got, tc.want)
			}
		})
	}
}
