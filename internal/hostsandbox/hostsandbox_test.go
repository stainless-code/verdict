package hostsandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
	base := resolvedTempDir(t)
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
	base := resolvedTempDir(t)
	h, x := filepath.Join(base, "h"), filepath.Join(base, "x")
	mustMkdirAll(t, filepath.Join(x, "opencode"))
	mustMkdirAll(t, filepath.Join(h, ".local", "share", "opencode"))
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
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := resolvedTempDir(t)
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

// TestCredentialTableHidesALinkedDefaultOpenCodeDB proves the default
// database, OPENCODE_DB unset, is resolved like an explicit one: an
// opencode.db that is a link hides its target's directory too.
func TestCredentialTableHidesALinkedDefaultOpenCodeDB(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	h, target := filepath.Join(base, "h"), filepath.Join(base, "target")
	data := filepath.Join(h, ".local", "share", "opencode")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	link := linkedDB(t, base, target)
	if err := os.Rename(link, filepath.Join(data, "opencode.db")); err != nil {
		t.Fatal(err)
	}
	got, err := Inputs{ChildEnv: []string{"HOME=" + h}}.CredentialLocations(filepath.Join(base, "r"), false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, resolved) {
		t.Errorf("credential locations lack the default database's target %s:\n%s", resolved, strings.Join(got, "\n"))
	}
}

// TestCredentialTableRefusesADataDirLinkedToAHome proves a credential
// location is compared with the homes by what it resolves to: an opencode
// data directory that is a link to HOME refuses rather than hiding HOME.
func TestCredentialTableRefusesADataDirLinkedToAHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	h := filepath.Join(base, "h")
	share := filepath.Join(h, ".local", "share")
	if err := os.MkdirAll(share, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(h, filepath.Join(share, "opencode")); err != nil {
		t.Fatal(err)
	}
	_, err := Inputs{ChildEnv: []string{"HOME=" + h}}.CredentialLocations(filepath.Join(base, "r"), false)
	if err == nil || !strings.Contains(err.Error(), "a home, or above one") {
		t.Fatalf("a data directory linked to HOME = %v, want a refusal", err)
	}
}

// TestCredentialTableHidesADanglingOpenCodeDBLinkTarget proves a database
// link whose target does not exist yet hides the directory it points into,
// where the database would appear — an existing one, or a missing one (made
// before a Linux launch), never skipped.
func TestCredentialTableHidesADanglingOpenCodeDBLinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := resolvedTempDir(t)
	target := filepath.Join(base, "target")
	for name, want := range map[string]string{
		"oc.db":      target,
		"nowhere.db": filepath.Join(base, "missing", "deeper"),
	} {
		link := symlinkIn(t, filepath.Join(base, "link"), name, filepath.Join(want, "oc.db"), target)
		got, err := Inputs{ChildEnv: []string{"HOME=" + filepath.Join(base, "h"), "OPENCODE_DB=" + link}}.
			CredentialLocations(filepath.Join(base, "r"), false)
		if err != nil || !slices.Contains(got, want) {
			t.Errorf("%s: credential locations lack %s (%v):\n%s", name, want, err, strings.Join(got, "\n"))
		}
	}
}

// TestResolveDBDirFollowsTheKernelsPath proves a database link is resolved
// as the kernel resolves it: every hop of a chain to a missing leaf, and a
// relative `..` target against the real directory of a link reached through
// a linked directory; a chain past the hop limit refuses.
func TestResolveDBDirFollowsTheKernelsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := resolvedTempDir(t)
	// a → b → y/oc.db, missing: the database appears in y.
	y := filepath.Join(base, "y")
	b := symlinkIn(t, filepath.Join(base, "x"), "oc.db", filepath.Join(y, "oc.db"), y)
	a := symlinkIn(t, filepath.Join(base, "w"), "oc.db", b)
	// share/opencode → disk/oc, and disk/oc/opencode.db → ../store/opencode.db:
	// the kernel lands in disk/store, not share/store.
	disk := filepath.Join(base, "disk", "oc")
	symlinkIn(t, filepath.Join(base, "share"), "opencode", disk, disk, filepath.Join(base, "disk", "store"))
	symlinkIn(t, disk, "opencode.db", filepath.Join("..", "store", "opencode.db"))
	for path, want := range map[string]string{
		a: y,
		filepath.Join(base, "share", "opencode", "opencode.db"): filepath.Join(base, "disk", "store"),
	} {
		if got, err := ResolveDBDir(path); err != nil || got != want {
			t.Errorf("ResolveDBDir(%s) = %q, %v; want %q", path, got, err, want)
		}
	}
}

