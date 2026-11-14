package shellquote

import "testing"

// TestArg pins the quoting rule: a plain path stays bare so today's hints read
// the same, and every other rune gets POSIX single quotes.
func TestArg(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain path", "/usr/local/bin/verdict", "/usr/local/bin/verdict"},
		{"package name", "@stainless-code/verdict", "@stainless-code/verdict"},
		{"empty", "", ""},
		{"space", "/tmp/my dir/verdict", "'/tmp/my dir/verdict'"},
		{"single quote", "/tmp/it's/verdict", `'/tmp/it'\''s/verdict'`},
		{"dollar", "/tmp/$HOME/verdict", "'/tmp/$HOME/verdict'"},
		{"glob class", "app/[id]/page.tsx", "'app/[id]/page.tsx'"},
		{"glob escape", `app/\[id\]/page.tsx`, `'app/\[id\]/page.tsx'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Arg(tc.in); got != tc.want {
				t.Errorf("Arg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
