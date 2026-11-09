package recordcore

import (
	"strings"
	"testing"
)

// zwsp is the escape under test, spelled once so a case reads as the bytes it
// expects rather than as an invisible character.
const zwsp = "\u200b"

type mentionCase struct {
	name string
	in   string
	want string
}

// TestNeutralizeMentions walks the body shapes the escape must and must not
// touch. Every case also asserts idempotence: neutralizing the wanted bytes
// again returns them unchanged, because the escape sits between `@` and the
// handle, so the handle no longer starts with a handle byte.
func TestNeutralizeMentions(t *testing.T) {
	t.Parallel()
	cases := []mentionCase{
		{
			name: "prose",
			in:   "Ping @alice about the retry loop before merging.\n",
			want: "Ping @" + zwsp + "alice about the retry loop before merging.\n",
		},
		{
			name: "line-start",
			in:   "@alice see the note above.\n",
			want: "@" + zwsp + "alice see the note above.\n",
		},
		{
			name: "blockquote",
			in:   "> @alice the marker line still renders.\n",
			want: "> @" + zwsp + "alice the marker line still renders.\n",
		},
		{
			name: "handle-charset",
			in:   "ask @alice-b2 and @B0b now.\n",
			want: "ask @" + zwsp + "alice-b2 and @" + zwsp + "B0b now.\n",
		},
		{
			name: "inline-code",
			in:   "Use `@alice` literally, then @bob.\n",
			want: "Use `@alice` literally, then @" + zwsp + "bob.\n",
		},
		{
			name: "inline-code-double-backtick",
			in:   "Use ``@alice` -> x`` literally.\n",
			want: "Use ``@alice` -> x`` literally.\n",
		},
		{
			name: "inline-code-across-lines",
			in:   "Use `@alice\nand @bob` literally.\n",
			want: "Use `@alice\nand @bob` literally.\n",
		},
		{
			name: "fenced",
			in:   "```go\n// ping @alice in a sample\n```\n",
			want: "```go\n// ping @alice in a sample\n```\n",
		},
		{
			name: "fenced-tilde",
			in:   "~~~\ncurl -X POST @alice\n~~~\n",
			want: "~~~\ncurl -X POST @alice\n~~~\n",
		},
		{
			name: "fenced-suggestion",
			in:   "```suggestion\nnotify(@alice)\n```\n",
			want: "```suggestion\nnotify(@alice)\n```\n",
		},
		{
			name: "prose-after-fence",
			in:   "```\n@alice\n```\n\nPing @bob.\n",
			want: "```\n@alice\n```\n\nPing @" + zwsp + "bob.\n",
		},
		{
			name: "unclosed-fence",
			in:   "```\n@alice\n@bob\n",
			want: "```\n@alice\n@bob\n",
		},
		{
			name: "email",
			in:   "Write to user@example.com about it.\n",
			want: "Write to user@example.com about it.\n",
		},
		{
			name: "bracketed",
			in:   "Thanks (@alice), merging now.\n",
			want: "Thanks (@" + zwsp + "alice), merging now.\n",
		},
		{
			name: "bold",
			in:   "Credit **@alice** for the fix.\n",
			want: "Credit **@" + zwsp + "alice** for the fix.\n",
		},
		{
			name: "underscore-emphasis",
			in:   "Credit _@alice_ for the fix.\n",
			want: "Credit _@" + zwsp + "alice_ for the fix.\n",
		},
		{
			name: "punctuated-sentence-close",
			in:   "Thanks, @alice.\n",
			want: "Thanks, @" + zwsp + "alice.\n",
		},
		{
			name: "address-with-at-local",
			in:   "Mail first.last+tag@example.co.uk.\n",
			want: "Mail first.last+tag@example.co.uk.\n",
		},
		{
			name: "address-in-parentheses",
			in:   "Mail (dev@example.com) about it.\n",
			want: "Mail (dev@example.com) about it.\n",
		},
		{
			name: "already-neutralized",
			in:   "Ping @" + zwsp + "alice and @" + zwsp + "bob.\n",
			want: "Ping @" + zwsp + "alice and @" + zwsp + "bob.\n",
		},
		{
			name: "html-comment",
			in:   "<!-- @alice asked for this -->\nPing @bob.\n",
			want: "<!-- @alice asked for this -->\nPing @" + zwsp + "bob.\n",
		},
		{
			name: "finding-marker",
			in:   "### Retry loop\n\n<!-- verdict-finding sig=abc123 @alice -->\n",
			want: "### Retry loop\n\n<!-- verdict-finding sig=abc123 @alice -->\n",
		},
		{
			name: "multiline-comment",
			in:   "<!--\n@alice\n-->\nPing @bob.\n",
			want: "<!--\n@alice\n-->\nPing @" + zwsp + "bob.\n",
		},
		{
			name: "no-handle",
			in:   "Mail @ example, or the @ alone.\n",
			want: "Mail @ example, or the @ alone.\n",
		},
		{
			name: "no-mentions",
			in:   "> [!IMPORTANT]\n> Nothing to flag: 2 findings, 0 open.\n\n### Basis\n\nAll quiet.\n",
			want: "> [!IMPORTANT]\n> Nothing to flag: 2 findings, 0 open.\n\n### Basis\n\nAll quiet.\n",
		},
		{
			name: "crlf",
			in:   "Ping @alice.\r\n```\r\n@bob\r\n```\r\n",
			want: "Ping @" + zwsp + "alice.\r\n```\r\n@bob\r\n```\r\n",
		},
		{
			name: "no-trailing-newline",
			in:   "Ping @alice",
			want: "Ping @" + zwsp + "alice",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkMentionCase(t, tc)
		})
	}
}

// checkMentionCase grades one row and its idempotence.
func checkMentionCase(t *testing.T, tc mentionCase) {
	t.Helper()
	got := NeutralizeMentions(tc.in)
	if got != tc.want {
		t.Errorf("NeutralizeMentions(%q) = %q; want %q", tc.in, got, tc.want)
	}
	if again := NeutralizeMentions(got); again != got {
		t.Errorf("not idempotent: %q -> %q", got, again)
	}
}

// TestNeutralizeMentionsLeavesTheBodyLength proves the escape only ever adds
// bytes: a body grows by one zero-width space per mention and never shrinks,
// so no downstream length guard can see a shorter body than the agent wrote.
func TestNeutralizeMentionsLeavesTheBodyLength(t *testing.T) {
	t.Parallel()
	in := "Ping @alice about @bob's note.\n"
	got := NeutralizeMentions(in)
	if len(got) != len(in)+2*len(zeroWidthSpace) {
		t.Errorf("len(got) = %d, want %d", len(got), len(in)+2*len(zeroWidthSpace))
	}
	if strings.Count(got, zeroWidthSpace) != 2 {
		t.Errorf("zero-width spaces = %d, want 2 in %q", strings.Count(got, zeroWidthSpace), got)
	}
}
