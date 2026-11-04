package verifycore

import (
	"strings"
	"testing"
)

// TestRedactShapesASCIIMatchesRegexp pins the scanner against the regexp
// implementation across every redaction class and the boundary cases each
// class turns on.
func TestRedactShapesASCIIMatchesRegexp(t *testing.T) {
	tests := []struct{ name, in string }{
		{name: "url user and password", in: "clone https://alice:s3cretpw@example.invalid/r.git\n"},
		{name: "url user only", in: "ssh://git@github.com/acme/app\n"},
		{name: "url scheme punctuation", in: "git+ssh://user:pw@host/repo\n"},
		{name: "url no userinfo", in: "https://example.invalid/a@b\n"},
		{name: "url password slash", in: "https://u:p/w@host\n"},
		{name: "url boundary after word", in: "xhttps://u:p@host\n"},
		{name: "url boundary after underscore", in: "_https://u:p@host\n"},
		{name: "url after plus", in: "a+b://u:p@host\n"},
		{name: "url empty userinfo", in: "https://@host\n"},
		{name: "url password only", in: "https://:pw@host\n"},
		{name: "url bare separator", in: "see :// no scheme\n"},
		{name: "url twice", in: "https://u:p@a https://v:q@b\n"},
		{name: "auth bearer", in: "Authorization: Bearer sk-ant-api03-abcdefghijklmnop\n"},
		{name: "auth proxy basic", in: "> Proxy-Authorization: Basic dXNlcjpwYXNzd29yZA==\n"},
		{name: "auth mixed case", in: "aUtHoRiZaTiOn:token ghp_abcdefghijklmnopqrst\n"},
		{name: "auth no space", in: "authorization:x\n"},
		{name: "auth space before colon", in: "authorization \t : x\n"},
		{name: "auth trailing space", in: "authorization: "},
		{name: "auth trailing tab", in: "authorization:\t"},
		{name: "auth newline in prefix", in: "authorization\n: x\n"},
		{name: "auth only newlines", in: "authorization: \r\n"},
		{name: "auth inside word", in: "xauthorization: y\n"},
		{name: "auth empty value", in: "authorization:\n"},
		{name: "auth second colon", in: "authorization:" + ": x\n"},
		{name: "auth twice", in: "authorization: " + "authorization: x\n"},
		{name: "bearer bare", in: "sent Bearer abcdefgh12345678 to host\n"},
		{name: "bearer short", in: "Bearer abcdefg\n"},
		{name: "bearer exactly eight", in: "Bearer abcdefgh\n"},
		{name: "bearer mixed case", in: "bEaReR abcdefgh\n"},
		{name: "bearer inside word", in: "xBearer abcdefgh\n"},
		{name: "bearer multiline", in: "bearer\n\nabcdefgh\n"},
		{name: "bearer token punctuation", in: "Bearer a.b_c~d+e/f=g-h\n"},
		{name: "bearer twice", in: "bearer abcdefgh bearer ijklmnop\n"},
		{name: "token github", in: "token=ghp_0123456789abcdefghij\n"},
		{name: "token github pat", in: "x github_pat_11ABCDEFG0abcdefghij_klmnopqrst\n"},
		{name: "token gitlab", in: "glpat-abcdefghij1234567890\n"},
		{name: "token openai", in: "sk-proj-abcdefghijklmnop1234\n"},
		{name: "token anthropic", in: "sk-ant-api03-abcdefghijklmnop\n"},
		{name: "token pk", in: "pk-abcdefghijklmnop\n"},
		{name: "token slack", in: "xoxb-1234567890-abcdefghij\n"},
		{name: "token slack user", in: "xoxp-1234567890-abcdefghij\n"},
		{name: "token aws", in: "AKIAIOSFODNN7EXAMPLE\n"},
		{name: "token aws temp", in: "ASIAIOSFODNN7EXAMPLE\n"},
		{name: "token aws extra", in: "AKIAIOSFODNN7EXAMPLEXXX\n"},
		{name: "token google", in: "AIzaSyA1234567890abcdefghijklmnopqrstuv\n"},
		{name: "token npm", in: "npm_0123456789abcdefghijklmnopqrstuvwxyz\n"},
		{name: "token docker", in: "dckr_pat_abcdefghijklmnopqrstuv\n"},
		{name: "token short", in: "sk-short\n"},
		{name: "token short aws", in: "AKIA1234\n"},
		{name: "token after underscore", in: "_ghp_0123456789abcdefghij\n"},
		{name: "token inside word", in: "xghp_0123456789abcdefghij\n"},
		{name: "token overlapping families", in: "sk-xxxxglpat-abcdefghijklmnop\n"},
		{name: "token back to back", in: "ghp_0123456789abcdefghij npm_0123456789abcdefghijklmnopqrstuvwxyz\n"},
		{name: "token inside url", in: "https://ghp_0123456789abcdefghij@host\n"},
		{name: "token inside auth", in: "Authorization: ghp_0123456789abcdefghij\n"},
		{name: "token inside bearer", in: "Bearer ghp_0123456789abcdefghij\n"},
		{name: "url inside auth", in: "Authorization: Bearer https://u:p@host\n"},
		{name: "ansi and nul", in: "\x1b[31mwarn\x1b[0m\x00 build failed\n"},
		{name: "plain", in: "build ok: 3 files\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertShapesMatch(t, tc.in)
		})
	}
}

