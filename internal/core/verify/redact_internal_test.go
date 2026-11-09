package verifycore

import (
	"strings"
	"testing"
)

// TestRedactSecretsHidesOverlappingValues proves two secret-named environment
// values that overlap are hidden in one pass. The redactor used to chain a
// `strings.ReplaceAll` per secret, so replacing the shorter value consumed the
// bytes the longer one needed to match and left its tail — here five bytes of
// URL_TOKEN's `valueEXTRA` — in the recorded tail, which lands in patch.json.
// The matching now comes from envsecret.ScrubValues, shared with the launcher's
// tail scrub, so the overlap cannot be fixed in one copy and missed in the
// other.
func TestRedactSecretsHidesOverlappingValues(t *testing.T) {
	t.Parallel()
	environ := []string{
		"PATH=/bin",
		"API_KEY=secretvalue",
		"URL_TOKEN=valueEXTRA",
	}
	got := redactSecrets("token=secretvalueEXTRA exit=0", environ)
	if got != "token="+redacted+" exit=0" {
		t.Fatalf("redactSecrets = %q, want only the credential hidden", got)
	}
	for _, leaked := range []string{"secret", "value", "EXTRA"} {
		if strings.Contains(got, leaked) {
			t.Errorf("redactSecrets leaked %q: %q", leaked, got)
		}
	}
}

// TestRedactSecretsKeepsItsOwnPolicy proves the redactor kept the two rules
// that are policy rather than matching now that the algorithm is shared: only
// environment variables are collected (an argument secret is the launcher's
// business) and a value below the 8-character floor is left alone.
func TestRedactSecretsKeepsItsOwnPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		want    string
		environ []string
	}{
		{
			name:    "eight-byte-value-redacted",
			environ: []string{"EIGHT_TOKEN=abcd1234"},
			want:    "a=" + redacted + " b=abcd123",
		},
		{
			name:    "seven-byte-value-kept",
			environ: []string{"SEVEN_TOKEN=abcd123"},
			want:    "a=abcd1234 b=abcd123",
		},
		{
			name:    "non-secret-name-kept",
			environ: []string{"PATH=/usr/bin/gh"},
			want:    "a=abcd1234 b=abcd123",
		},
	}
	const in = "a=abcd1234 b=abcd123"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := redactSecrets(in, tc.environ); got != tc.want {
				t.Errorf("redactSecrets(%q) = %q, want %q", in, got, tc.want)
			}
		})
	}
}

