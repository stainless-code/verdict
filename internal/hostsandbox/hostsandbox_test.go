package hostsandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestSeatbeltLiteralQuotesWhatItCanAndRefusesTheRest proves a path becomes
// a Seatbelt string with its quotes and backslashes escaped and its parens
// kept literal, and that a control character or invalid UTF-8 — neither of
// which the profile can represent — refuses.
func TestSeatbeltLiteralQuotesWhatItCanAndRefusesTheRest(t *testing.T) {
	for in, want := range map[string]string{
		`/a b/(c)`:    `"/a b/(c)"`,
		`/q"uote`:     `"/q\"uote"`,
		`/back\slash`: `"/back\\slash"`,
	} {
		if got, err := SeatbeltLiteral(in); err != nil || got != want {
			t.Errorf("SeatbeltLiteral(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/new\nline", "/tab\tx", "/del\x7f", "/bad\xffutf8"} {
		if got, err := SeatbeltLiteral(bad); err == nil {
			t.Errorf("SeatbeltLiteral(%q) = %q, want a refusal", bad, got)
		}
	}
}

// TestSeatbeltRegexQuoteMatchesOnlyItself proves every regex metacharacter
// in a path is escaped, so a regex built from it matches the path alone.
func TestSeatbeltRegexQuoteMatchesOnlyItself(t *testing.T) {
	if got, want := SeatbeltRegexQuote(`/a.b/c+d/(e)[f]{g}*?^$|\`), `/a\.b/c\+d/\(e\)\[f\]\{g\}\*\?\^\$\|\\`; got != want {
		t.Errorf("SeatbeltRegexQuote = %q, want %q", got, want)
	}
}

// TestExpandRealpathsAddsEverySpelling proves a path through a link gains its
// resolved spelling and its own node's spelling, and a missing tail resolves
// through its deepest existing ancestor.
func TestExpandRealpathsAddsEverySpelling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got := ExpandRealpaths([]string{filepath.Join(link, "missing", "file")})
	if !slices.Contains(got, filepath.Join(resolved, "missing", "file")) {
		t.Errorf("ExpandRealpaths = %v, want the resolved spelling of the missing tail", got)
	}
	if got := ExpandRealpaths([]string{link}); !slices.Contains(got, resolved) || !slices.Contains(got, link) {
		t.Errorf("ExpandRealpaths(link) = %v, want both the link and its target", got)
	}
}

// TestFilesystemRootSeesThroughALink proves the filesystem root is found
// under its own spelling and through a link that resolves to it.
func TestFilesystemRootSeesThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	if _, ok := FilesystemRoot(string(filepath.Separator)); !ok {
		t.Error("the filesystem root was not recognized")
	}
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(string(filepath.Separator), link); err != nil {
		t.Fatal(err)
	}
	if _, ok := FilesystemRoot(link); !ok {
		t.Error("a link to the filesystem root was not recognized")
	}
	if _, ok := FilesystemRoot(t.TempDir()); ok {
		t.Error("a temp directory was taken for the filesystem root")
	}
}

// TestCredentialTableHidesTheAgentLogins proves the shared table names every
// agent login store a review sandbox and verify hide, under HOME and under
// the variables that relocate them.
func TestCredentialTableHidesTheAgentLogins(t *testing.T) {
	// Every root is absolute in the platform's own form, as Inputs requires.
	base := t.TempDir()
	h, x := filepath.Join(base, "h"), filepath.Join(base, "x")
	got, err := Inputs{
		ChildEnv: []string{
			"HOME=" + h, "CODEX_HOME=" + filepath.Join(x, "codex"), "XDG_DATA_HOME=" + filepath.Join(x, "data"),
			"PI_CODING_AGENT_DIR=" + filepath.Join(x, "pi"), "CLAUDE_CONFIG_DIR=" + filepath.Join(x, "claude"),
			"XDG_CONFIG_HOME=" + filepath.Join(x, "cfg"), "XAUTHORITY=" + filepath.Join(x, "xauth"),
			"OPENCODE_DB=" + filepath.Join(x, "oc.db"),
		},
	}.CredentialLocations(filepath.Join(base, "r"), false)
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{"h": h, "x": x}
	for _, want := range []string{
		"h/.codex/auth.json", "h/.local/share/opencode/auth.json", "h/.pi/agent/auth.json",
		"h/.claude/.credentials.json", "h/.config/github-copilot", "h/.gemini/oauth_creds.json",
		"h/.config/git/credentials", "x/codex/auth.json", "x/data/opencode/auth.json", "x/pi/auth.json",
		"x/claude/.credentials.json", "x/cfg/github-copilot", "x/cfg/git/credentials",
		"h/.local/share/opencode/mcp-auth.json", "x/data/opencode/mcp-auth.json",
		"h/.Xauthority", "x/xauth", "h/.1password", "h/.bitwarden-ssh-agent.sock",
		"h/Library/Group Containers/2BUA8C4S2C.com.1password",
		"h/Library/Containers/com.maxgoedjen.Secretive.SecretAgent",
		"h/.local/share/opencode/opencode.db", "h/.local/share/opencode/opencode.db-wal",
		"h/.local/share/opencode/opencode.db-shm", "x/data/opencode/opencode.db", "x/data/opencode/opencode.db-wal",
		"x/data/opencode/opencode.db-shm", "x/oc.db", "x/oc.db-wal", "x/oc.db-shm",
	} {
		root, rest, _ := strings.Cut(want, "/")
		if path := filepath.Join(roots[root], filepath.FromSlash(rest)); !slices.Contains(got, path) {
			t.Errorf("credential locations lack %s:\n%s", path, strings.Join(got, "\n"))
		}
	}
}
