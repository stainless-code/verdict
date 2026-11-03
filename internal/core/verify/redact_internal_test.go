package verifycore

import "testing"

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
