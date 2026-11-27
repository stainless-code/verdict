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
			"OPENCODE_DB=" + filepath.Join(x, "db", "oc.db"),
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
		"h/.local/share/opencode", "x/data/opencode", "x/db",
	} {
		root, rest, _ := strings.Cut(want, "/")
		if path := filepath.Join(roots[root], filepath.FromSlash(rest)); !slices.Contains(got, path) {
			t.Errorf("credential locations lack %s:\n%s", path, strings.Join(got, "\n"))
		}
	}
}

// TestCredentialTableHidesARelativeOpenCodeDB proves a relative OPENCODE_DB
// is hidden where opencode resolves it — below each opencode data
// directory — and ":memory:" names nothing.
func TestCredentialTableHidesARelativeOpenCodeDB(t *testing.T) {
	base := t.TempDir()
	h, x := filepath.Join(base, "h"), filepath.Join(base, "x")
	got, err := Inputs{ChildEnv: []string{"HOME=" + h, "XDG_DATA_HOME=" + x, "OPENCODE_DB=../moved/oc.db"}}.
		CredentialLocations(filepath.Join(base, "r"), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Join(x, "moved"), filepath.Join(h, ".local", "share", "moved")} {
		if !slices.Contains(got, want) {
			t.Errorf("credential locations lack %s:\n%s", want, strings.Join(got, "\n"))
		}
	}
	memory, err := Inputs{ChildEnv: []string{"HOME=" + h, "OPENCODE_DB=:memory:"}}.
		CredentialLocations(filepath.Join(base, "r"), false)
	if err != nil || slices.Contains(memory, filepath.Join(h, ".local", "share", "opencode", ":memory:")) {
		t.Errorf(":memory: = %v, %v; want no path", memory, err)
	}
}

// TestCredentialTableRefusesToHideAHome proves an OPENCODE_DB directly in
// HOME refuses, since hiding its directory would hide the home.
func TestCredentialTableRefusesToHideAHome(t *testing.T) {
	base := t.TempDir()
	h := filepath.Join(base, "h")
	_, err := Inputs{ChildEnv: []string{"HOME=" + h, "OPENCODE_DB=" + filepath.Join(h, "oc.db")}}.
		CredentialLocations(filepath.Join(base, "r"), false)
	if err == nil || !strings.Contains(err.Error(), "a home, or above one") {
		t.Fatalf("OPENCODE_DB in HOME = %v, want a refusal", err)
	}
}

// TestCredentialTableHidesALinkedOpenCodeDBTarget proves an OPENCODE_DB that
// is a link hides the directory of the file it names as well — where SQLite
// opens the database and keeps its journal and log.
func TestCredentialTableHidesALinkedOpenCodeDBTarget(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := linkedDB(t, base, target)
	got, err := Inputs{ChildEnv: []string{"HOME=" + filepath.Join(base, "h"), "OPENCODE_DB=" + link}}.
		CredentialLocations(filepath.Join(base, "r"), false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Dir(link), resolved} {
		if !slices.Contains(got, want) {
			t.Errorf("credential locations lack %s:\n%s", want, strings.Join(got, "\n"))
		}
	}
}

// linkedDB writes a database in target and returns a link to it in a
// directory of its own under base.
func linkedDB(t *testing.T, base, target string) string {
	t.Helper()
	link := filepath.Join(base, "link", "oc.db")
	for _, dir := range []string{target, filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "oc.db"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "oc.db"), link); err != nil {
		t.Fatal(err)
	}
	return link
}
