package fixcore

import "testing"

// TestPromotedCommand pins the receipt rewrite to whole path tokens: the bare
// and `./`-spelled bundle reference both move to the promoted path, while a
// sibling file that merely shares the base name keeps its own spelling.
func TestPromotedCommand(t *testing.T) {
	const (
		art  = "tests/6123456789abcdef0123456789abcdef01234567-161139e764cc.py"
		dest = ".verdict/tests/6123456789abcdef0123456789abcdef01234567-161139e764cc.py"
	)
	for _, tc := range []struct{ name, cmd, want string }{
		{"bare", "python3 " + art, "python3 " + dest},
		{"dot slash", "sh ./" + art, "sh " + dest},
		{"pytest selector", "pytest " + art + "::test_case", "pytest " + dest + "::test_case"},
		{"quoted", `sh -c "python3 ` + art + `"`, `sh -c "python3 ` + dest + `"`},
		{"shell separator", "python3 " + art + " && true", "python3 " + dest + " && true"},
		{"sibling extension", "python3 " + art + ".expected", "python3 " + art + ".expected"},
		{"suffixed variant", "python3 " + art + "_2", "python3 " + art + "_2"},
		{"nested path", "python3 " + art + "/inner", "python3 " + art + "/inner"},
		{"already promoted", "python3 " + dest, "python3 " + dest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promotedCommand(tc.cmd, art, dest); got != tc.want {
				t.Errorf("promotedCommand(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}