// TestResolveDBDirRefusesAChainPastTheHopLimit proves a link chain longer
// than the kernel's bound refuses rather than hiding a guess.
func TestResolveDBDirRefusesAChainPastTheHopLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	loop := filepath.Join(resolvedTempDir(t), "loop")
	if err := os.MkdirAll(loop, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range maxLinkHops + 1 {
		if err := os.Symlink(filepath.Join(loop, strconv.Itoa(i+1)), filepath.Join(loop, strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolveDBDir(filepath.Join(loop, "0")); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("a chain past the hop limit = %v, want a refusal", err)
	}
}

// resolvedTempDir is a test directory under its resolved spelling, so a
// resolved path compares equal.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// symlinkIn makes dir and every extra directory, then a link named name in
// dir pointing at target, and returns the link.
func symlinkIn(t *testing.T, dir, name, target string, extra ...string) string {
	t.Helper()
	for _, d := range append([]string{dir}, extra...) {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, name)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// TestResolvePathWalksAsTheKernelDoes pins the one resolver against the
// layouts a lexical or partial resolver gets wrong: a `..` after a link
// applied to the link's real target, a dangling link mid-path whose target
// is where the database will be made, chains with relative and absolute
// targets, and the refusals — a loop, a file mid-path, a missing tail whose
// `..` leaves a directory that does not exist yet.
func TestResolvePathWalksAsTheKernelDoes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := resolvedTempDir(t)
	sep := string(filepath.Separator)
	for _, dir := range []string{"external/nested", "configured", "real", "chain"} {
		mustMkdirAll(t, filepath.Join(base, filepath.FromSlash(dir)))
	}
	writeFile(t, filepath.Join(base, "real", "f"))
	writeFile(t, filepath.Join(base, "file"))
	link(t, filepath.Join("..", "external", "nested"), filepath.Join(base, "configured", "jump"))
	link(t, "jump/../oc.db", filepath.Join(base, "configured", "db"))
	link(t, filepath.Join(base, "missing", "external", "data"), filepath.Join(base, "share"))
	link(t, "../real/f", filepath.Join(base, "chain", "rel"))
	link(t, filepath.Join(base, "chain", "rel"), filepath.Join(base, "chain", "abs"))
	link(t, "l2", filepath.Join(base, "l1"))
	link(t, "l1", filepath.Join(base, "l2"))
	for path, want := range map[string]string{
		filepath.Join(base, "configured", "db"):                                            filepath.Join(base, "external", "oc.db"),
		filepath.Join(base, "share", "opencode", "opencode.db"):                            filepath.Join(base, "missing", "external", "data", "opencode", "opencode.db"),
		filepath.Join(base, "chain", "abs"):                                                filepath.Join(base, "real", "f"),
		base + sep + "configured" + sep + "jump" + sep + ".." + sep + "nested" + sep + "x": filepath.Join(base, "external", "nested", "x"),
	} {
		if got, err := ResolvePath(path); err != nil || got != want {
			t.Errorf("ResolvePath(%s) = %q, %v; want %q", path, got, err, want)
		}
	}
	assertRefused(t, filepath.Join(base, "l1"), filepath.Join(base, "file", "x"),
		base+sep+"gone"+sep+".."+sep+".."+sep+"x", base+sep+"gone"+sep+"x"+sep+".."+sep+"y")
}

func TestCredentialResolutionRefusesUncertainLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for _, tc := range []struct{ name, target, reason string }{
		{"missing tail", "missing/../jump/../oc.db", "steps out of a directory that does not exist yet"},
		{"empty target", "", "has an empty target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkUncertainLink(t, tc.target, tc.reason)
		})
	}
}

func checkUncertainLink(t *testing.T, target, reason string) {
	t.Helper()
	if target == "" && runtime.GOOS != "darwin" {
		t.Skip("empty symlinks need a filesystem that can create them")
	}
	base := resolvedTempDir(t)
	mustMkdirAll(t, filepath.Join(base, "external", "nested"))
	link(t, "external/nested", filepath.Join(base, "jump"))
	db := filepath.Join(base, "db")
	link(t, target, db)
	in := Inputs{ChildEnv: []string{"HOME=" + filepath.Join(base, "home"), "OPENCODE_DB=" + db}}
	_, err := in.CredentialLocations(filepath.Join(base, "repo"), true)
	if err == nil || !strings.Contains(err.Error(), "OPENCODE_DB") || !strings.Contains(err.Error(), reason) {
		t.Errorf("CredentialLocations = %v, want OPENCODE_DB refusal containing %q", err, reason)
	}
}

// assertRefused fails for each path ResolvePath resolves instead of
// refusing.
func assertRefused(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if got, err := ResolvePath(path); err == nil {
			t.Errorf("ResolvePath(%s) = %q, want a refusal", path, got)
		}
	}
}

// TestResolvePathMatchesEvalSymlinks is a property test: over seeded random
// trees of directories and links (relative and absolute, to anywhere in the
// tree), a random walk through them — links, `.`, and `..` — to a file that
// exists resolves exactly as [filepath.EvalSymlinks] resolves it.
func TestResolvePathMatchesEvalSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for seed := range uint64(40) {
		rng := &splitmix{state: seed}
		base := resolvedTempDir(t)
		randomTree(t, rng, base)
		nested := filepath.Join(base, "db", strconv.Itoa(rng.intN(1000)))
		mustMkdirAll(t, nested)
		writeFile(t, filepath.Join(base, "db", "leaf"))
		link(t, nested, filepath.Join(base, "jump"))
		assertOperatorDBResolvesLikeKernel(t, seed, base+"/jump/../leaf", base)
		for range 25 {
			assertResolvesLikeKernel(t, seed, randomWalk(rng, base))
		}
	}
}

