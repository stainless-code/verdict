package parity

import "testing"

// TestScrubRootsSlashForm pins the golden scrub to both path spellings: a
// Windows sandbox root carries backslashes while fixtures and product output
// render slash paths, so a slash-spelled root must still become ROOT instead
// of leaking the machine path into Windows goldens.
func TestScrubRootsSlashForm(t *testing.T) {
	t.Parallel()

	root := `C:\tmp\sandbox`
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"native", `C:\tmp\sandbox\remote`, `ROOT\remote`},
		{"slash", `C:/tmp/sandbox/remote`, "ROOT/remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubRoots(tc.in, []string{root}); got != tc.want {
				t.Fatalf("scrubRoots(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
