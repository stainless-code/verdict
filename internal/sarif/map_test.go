package sarif

import (
	"encoding/json"
	"testing"
)

func TestTierForResultTable(t *testing.T) {
	t.Parallel()
	cases := []tierCase{
		{name: "error", level: "error", wantTier: "critical"},
		{name: "warning", level: "warning", wantTier: "important"},
		{name: "note", level: "note", wantTier: "informational"},
		{name: "none", level: "none", wantTier: "informational"},
		{name: "empty-defaults-important", level: "", wantTier: "important"},
		{name: "unknown-warns", level: "fatal", wantTier: "important", wantWarn: true},
		{name: "secsev-critical-string", level: "warning", secSev: "9.8", wantTier: "critical"},
		{name: "secsev-high-float", level: "note", secSev: 7.0, wantTier: "critical"},
		{name: "secsev-medium-number", level: "note", secSev: json.Number("5.5"), wantTier: "important"},
		{name: "secsev-low", level: "error", secSev: "2.1", wantTier: "informational"},
		{name: "secsev-zero", level: "error", secSev: 0.0, wantTier: "informational"},
		{name: "secsev-out-of-range-falls-back", level: "error", secSev: 15.0, wantTier: "critical"},
		{name: "secsev-negative-falls-back", level: "note", secSev: -1.0, wantTier: "informational"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertTierCase(t, tc)
		})
	}
}

type tierCase struct {
	secSev   any
	name     string
	level    string
	wantTier string
	wantWarn bool
}

func assertTierCase(t *testing.T, tc tierCase) {
	t.Helper()
	got, warn := TierForResult(tc.level, tc.secSev)
	if got != tc.wantTier {
		t.Errorf("TierForResult(%q, %v) = %q, want %q", tc.level, tc.secSev, got, tc.wantTier)
	}
	if (warn != "") != tc.wantWarn {
		t.Errorf("warn = %q, wantWarn = %v", warn, tc.wantWarn)
	}
}

func TestLevelForTier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sev  any
		want string
	}{
		{"critical", "error"},
		{"important", "warning"},
		{"informational", "note"},
		{nil, "note"},
		{"other", "note"},
	}
	for _, tc := range cases {
		if got := LevelForTier(tc.sev); got != tc.want {
			t.Errorf("LevelForTier(%v) = %q, want %q", tc.sev, got, tc.want)
		}
	}
}

func TestCleanURIValid(t *testing.T) {
	t.Parallel()
	valid := []struct {
		in   string
		want string
	}{
		{"src/main.go", "src/main.go"},
		{"./src/main.go", "src/main.go"},
		{"a/./b/c.go", "a/b/c.go"},
		{"", ""},
	}
	for _, tc := range valid {
		got, err := CleanURI(tc.in)
		if err != nil {
			t.Fatalf("CleanURI(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("CleanURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanURIUnsafe(t *testing.T) {
	t.Parallel()
	unsafe := []string{
		"/etc/passwd",
		"../secret",
		"a/../b",
		"a/%2e%2e/b",
		"%2fetc/passwd",
		"file:///etc/passwd",
		"https://example.com/a.go",
		`C:\repo\a.go`,
		"C:/repo/a.go",
		`a\b.go`,
		"a\x00b.go",
		".",
	}
	for _, u := range unsafe {
		if got, err := CleanURI(u); err == nil {
			t.Errorf("CleanURI(%q) = %q, want refusal", u, got)
		}
	}
}
