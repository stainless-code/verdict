package dashboard

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRuneWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{'é', 1},
		{'─', 1},
		{'…', 1},
		{'日', 2},
		{'한', 2},
		{'Ａ', 2},
		{'😀', 2},
		{'🦀', 2},
		{0x3000, 2},
		{0x0301, 0},
		{0x200D, 0},
		{0xFE0F, 0},
		{0x1b, 0},
		{0x85, 0},
		{0x303F, 1},
		{0xFF61, 1},
		// Unicode 17.0 EastAsianWidth: U+1F7F0 is W; the thermometer, hot
		// pepper, rounded triangle, and desktop computer sit in emoji blocks but are N.
		{0x1F7F0, 2},
		{0x1F94C, 2},
		{0x16FE0, 2},
		{0x1F321, 1},
		{0x1F336, 1},
		{0x1F6C6, 1},
		{0x1F5A5, 1}, // desktop computer: N, inside the old broad emoji interval
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%U) = %d, want %d", c.r, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s     string
		want  string
		limit int
	}{
		{"short", "short", 10},
		{"exactly10!", "exactly10!", 10},
		{"one too long", "one too lo…", 11},
		{"日本語テキスト", "日本…", 6}, // a wide rune never straddles the budget
		{"ab日本", "ab…", 4},    // 日 would need cells 3-4 with the ellipsis on 5
		{"anything", "…", 1},
		{"anything", "", 0},
	}
	for _, c := range cases {
		if got := truncate(c.s, c.limit, '…'); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.limit, got, c.want)
		}
	}
}

// FuzzTruncate holds the two properties every cell write relies on: the
// result never outgrows its budget, and a cut always ends in the ellipsis.
func FuzzTruncate(f *testing.F) {
	for _, seed := range []string{"", "plain", "日本語テキスト", "e\u0301\u200d😀🦀", "\x1b[2J"} {
		f.Add(seed, 5)
	}
	f.Fuzz(func(t *testing.T, s string, limit int) {
		checkTruncate(t, s, limit%200)
	})
}

func checkTruncate(t *testing.T, s string, limit int) {
	t.Helper()
	got := truncate(s, limit, '…')
	switch {
	case limit <= 0 && got != "":
		t.Fatalf("truncate(%q, %d) = %q, want empty", s, limit, got)
	case stringWidth(got) > max(limit, 0):
		t.Fatalf("truncate(%q, %d) = %q is %d cells wide", s, limit, got, stringWidth(got))
	case got != s && limit > 0 && (!utf8.ValidString(got) || !strings.HasSuffix(got, "…")):
		t.Fatalf("truncate(%q, %d) = %q cut without the ellipsis", s, limit, got)
	}
}