func assertShapesMatch(t *testing.T, in string) {
	t.Helper()
	want := redactShapesRegexp(in)
	if got := redactShapesASCII(in); got != want {
		t.Fatalf("redactShapesASCII(%q) = %q, want %q", in, got, want)
	}
	if got := redactShapes(in); got != want {
		t.Fatalf("redactShapes(%q) = %q, want %q", in, got, want)
	}
}

// TestRedactShapesASCIIRandomized builds hostile inputs from redaction
// fragments so every class meets every other class, its boundaries, and the
// control bytes the tail can carry.
func TestRedactShapesASCIIRandomized(t *testing.T) {
	fragments := []string{
		"https://", "http://", "ssh://", "git+ssh://", "a://", "://", "user:pw@",
		"user@", ":pw@", "@", "authorization", "Authorization", "AUTHORIZATION",
		"proxy-", "Proxy-", ": ", ":", " ", "bearer", "Bearer", "BEARER", " \t",
		"\n", "\r\n", "ghp_", "github_pat_", "glpat-", "sk-", "sk-ant-", "pk-",
		"rk-", "xoxb-", "AKIA", "ASIA", "AIza", "npm_", "dckr_pat_", "abcdefgh",
		strings.Repeat("A", 16), strings.Repeat("a", 20),
		"0123456789abcdefghijklmnopqrstuvwxyz", "/", "?", "#", "\x00",
		"\x1b[31m", "\x1b", "\t", "\r", ".", "-", "+", "_", "=", "~", "\\",
	}
	state := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return state
	}
	for i := range 20000 {
		var b strings.Builder
		for n := int(next()%12) + 1; n > 0; n-- {
			b.WriteString(fragments[next()%uint64(len(fragments))])
		}
		in := b.String()
		if got, want := redactShapesASCII(in), redactShapesRegexp(in); got != want {
			t.Fatalf("case %d: redactShapesASCII(%q) = %q, want %q", i, in, got, want)
		}
	}
}

// TestRedactShapesNonASCIIFallsBack pins the reason the scanner is ASCII-only:
// the regexp path folds the URL scheme's a to LONG S and KELVIN SIGN.
func TestRedactShapesNonASCIIFallsBack(t *testing.T) {
	for _, in := range []string{"a\u212a://user:pw@host\n", "a\u017f://user:pw@host\n"} {
		want := redactShapesRegexp(in)
		if !strings.Contains(want, redacted) {
			t.Fatalf("regexp path does not redact %q", in)
		}
		if got := redactShapes(in); got != want {
			t.Fatalf("redactShapes(%q) = %q, want %q", in, got, want)
		}
	}
}