func assertOperatorDBResolvesLikeKernel(t *testing.T, seed uint64, path, base string) {
	t.Helper()
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	in := Inputs{ChildEnv: []string{"HOME=" + filepath.Join(base, "home"), "OPENCODE_DB=" + path}}
	got, err := in.CredentialLocations(filepath.Join(base, "repo"), false)
	if err != nil || !slices.Contains(got, filepath.Dir(want)) {
		t.Errorf("seed %d: OPENCODE_DB=%q: missing %q in %q (%v)", seed, path, filepath.Dir(want), got, err)
	}
}

// assertResolvesLikeKernel compares ResolvePath with EvalSymlinks for a path
// that exists.
func assertResolvesLikeKernel(t *testing.T, seed uint64, path string) {
	t.Helper()
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		return
	}
	if got, err := ResolvePath(path); err != nil || got != want {
		t.Fatalf("seed %d: ResolvePath(%s) = %q, %v; EvalSymlinks = %q", seed, path, got, err, want)
	}
}

// splitmix is a seeded generator (SplitMix64) the property test draws from,
// so every run builds the same trees.
type splitmix struct{ state uint64 }

// intN is a draw in [0, n).
func (r *splitmix) intN(n int) int {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return int((z ^ z>>31) % uint64(n))
}

// randomTree makes nested directories under base, each holding a file
// named leaf, and links among them.
func randomTree(t *testing.T, rng *splitmix, base string) {
	t.Helper()
	dirs := []string{base}
	for i := range 8 {
		dirs = append(dirs, filepath.Join(dirs[rng.intN(len(dirs))], "d"+strconv.Itoa(i)))
		mustMkdirAll(t, dirs[len(dirs)-1])
	}
	for _, dir := range dirs {
		writeFile(t, filepath.Join(dir, "leaf"))
	}
	for i := range 8 {
		at, to := dirs[rng.intN(len(dirs))], dirs[rng.intN(len(dirs))]
		target := to
		if rel, err := filepath.Rel(at, to); err == nil && rng.intN(2) == 0 {
			target = rel
		}
		link(t, target, filepath.Join(at, "l"+strconv.Itoa(i)))
	}
}

// randomWalk is a path from base through random entries — directories,
// links, `.`, `..` — ending at a leaf file, written without cleaning.
func randomWalk(rng *splitmix, base string) string {
	path := base
	for range rng.intN(6) + 1 {
		choices, ok := walkChoices(path, base)
		if !ok {
			break
		}
		path += string(filepath.Separator) + choices[rng.intN(len(choices))]
	}
	return path + string(filepath.Separator) + "leaf"
}

// walkChoices are the next components a walk at path may take: ".", ".."
// above base, and every entry but the leaf; false once path does not
// resolve.
func walkChoices(path, base string) ([]string, bool) {
	at, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, false
	}
	entries, err := os.ReadDir(at)
	if err != nil {
		return nil, false
	}
	choices := []string{"."}
	if at != base {
		choices = append(choices, "..")
	}
	for _, e := range entries {
		if e.Name() != "leaf" {
			choices = append(choices, e.Name())
		}
	}
	return choices, true
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// TestCredentialLocationsRefusesUnrepresentablePathsFirst proves a home or a
// relocation value no policy can name refuses typed — a control character —
// before the table is resolved against the filesystem, on every OS: where
// the OS rejects the name itself (Windows), its error at the path must not
// win over this refusal.
func TestCredentialLocationsRefusesUnrepresentablePathsFirst(t *testing.T) {
	base := t.TempDir()
	h := filepath.Join(base, "h")
	for name, env := range map[string][]string{
		"home":        {"HOME=" + h + "\n"},
		"data root":   {"HOME=" + h, "XDG_DATA_HOME=" + filepath.Join(base, "da\nta")},
		"opencode db": {"HOME=" + h, "OPENCODE_DB=" + filepath.Join(base, "d\tb", "oc.db")},
	} {
		_, err := Inputs{ChildEnv: env}.CredentialLocations(filepath.Join(base, "r"), false)
		if err == nil || !strings.Contains(err.Error(), "control character") {
			t.Errorf("%s: CredentialLocations = %v, want the control-character refusal", name, err)
		}
	}
}

// TestCredentialMountsMountALinkAtItsTarget proves a credential location that
// is a link is mounted at the path it resolves to — bwrap refuses a link as
// a mount's destination — once, beside its target named directly.
func TestCredentialMountsMountALinkAtItsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := resolvedTempDir(t)
	target := filepath.Join(base, "target")
	mustMkdirAll(t, target)
	link(t, target, filepath.Join(base, "link"))
	got := CredentialMounts([]string{filepath.Join(base, "link"), target})
	if len(got) != 1 || got[0].Dest != target || !got[0].Dir {
		t.Errorf("mounts = %+v, want one directory mount at %s", got, target)
	}
}
