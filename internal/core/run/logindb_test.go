package run

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	"could read every token stored there (gh's among them) into review text; set GEMINI_API_KEY and configure " +
	"agy to sign in with it rather than your saved account; pass --allow-unsandboxed-review to review without " +
	"the sandbox"

// TestAgyKeychainLoginWarnsUnderSeatbelt proves a macOS sandbox launch of
// agy, whose saved sign-in is the keychain its profile keeps closed, names
// the variable that signs it in instead and the setting it needs — still
// warning once the variable is set, since agy reads it only when configured
// to — and that nothing warns under bwrap or for a row whose login is a file.
func TestAgyKeychainLoginWarnsUnderSeatbelt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, agent, mechanism string
		env                    []string
		want                   []string
	}{
		{name: "seatbelt", agent: "agy", mechanism: hostsandbox.SandboxExecTool, want: []string{agyKeychainWarning}},
		{
			name: "api key set", agent: "agy", mechanism: hostsandbox.SandboxExecTool, env: []string{"GEMINI_API_KEY=k"},
			want: []string{agyKeychainWarning},
		},
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

// TestOpencodeSeedResolvesOpencodeDBAsOpencodeDoes proves a relative
// OPENCODE_DB is read below opencode's data directory, where opencode opens
// it, and ":memory:" seeds nothing.
func TestOpencodeSeedResolvesOpencodeDBAsOpencodeDoes(t *testing.T) {
	t.Parallel()
	def, _ := lookupAgent("opencode")
	home, tmp := t.TempDir(), t.TempDir()
	data := filepath.Join(home, ".local", "share")
	plantOpencodeDB(t, filepath.Join(data, "moved"))
	if err := os.Rename(filepath.Join(data, "moved", "opencode"), filepath.Join(data, "opencode")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(data, "opencode", "opencode.db"), filepath.Join(data, "opencode", "rel.db")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ephemeralEnv(&def, []string{"HOME=" + home, "OPENCODE_DB=rel.db"}, tmp); err != nil {
		t.Fatal(err)
	}
	if got := readSeeded(t, filepath.Join(tmp, "home-xdg_data_home", "opencode", "auth.json")); string(got) != seededLogins {
		t.Errorf("seeded auth.json =\n%s\nwant\n%s", got, seededLogins)
	}
	memory := t.TempDir()
	if _, _, err := ephemeralEnv(&def, []string{"HOME=" + home, "OPENCODE_DB=:memory:"}, memory); err != nil {
		t.Fatal(err)
	}
	if got := readSeeded(t, filepath.Join(memory, "home-xdg_data_home", "opencode", "auth.json")); string(got) != staleAuth {
		t.Errorf(":memory: seeded %s, want the copy alone", got)
	}
}

// TestOpencodeSeedWarnsOnAChannelDatabase proves a database a channel build
// names (opencode-<channel>.db) beside opencode.db is named in a warning,
// never read silently in its place.
func TestOpencodeSeedWarnsOnAChannelDatabase(t *testing.T) {
	t.Parallel()
	def, _ := lookupAgent("opencode")
	home := t.TempDir()
	data := filepath.Join(home, ".local", "share")
	plantOpencodeDB(t, data)
	writeUnder(t, data, filepath.Join("opencode", "opencode-canary.db"), "x")
	_, warnings, err := ephemeralEnv(&def, []string{"HOME=" + home}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "opencode keeps opencode-canary.db beside opencode.db") {
		t.Errorf("warnings = %q, want the channel database named", warnings)
	}
}

// TestAllowShellClosesClaudesKeychain proves --allow-shell, which gives
// claude a shell, closes its keychain login like agy's: the box keeps no
// keychain state, the macOS profile opens no keychain broker and no longer
// re-allows the keychain files the credential table denies, and a macOS
// launch warns to sign in with ANTHROPIC_API_KEY. Without the shell the
// keychain stays claude's.
func TestAllowShellClosesClaudesKeychain(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("%s has no review sandbox policy to build", runtime.GOOS)
	}
	assertShellKeychain(t, false)
	assertShellKeychain(t, true)
	def, _ := lookupAgent("claude")
	st := &state{req: &core.RunRequest{UnsandboxedOptOut: true}}
	st.box = &reviewBox{mechanism: hostsandbox.SandboxExecTool, posture: postureSandboxed, shellKeychain: true}
	st.warnKeychainLogin(&def)
	want := "claude's saved macOS sign-in is in your login keychain, which verdict's review sandbox keeps closed " +
		"to it: --allow-shell gives claude a shell, and with the keychain open a prompt-injected diff could read " +
		"every token stored there (gh's among them) into review text; set ANTHROPIC_API_KEY for its review " +
		"launches; pass --allow-unsandboxed-review to review without the sandbox"
	if !slices.Equal(st.warnings, []string{want}) {
		t.Errorf("warnings = %q, want %q", st.warnings, want)
	}
	keyed := &state{req: &core.RunRequest{Environ: []string{"ANTHROPIC_API_KEY=k"}}, box: st.box}
	keyed.warnKeychainLogin(&def)
	if len(keyed.warnings) != 0 {
		t.Errorf("claude with its documented ANTHROPIC_API_KEY set warned: %q", keyed.warnings)
	}
}

// assertShellKeychain builds claude's box with or without a shell and checks
// the keychain is its own exactly when the shell is denied.
func assertShellKeychain(t *testing.T, shell bool) {
	t.Helper()
	req, _ := sandboxRequest(t, "claude", lookEverything)
	req.Print, req.AllowShell = true, shell
	st := &state{req: req}
	def, _ := st.lookupAgent("claude")
	box, why, err := st.buildBox(t.Context(), &def)
	if err != nil || why != "" {
		t.Fatalf("shell %v: buildBox = %v, %q", shell, err, why)
	}
	profile, err := seatbeltReviewProfile([]string{"/w"}, "", box)
	if err != nil {
		t.Fatal(err)
	}
	keychain := `(subpath "` + filepath.Join(box.home, "Library", "Keychains") + `")`
	own := !shell
	if box.keychain() != own || strings.Contains(profile, `"com.apple.SecurityServer"`) != own ||
		strings.Contains(profile, "(allow file-read* file-write* "+keychain) != own {
		t.Errorf("shell %v: keychain() = %v; profile:\n%s", shell, box.keychain(), profile)
	}
}

// TestMakeHiddenDirMakesAMissingDataDir proves a missing opencode data
// directory is made, empty and private, that one already there is left
// alone, and that a missing data root is made too: every missing level from
// the deepest existing ancestor down, each 0700.
func TestMakeHiddenDirMakesAMissingDataDir(t *testing.T) {
	t.Parallel()
	share := t.TempDir()
	dir := filepath.Join(share, "opencode")
	if err := makeHiddenDir(dir); err != nil {
		t.Fatal(err)
	}
	assertPrivateDir(t, dir)
	writeUnder(t, dir, "kept", "x")
	if err := makeHiddenDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kept")); err != nil {
		t.Errorf("an existing directory was replaced: %v", err)
	}
	chain := filepath.Join(share, "local", "share", "opencode")
	if err := makeHiddenDir(chain); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{filepath.Join(share, "local"), filepath.Join(share, "local", "share"), chain} {
		assertPrivateDir(t, level)
	}
}

