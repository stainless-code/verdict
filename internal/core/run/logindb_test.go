package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// testdata/opencode-logins.db carries opencode's credential schema (written
// by Python's sqlite3 module) with: an oauth login (opencode), an API key
// (anthropic), openai twice — an imported key changed later and the
// active one — an MCP server's login, a login switched away from (active 0),
// a type auth.json does not hold, and a value that is not JSON.

// staleAuth is the legacy auth.json beside the database: its opencode entry
// predates the database's, and its other entry exists nowhere else.
const staleAuth = `{"opencode":{"type":"api","key":"stale"},"legacy":{"type":"api","key":"kept"}}`

// seededLogins is the auth.json the throwaway home must hold: every provider
// login the database chose, over the copy's entries.
const seededLogins = `{"anthropic":{"key":"an-key","type":"api"},"legacy":{"type":"api","key":"kept"},` +
	`"openai":{"key":"active","type":"api"},"opencode":{"type":"oauth","refresh":"r","access":"a","expires":5}}`

// plantOpencodeDB writes the fixture database and the stale auth.json under
// dir/opencode.
func plantOpencodeDB(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-logins.db"))
	if err != nil {
		t.Fatal(err)
	}
	writeUnder(t, dir, filepath.Join("opencode", "opencode.db"), string(raw))
	writeUnder(t, dir, filepath.Join("opencode", "auth.json"), staleAuth)
}

// TestOpencodeSeedCarriesTheDatabaseLogins proves opencode's throwaway data
// home gets the provider logins its database's credential table holds —
// written over the stale auth.json copy a fresh database imports — and its
// launch names the throwaway database, never the real one.
func TestOpencodeSeedCarriesTheDatabaseLogins(t *testing.T) {
	t.Parallel()
	home, tmp := t.TempDir(), t.TempDir()
	plantOpencodeDB(t, filepath.Join(home, ".local", "share"))
	def, _ := lookupAgent("opencode")
	env, warnings, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("ephemeralEnv: %v, warnings %q", err, warnings)
	}
	dir := filepath.Join(tmp, "home-xdg_data_home")
	if got := readSeeded(t, filepath.Join(dir, "opencode", "auth.json")); string(got) != seededLogins {
		t.Errorf("seeded auth.json =\n%s\nwant\n%s", got, seededLogins)
	}
	if want := "OPENCODE_DB=" + filepath.Join(dir, "opencode", "opencode.db"); !slices.Contains(env, want) {
		t.Errorf("env = %q, want %s", env, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "opencode", "opencode.db")); !os.IsNotExist(err) {
		t.Errorf("the real database was copied into the throwaway home (err = %v)", err)
	}
}

// TestOpencodeSeedReadsARelocatedDatabase proves OPENCODE_DB, which moves
// opencode's database, is where the logins are read from.
func TestOpencodeSeedReadsARelocatedDatabase(t *testing.T) {
	t.Parallel()
	home, moved, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	plantOpencodeDB(t, moved)
	def, _ := lookupAgent("opencode")
	db := filepath.Join(moved, "opencode", "opencode.db")
	if _, _, err := ephemeralEnv(&def, []string{"HOME=" + home, "OPENCODE_DB=" + db}, tmp); err != nil {
		t.Fatal(err)
	}
	got := readSeeded(t, filepath.Join(tmp, "home-xdg_data_home", "opencode", "auth.json"))
	want := `{"anthropic":{"key":"an-key","type":"api"},"openai":{"key":"active","type":"api"},` +
		`"opencode":{"type":"oauth","refresh":"r","access":"a","expires":5}}`
	if string(got) != want {
		t.Errorf("seeded auth.json =\n%s\nwant\n%s", got, want)
	}
}

// TestOpencodeSeedRefusesALinkedDatabase proves a database that is a
// symlink — to another agent's file — refuses the launch rather than being
// read.
func TestOpencodeSeedRefusesALinkedDatabase(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"opencode.db", "opencode.db-wal"} {
		home, other := t.TempDir(), t.TempDir()
		data := filepath.Join(home, ".local", "share")
		plantOpencodeDB(t, data)
		writeUnder(t, other, "secret", "another agent's login")
		link := filepath.Join(data, "opencode", name)
		_ = os.Remove(link)
		if err := os.Symlink(filepath.Join(other, "secret"), link); err != nil {
			t.Fatal(err)
		}
		def, _ := lookupAgent("opencode")
		if _, _, err := ephemeralEnv(&def, []string{"HOME=" + home}, t.TempDir()); err == nil ||
			!strings.Contains(err.Error(), "opencode.db") {
			t.Errorf("a linked %s = %v, want the launch refused", name, err)
		}
	}
}

