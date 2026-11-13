package exclude

import (
	"errors"
	"strings"
	"testing"
)

func TestOrderedIgnore(t *testing.T) {
	rules, err := ParseIgnore([]byte("# receipt\nvendor/**\n!vendor/keep/**\nvendor/keep/private/**\n\\#literal\n"), "ignore")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path, old, id string
		excluded      bool
	}{
		{"vendor/a.go", "", "ignore:2", true},
		{"vendor/keep/a.go", "", "ignore:3", false},
		{"vendor/keep/private/a.go", "", "ignore:4", true},
		{"new/a.go", "vendor/a.go", "ignore:2", true},
		{"vendor/keep/a.go", "vendor/a.go", "ignore:3", false},
		{"#literal", "", "ignore:5", true},
		{"src/a_test.go", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.path+tt.old, func(t *testing.T) {
			got, excluded := Excluded(rules, tt.path, tt.old)
			if got.ID != tt.id || excluded != tt.excluded {
				t.Fatalf("got %q/%v; want %q/%v", got.ID, excluded, tt.id, tt.excluded)
			}
		})
	}
}

func TestIgnoreLimitsAndSyntax(t *testing.T) {
	tests := []struct {
		want      error
		name, raw string
	}{
		{ErrInvalid, "bare negation", "!"},
		{ErrInvalid, "class", "a["},
		{ErrInvalid, "escape", "a\\"},
		{ErrInvalid, "utf8", "\xff"},
		{ErrInvalid, "nul", "a\x00b"},
		{ErrTooLarge, "bytes", strings.Repeat("#", 65537)},
		{ErrTooLarge, "patterns", strings.Repeat("a\n", 1001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseIgnore([]byte(tt.raw), "ignore"); !errors.Is(err, tt.want) {
				t.Fatalf("got %v; want %v", err, tt.want)
			}
		})
	}
}

func TestIgnoreBoundary(t *testing.T) {
	for _, raw := range []string{strings.Repeat("a\n", 1000), "#" + strings.Repeat("x", 65535), "[!a-c]?\\*\r\n"} {
		if _, err := ParseIgnore([]byte(raw), "ignore"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShellGlobDialect(t *testing.T) {
	rules, err := ParseIgnore([]byte("src/*[0-9]?.go"), "cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Excluded(rules, "src/deep/file2x.go", ""); !ok {
		t.Fatal("shell glob must cross slash and match class and question mark")
	}
	if _, ok := Excluded(rules, "src/deep/fileax.go", ""); ok {
		t.Fatal("class accepted a letter")
	}
}