// assertPrivateDir fails unless dir is a directory of mode 0700 (any mode
// on Windows, which has none).
func assertPrivateDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("made %v (%v), want a 0700 directory", info, err)
	}
}

// TestMakeHiddenDirMakesBelowALinkedRoot proves a data root that is a link
// gets its directory below the real directory it names — where the kernel,
// and bwrap's mount over the link, land — never through the link.
func TestMakeHiddenDirMakesBelowALinkedRoot(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base, elsewhere := t.TempDir(), t.TempDir()
	share := filepath.Join(base, "share")
	if err := os.Symlink(elsewhere, share); err != nil {
		t.Fatal(err)
	}
	if err := makeHiddenDir(filepath.Join(share, "opencode")); err != nil {
		t.Fatal(err)
	}
	assertPrivateDir(t, filepath.Join(elsewhere, "opencode"))
}

// TestLinuxLaunchMakesTheOpencodeDBDir proves the directories a Linux launch
// makes before bwrap include the one an OPENCODE_DB in a missing directory
// names, beside every opencode data directory, so its database is hidden
// once a host opencode creates it.
func TestLinuxLaunchMakesTheOpencodeDBDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home, db := filepath.Join(base, "h"), filepath.Join(base, "missing", "deeper", "oc.db")
	dirs, err := openCodeDataDirs(hostsandbox.Inputs{ChildEnv: []string{"HOME=" + home, "OPENCODE_DB=" + db}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(home, ".local", "share", "opencode"), hostsandbox.ResolveExisting(filepath.Dir(db)),
	} {
		if !slices.Contains(dirs, want) {
			t.Errorf("dirs = %q, want %s among them", dirs, want)
		}
	}
}

// TestProbeRefusesAPolicyItCannotBuild proves a wrapper the probe cannot
// build — a credential directory that cannot be made — is a policy error,
// which refuses every launch, never a reason the host has no sandbox, which
// would let a self-confining row run unsandboxed.
func TestProbeRefusesAPolicyItCannotBuild(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	writeUnder(t, filepath.Dir(file), "file", "x")
	box := &reviewBox{mechanism: hostsandbox.BwrapTool, runner: "/bin/false", hiddenDataDirs: []string{
		filepath.Join(file, "share", "opencode"),
	}}
	st := &state{req: &core.RunRequest{}}
	why, err := st.probeBox(t.Context(), box)
	var policy *policyError
	if why != "" || !errors.As(err, &policy) {
		t.Fatalf("probeBox = %q, %v; want a policy error", why, err)
	}
}