// TestOpencodeSeedSkipsAMissingDatabase proves a home with no opencode data,
// or a relocation naming a directory that does not exist, seeds nothing and
// refuses nothing.
func TestOpencodeSeedSkipsAMissingDatabase(t *testing.T) {
	t.Parallel()
	def, _ := lookupAgent("opencode")
	home := t.TempDir()
	for _, env := range [][]string{
		{"HOME=" + home},
		{"HOME=" + home, "OPENCODE_DB=" + filepath.Join(home, "gone", "opencode.db")},
	} {
		if _, warnings, err := ephemeralEnv(&def, env, t.TempDir()); err != nil || len(warnings) != 0 {
			t.Errorf("%q: err = %v, warnings = %q; want neither", env, err, warnings)
		}
	}
}

// TestOpencodeSeedWarnsOnAnUnreadableDatabase proves a database that is not
// a credential table leaves the auth.json copy as it was and says why.
func TestOpencodeSeedWarnsOnAnUnreadableDatabase(t *testing.T) {
	t.Parallel()
	home, tmp := t.TempDir(), t.TempDir()
	data := filepath.Join(home, ".local", "share")
	writeUnder(t, data, filepath.Join("opencode", "opencode.db"), strings.Repeat("not a database ", 10))
	writeUnder(t, data, filepath.Join("opencode", "auth.json"), staleAuth)
	def, _ := lookupAgent("opencode")
	_, warnings, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not a SQLite database") ||
		!strings.Contains(warnings[0], "only the copy of auth.json") {
		t.Errorf("warnings = %q", warnings)
	}
	if got := readSeeded(t, filepath.Join(tmp, "home-xdg_data_home", "opencode", "auth.json")); string(got) != staleAuth {
		t.Errorf("auth.json = %s, want the copy unchanged", got)
	}
}

// readSeeded reads one seeded file.
func readSeeded(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// agyKeychainWarning is the warning a sandboxed macOS agy launch carries
// when nothing but its keychain sign-in could log it in.
const agyKeychainWarning = "agy's saved macOS sign-in is in your login keychain, which verdict's review sandbox " +
	"keeps closed to it: agy keeps a shell in every review, and with the keychain open a prompt-injected diff " +
	"could read every token stored there (gh's among them) into review text; set GEMINI_API_KEY for its review " +
	"launches; pass --allow-unsandboxed-review to review without the sandbox"

// TestAgyKeychainLoginWarnsUnderSeatbelt proves a macOS sandbox launch of
// agy, whose saved sign-in is the keychain its profile keeps closed, names
// the variable that signs it in instead — and that nothing warns once that
// variable is set, under bwrap, or for a row whose login is a file.
func TestAgyKeychainLoginWarnsUnderSeatbelt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, agent, mechanism string
		env                    []string
		want                   []string
	}{
		{name: "seatbelt", agent: "agy", mechanism: hostsandbox.SandboxExecTool, want: []string{agyKeychainWarning}},
		{name: "api key set", agent: "agy", mechanism: hostsandbox.SandboxExecTool, env: []string{"GEMINI_API_KEY=k"}},
		{name: "bwrap", agent: "agy", mechanism: hostsandbox.BwrapTool},
		{name: "file login", agent: "pi", mechanism: hostsandbox.SandboxExecTool},
	} {
		def, _ := lookupAgent(tc.agent)
		st := &state{req: &core.RunRequest{Environ: tc.env, UnsandboxedOptOut: true}}
		st.box = &reviewBox{mechanism: tc.mechanism, posture: postureSandboxed}
		st.warnKeychainLogin(&def)
		if !slices.Equal(st.warnings, tc.want) {
			t.Errorf("%s: warnings = %q, want %q", tc.name, st.warnings, tc.want)
		}
	}
}
