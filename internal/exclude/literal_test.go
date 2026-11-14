package exclude

import "testing"

// TestLiteralPathMatchesExactlyThatPath proves the escaped pattern the
// over-cap hint prints survives Match: it hits the path it names and no
// lookalike the unescaped pattern would also hit.
func TestLiteralPathMatchesExactlyThatPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, path, pattern, lookalike string
	}{
		{name: "class", path: "app/[id]/page.tsx", pattern: `app/\[id\]/page.tsx`, lookalike: "app/i/page.tsx"},
		{name: "star", path: "docs/*.md", pattern: `docs/\*.md`, lookalike: "docs/readme.md"},
		{name: "question", path: "a?b.txt", pattern: `a\?b.txt`, lookalike: "axb.txt"},
		{name: "backslash", path: `a\b.txt`, pattern: `a\\b.txt`, lookalike: "a/b.txt"},
		{name: "space", path: "my dir/a.go", pattern: "my dir/a.go", lookalike: "myxdir/a.go"},
		{name: "quote", path: "it's/a.go", pattern: "it's/a.go", lookalike: "itxa.go"},
		{name: "leading bang", path: "!notes.md", pattern: `\!notes.md`, lookalike: "notes.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertLiteralPath(t, tc.path, tc.pattern, tc.lookalike)
		})
	}
}

func assertLiteralPath(t *testing.T, path, pattern, lookalike string) {
	t.Helper()
	if got := LiteralPath(path); got != pattern {
		t.Fatalf("LiteralPath(%q) = %q, want %q", path, got, pattern)
	}
	if !Match(pattern, path) {
		t.Errorf("Match(%q, %q) = false; want the escaped pattern to hit its own path", pattern, path)
	}
	if Match(pattern, lookalike) {
		t.Errorf("Match(%q, %q) = true; want the escaped pattern to miss the lookalike", pattern, lookalike)
	}
}

// TestLiteralPathSurvivesParseIgnore proves a leading ! stays an exclusion
// rule rather than turning the pattern into a re-include.
func TestLiteralPathSurvivesParseIgnore(t *testing.T) {
	t.Parallel()
	rules, err := ParseIgnore([]byte(LiteralPath("!notes.md")+"\n"), "ignore")
	if err != nil {
		t.Fatalf("ParseIgnore refused the escaped leading bang: %v", err)
	}
	if len(rules) != 1 || rules[0].Negate {
		t.Fatalf("rules = %+v; want one non-negated rule", rules)
	}
	if _, excluded := Excluded(rules, "!notes.md", ""); !excluded {
		t.Errorf("Excluded(%q) = false; want the escaped rule to exclude it", "!notes.md")
	}
}