// TestRedactShapes pins shape-based redaction: the recorded tail drops a
// leaked credential even when no environment value names it.
func TestRedactShapes(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{
			name: "bearer header", in: "Authorization: Bearer sk-ant-api03-abcdefghijklmnop\n",
			want: "Authorization: ***REDACTED***\n",
		},
		{
			name: "lowercase token header", in: "authorization:token ghp_abcdefghijklmnopqrst\n",
			want: "authorization:***REDACTED***\n",
		},
		{
			name: "proxy authorization", in: "> Proxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\n",
			want: "> Proxy-Authorization: ***REDACTED***\n",
		},
		{
			name: "url userinfo", in: "clone https://alice:s3cretpw@example.invalid/r.git\n",
			want: "clone https://***REDACTED***@example.invalid/r.git\n",
		},
		{
			name: "url user only", in: "ssh://git@github.com/acme/app\n",
			want: "ssh://***REDACTED***@github.com/acme/app\n",
		},
		{
			name: "bare bearer", in: "sent Bearer abcdefgh12345678 to host\n",
			want: "sent Bearer ***REDACTED*** to host\n",
		},
		{name: "github token", in: "token=ghp_0123456789abcdefghij\n", want: "token=***REDACTED***\n"},
		{name: "fine-grained pat", in: "x github_pat_11ABCDEFG0abcdefghij_klmnopqrst\n", want: "x ***REDACTED***\n"},
		{name: "gitlab token", in: "glpat-abcdefghij1234567890\n", want: "***REDACTED***\n"},
		{name: "openai key", in: "sk-proj-abcdefghijklmnop1234\n", want: "***REDACTED***\n"},
		{name: "slack token", in: "xoxb-1234567890-abcdefghij\n", want: "***REDACTED***\n"},
		{name: "aws key id", in: "AKIAIOSFODNN7EXAMPLE\n", want: "***REDACTED***\n"},
		{name: "google api key", in: "AIzaSyA1234567890abcdefghijklmnopqrstuv\n", want: "***REDACTED***\n"},
		{name: "npm token", in: "npm_0123456789abcdefghijklmnopqrstuvwxyz\n", want: "***REDACTED***\n"},
		{name: "docker pat", in: "dckr_pat_abcdefghijklmnopqrstuv\n", want: "***REDACTED***\n"},
		{name: "plain text untouched", in: "build ok: 3 files\n", want: "build ok: 3 files\n"},
		{name: "short token untouched", in: "sk-short\n", want: "sk-short\n"},
		{name: "short aws untouched", in: "AKIA1234\n", want: "AKIA1234\n"},
		{name: "plain url untouched", in: "https://example.invalid/a/b?c=d#e\n", want: "https://example.invalid/a/b?c=d#e\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactShapes(tc.in); got != tc.want {
				t.Fatalf("redactShapes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripControlChars pins the ANSI/control-character strip that keeps a
// hostile command from rewriting the tail it lands in.
func TestStripControlChars(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{name: "color", in: "\x1b[31mred\x1b[0m\n", want: "red\n"},
		{name: "osc title", in: "\x1b]0;title\x07text", want: "text"},
		{name: "osc hyperlink", in: "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", want: "link"},
		{name: "carriage return", in: "50%\r100%\n", want: "50%100%\n"},
		{name: "nul and backspace", in: "a\x00b\x08c\x7f", want: "abc"},
		{name: "two byte escape", in: "a\x1bMb", want: "ab"},
		{name: "unterminated csi", in: "a\x1b[38;5;", want: "a"},
		{name: "newline and tab kept", in: "a\tb\nc", want: "a\tb\nc"},
		{name: "unicode kept", in: "exit=0 — ok\n", want: "exit=0 — ok\n"},
		{name: "plain untouched", in: "line one\nline two\n", want: "line one\nline two\n"},
		{name: "bel only", in: "\x07", want: ""},
		{name: "nul only", in: "\x00\x01\x1f", want: ""},
		{name: "del only", in: "\x7f", want: ""},
		{name: "csi final at sign", in: "\x1b[1@x", want: "x"},
		{name: "csi final tilde", in: "\x1b[3~ok", want: "ok"},
		{name: "unterminated osc", in: "a\x1b]0;never closes", want: "a"},
		{name: "lone escape", in: "a\x1b", want: "a"},
		{name: "osc st then text", in: "\x1b]0;t\x1b\\after", want: "after"},
		{name: "osc bel then text", in: "\x1b]0;t\x07after", want: "after"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripControlChars(tc.in); got != tc.want {
				t.Fatalf("stripControlChars(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeTail pins the composition order: environment values, then
// shapes, then control characters.
func TestSanitizeTail(t *testing.T) {
	environ := []string{"PATH=/bin", "EVAL_PROBE_TOKEN=supersecretvalue123"}
	in := "\x1b[1mlogin\x1b[0m supersecretvalue123 Authorization: Bearer abcdefgh12345678\r\n"
	want := "login ***REDACTED*** Authorization: ***REDACTED***\n"
	if got := sanitizeTail(in, environ); got != want {
		t.Fatalf("sanitizeTail = %q, want %q", got, want)
	}
}

// TestSkipOSCReturnsClosingByte pins the helper contract that
// stripControlChars relies on: the returned index is the byte that closes the
// OSC string (the backslash of ST, the BEL itself), or the last byte when it
// never closes.
//
// Mutant killed: redact.go:158 ARITHMETIC_BASE (`return j + 1` -> `j - 1`).
// Through stripControlChars alone that mutant is equivalent (the leftover
// ESC \ is re-consumed as a two-byte escape), so only the helper contract
// observes it.
func TestSkipOSCReturnsClosingByte(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "st", in: "\x1b]0;t\x1b\\x", want: 6},
		{name: "bel", in: "\x1b]0;t\x07x", want: 5},
		{name: "never closes", in: "\x1b]0;tt", want: 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipOSC(tc.in, 2); got != tc.want {
				t.Fatalf("skipOSC(%q, 2) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestFlatOfSqueezesWhitespace pins the documented flatOf contract: newlines,
// tabs and carriage returns fold to a space, runs squeeze to one, the ends
// are trimmed.
//
// Mutants killed: normalize.go:58 CONDITIONALS_NEGATION (`sb.Len() > 0` ->
// `<= 0`) and normalize.go:69 CONDITIONALS_NEGATION (lastByte reports "empty"
// for every non-empty builder): both stop the squeeze, so "a  b" keeps two
// spaces. Gate-level effect is masked by the re-squeeze in bareOf/adjOf, which
// is why only a direct test sees it.
func TestFlatOfSqueezesWhitespace(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{name: "double space", in: "a  b", want: "a b"},
		{name: "mixed whitespace run", in: "a\t\n \r b", want: "a b"},
		{name: "ends trimmed", in: "  \n lead and trail \t", want: "lead and trail"},
		{name: "single spaces kept", in: "a b c", want: "a b c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flatOf(tc.in); got != tc.want {
				t.Fatalf("flatOf(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAdjOfReportsDeepNesting pins the bound for both span kinds: nesting up
// to maxSentinelPasses fully collapses to sentinels; one level deeper is
// reported so the gate can fail closed.
//
// Break killed: replaceSpans returning false unconditionally (manual) — the
// brace row is not reachable through Check, because the glob arm refuses
// `${` in command position first, so this is the only test of the brace loop.
func TestAdjOfReportsDeepNesting(t *testing.T) {
	nest := func(open, cl string, depth int) string {
		return strings.Repeat(open, depth) + "x" + strings.Repeat(cl, depth)
	}
	tests := []struct {
		name     string
		in       string
		wantDeep bool
	}{
		{name: "paren at bound", in: nest("$(", ")", 8), wantDeep: false},
		{name: "paren past bound", in: nest("$(", ")", 9), wantDeep: true},
		{name: "brace at bound", in: nest("${", "}", 8), wantDeep: false},
		{name: "brace past bound", in: nest("${", "}", 9), wantDeep: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, deep := adjOf(tc.in); deep != tc.wantDeep {
				t.Fatalf("adjOf deep = %v, want %v", deep, tc.wantDeep)
			}
		})
	}
}
