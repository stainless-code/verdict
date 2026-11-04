package verifycore

import "testing"

// TestIsWordByte pins the word class the sentinel arm shares with gluedSpan:
// a substitution glued to any of these bytes concatenates into a word bash
// reads as a tool name.
func TestIsWordByte(t *testing.T) {
	t.Parallel()
	for _, c := range []byte{'a', 'm', 'z', '0', '5', '9', '_'} {
		if !isWordByte(c) {
			t.Errorf("isWordByte(%q) = false, want true", c)
		}
	}
	for _, c := range []byte{'A', 'Z', '-', '.', '$', '/', ' ', '\t', 0x01} {
		if isWordByte(c) {
			t.Errorf("isWordByte(%q) = true, want false", c)
		}
	}
}

// TestGluedSpan pins the unreduced-span predicate: a surviving `$(`/`${` span
// glued to a word byte on either side is unreducible and hostile; a span free
// of word neighbors, arithmetic, and a trailing `$` are not.
func TestGluedSpan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"left glue letter", "r$( (echo m) )", true},
		{"left glue digit", "1$(x)", true},
		{"left glue underscore", "_$(x)", true},
		{"right glue letter", "$(x)y", true},
		{"right glue underscore", "$(x)_", true},
		{"brace span right glue", "${x}y", true},
		{"brace span left glue", "r${x}", true},
		{"space neighbors", "echo $(x)", false},
		{"no neighbors", "$(x)", false},
		{"quoted literal", "grep -F '$(rm' src/x.go", false},
		{"arithmetic skipped", "x$((1+1))", false},
		{"arithmetic alone", "echo $((1+1))", false},
		{"later span glued", "a$(x) b$(y)z", true},
		{"unclosed with left glue", "r$(foo", true},
		{"unclosed at start", "$(foo", false},
		{"trailing dollar", "a$", false},
		{"dollar alone", "$", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := gluedSpan(c.in); got != c.want {
				t.Fatalf("gluedSpan(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestSpanClose pins depth matching for both span kinds: the closer is the one
// that returns depth to zero, and an unterminated span reports -1.
func TestSpanClose(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		open int
		want int
	}{
		{"nested parens", "$(a(b)c)", 1, 7},
		{"nested braces", "${a{b}c}", 1, 7},
		{"arithmetic shape", "$((1)+2)", 1, 7},
		{"unclosed paren", "$(a", 1, -1},
		{"unclosed brace", "${a", 1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := spanClose(c.in, c.open); got != c.want {
				t.Fatalf("spanClose(%q, %d) = %d, want %d", c.in, c.open, got, c.want)
			}
		})
	}
}
