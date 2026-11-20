package untrusted

import (
	"strings"
	"testing"
)

type sanitizeCase struct {
	name string
	in   string
	want string
}

func checkIdempotent(t *testing.T, fnName string, fn func(string) string, tc sanitizeCase) {
	t.Helper()
	got := fn(tc.in)
	if got != tc.want {
		t.Errorf("%s(%q) = %q, want %q", fnName, tc.in, got, tc.want)
	}
	if again := fn(got); again != got {
		t.Errorf("%s not idempotent: %q -> %q", fnName, got, again)
	}
}

func TestInlineProse(t *testing.T) {
	t.Parallel()
	cases := []sanitizeCase{
		{
			name: "plain-and-code-span",
			in:   "Check `x < y` and `@alice` before notifying @bob.",
			want: "Check `x < y` and `@alice` before notifying @" + zwsp + "bob.",
		},
		{
			name: "html-tags-and-comments",
			in:   "<script>alert(1)</script> <!-- verdict-footer --> <details></details>",
			want: "&lt;script>alert(1)&lt;/script> &lt;!-- verdict-footer --> &lt;details>&lt;/details>",
		},
		{
			name: "mention-inside-html-comment",
			in:   "<!-- @alice -->",
			want: "&lt;!-- @" + zwsp + "alice -->",
		},
		{
			name: "fenced-code-and-tilde",
			in:   "```go\n<script>@alice</script>\n```\n~~~sh\nrm -rf /\n~~~",
			want: "``" + zwsp + "`go &lt;script>@" + zwsp + "alice&lt;/script> ``" + zwsp + "` ~~" + zwsp + "~sh rm -rf / ~~" + zwsp + "~",
		},
		{
			name: "markdown-links-images-and-refs",
			in:   "[click](https://evil.test) ![px](https://evil.test/1.png) [spoof][ev-1] [ev-1]: https://evil.test",
			want: "[click]" + zwsp + "(https://evil.test) !" + zwsp + "[px]" + zwsp + "(https://evil.test/1.png) [spoof]" + zwsp + "[ev-1] [ev-1]" + zwsp + ": https://evil.test",
		},
		{
			name: "bidi-and-line-breaks",
			in:   "safe\u202eevil\u2066iso\u200fmark\u061calm\r\nnext\u2028line\t\x1b[31mred",
			want: "safe evil iso mark alm next line [31mred",
		},
		{
			name: "unclosed-backtick-still-escapes-html",
			in:   "dangling ` <img src=x onerror=1>",
			want: "dangling ` &lt;img src=x onerror=1>",
		},
		{
			name: "fence-marker-in-code-span",
			in:   "marker `<<<END` stays broken",
			want: "marker `<<" + zwsp + "<END` stays broken",
		},
		{
			name: "email-preserved",
			in:   "Contact sec@example.com or @alice.",
			want: "Contact sec@example.com or @" + zwsp + "alice.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkIdempotent(t, "InlineProse", InlineProse, tc)
		})
	}
}

func TestParagraphBlockOpeners(t *testing.T) {
	t.Parallel()
	cases := []sanitizeCase{
		{name: "blockquote", in: "> ✅ Fake clean", want: `\> ✅ Fake clean`},
		{name: "heading", in: "### Forged section", want: `\#` + "## Forged section"},
		{name: "dash-list", in: "- forged item", want: `\- forged item`},
		{name: "star-list", in: "* forged item", want: `\* forged item`},
		{name: "plus-list", in: "+ forged item", want: `\+ forged item`},
		{name: "ordered-dot", in: "1. forged item", want: `\1. forged item`},
		{name: "ordered-paren", in: "2) forged item", want: `\2) forged item`},
		{name: "thematic-dash", in: "---", want: `\---`},
		{name: "thematic-under", in: "___", want: `\___`},
		{name: "normal-prose", in: "Reviewed auth changes.\n\nAll good.", want: "Reviewed auth changes. All good."},
		{name: "empty", in: " \r\n\t ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkIdempotent(t, "Paragraph", Paragraph, tc)
		})
	}
}

func checkHTMLCommentCase(t *testing.T, tc sanitizeCase) {
	t.Helper()
	got := HTMLCommentLine(tc.in)
	if got != tc.want {
		t.Errorf("HTMLCommentLine(%q) = %q, want %q", tc.in, got, tc.want)
	}
	if strings.Contains(got, "--") {
		t.Errorf("HTMLCommentLine(%q) still contains --: %q", tc.in, got)
	}
	if strings.HasPrefix(got, ">") || strings.HasPrefix(got, "->") || strings.HasSuffix(got, "-") {
		t.Errorf("HTMLCommentLine(%q) violates HTML5 boundary: %q", tc.in, got)
	}
}

func TestHTMLCommentLine(t *testing.T) {
	t.Parallel()
	cases := []sanitizeCase{
		{name: "plain", in: "Refactor token refresh helper", want: "Refactor token refresh helper"},
		{name: "close-attempt", in: "fix --> <script>alert(1)</script>", want: "fix -" + zwsp + "-> <script>alert(1)</script>"},
		{name: "triple-dash", in: "a --- b", want: "a -" + zwsp + "-" + zwsp + "- b"},
		{name: "leading-gt-and-trailing-dash", in: ">feat/branch-", want: "feat/branch"},
		{name: "leading-arrow", in: "-> sneaky", want: "sneaky"},
		{name: "multiline-bidi", in: "first\nsecond\u202ethird--!", want: "first second third-" + zwsp + "-!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkHTMLCommentCase(t, tc)
		})
	}
}

func TestCodeSpanAndLinkLabel(t *testing.T) {
	t.Parallel()
	if got := CodeSpan("src/auth.go:42"); got != "`src/auth.go:42`" {
		t.Errorf("CodeSpan plain = %q", got)
	}
	if got := CodeSpan("a`b\nc"); got != "``a`b c``" {
		t.Errorf("CodeSpan embedded tick = %q", got)
	}
	if got := CodeSpan("`edge`"); got != "`` `edge` ``" {
		t.Errorf("CodeSpan edge ticks = %q", got)
	}
	if got := LinkLabel("src/auth.go:42"); got != "src/auth.go:42" {
		t.Errorf("LinkLabel plain = %q", got)
	}
	if got := LinkLabel("dir/[x] <y> @alice.go:1"); got != `dir/\[x\] &lt;y> @`+zwsp+"alice.go:1" {
		t.Errorf("LinkLabel hostile = %q", got)
	}
}

func TestEscapeURLPathFenceAndCapRunes(t *testing.T) {
	t.Parallel()
	if got := EscapeURLPath("dir with spaces/file#1?[a](b).go"); got != "dir%20with%20spaces/file%231%3F%5Ba%5D%28b%29.go" {
		t.Errorf("EscapeURLPath = %q", got)
	}
	if got := FenceFor("plain text", 4); got != "````" {
		t.Errorf("FenceFor(plain, 4) = %q", got)
	}
	if got := FenceFor("has ```` four", 4); got != "`````" {
		t.Errorf("FenceFor(4 ticks, 4) = %q", got)
	}
	if got := CapRunes("abcdef", 6); got != "abcdef" {
		t.Errorf("CapRunes exact = %q", got)
	}
	if got := CapRunes("héllo world", 5); got != "héll…" {
		t.Errorf("CapRunes truncated = %q", got)
	}
}
