package core_test

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// stranger carries every break a value could use to end a line: a newline,
// a CRLF, U+2028, and the U+202E override.
const stranger = "a\nerror: forged\r\nb\xe2\x80\xa8c\xe2\x80\xaed"

type stringer struct{}

func (stringer) String() string { return "s\nerror: forged" }

func TestMsgfFoldsEveryArgumentAndKeepsTheFormat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		format string
		want   string
		args   []any
	}{
		{"string", "path %s is gone", "path a error: forged  b c d is gone", []any{stranger}},
		{"error", "read failed: %v", "read failed: a error: forged  b c d", []any{errors.New(stranger)}},
		{"stringer", "got %v", "got s error: forged", []any{stringer{}}},
		{"slice", "names %v", "names [x y z]", []any{[]string{"x\ny", "z"}}},
		{"quoted", "want %q", "want \"q\\" + "u202er\"", []any{"q\xe2\x80\xaer"}},
		{"width", "[%-6s]", "[a b   ]", []any{"a\nb"}},
		{"indexed", "%[2]s then %[1]s", "two  then one ", []any{"one\n", "two\r"}},
		{"number", "%d of %d", "3 of 4", []any{3, 4}},
		{"tab kept", "%s", "left\tright", []any{"left\tright"}},
		{"format lines kept", "first: %s\nnext: run it", "first: v error: forged\nnext: run it", []any{"v\nerror: forged"}},
		{"trusted", "list:\n%s", "list:\none\ntwo", []any{core.Trusted("one\ntwo")}},
		{"invalid utf-8", "%s", "a\xffb ", []any{"a\xffb\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := core.Msgf(tc.format, tc.args...); got != tc.want {
				t.Errorf("Msgf(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

func TestFoldLineFoldsBreaksAndBidi(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a\nb", "a b"},
		{"a\r\nb", "a  b"},
		{"a\x1b[31mb", "a [31mb"},
		{"a\xe2\x80\xa8b\xe2\x80\xa9c", "a b c"},
		{"a\xe2\x80\xaeb\xe2\x81\xa6c\xe2\x80\x8fd\xd8\x9ce", "a b c d e"},
		{"a\u0085b", "a b"},
		{"tab\tstays", "tab\tstays"},
		{"bad\xff\xfebytes", "bad\xff\xfebytes"},
	} {
		if got := core.FoldLine(tc.in); got != tc.want {
			t.Errorf("FoldLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An attribution template's source names the settings file that carried it.
func TestPlaceholderSourceCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, core.ValidateAttributionPlaceholders("{bad}", forgedline.Value),
		forgedline.Named+" carries unknown placeholder")
}
