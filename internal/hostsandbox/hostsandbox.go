// Package hostsandbox holds the OS-sandbox building blocks verify's command
// sandbox and run's review sandbox share: the credential table and the homes
// it resolves under, the Linux mounts that hide it, the Seatbelt rule
// renderers and path spellings macOS matches, and the private scratch tree a
// sandboxed child writes in. Each verb composes its own policy from them, so
// the table and the quoting rules exist once.
package hostsandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/jq"
)

// The sandbox runners and the bwrap mount options both policies use.
const (
	BwrapTool       = "bwrap"
	SandboxExecTool = "sandbox-exec"
	SandboxExecPath = "/usr/bin/sandbox-exec"
	Tmpfs           = "--tmpfs"
	Bind            = "--bind"
	ROBind          = "--ro-bind"
)

const (
	// PrivateDirPrefix names the per-run temporary directory a sandboxed
	// child gets as TMPDIR/TMP/TEMP; the policy allows it and denies the
	// shared host temp roots around it.
	PrivateDirPrefix = "verdict-sandbox-"

	// PrivateDirMode restores owner access to every directory under the
	// private tree before removal, so a child that dropped its own
	// permissions cannot strand the tree on the host.
	PrivateDirMode = 0o700

	// GoCacheDir is the Go build cache the child is pointed at inside its
	// private directory: the policy denies writes everywhere else, so the
	// default caches under HOME fail.
	GoCacheDir = "go-build"
	// CacheDir is the XDG cache the child is pointed at, for the same reason.
	CacheDir = "cache"
)

const (
	// SeatbeltServiceDenials closes the operations `(allow default)` grants
	// that reach outside the run without a file write: LaunchServices
	// (lsopen, which alone stops `open` and `osascript ... launch`),
	// AppleEvents (appleevent-send), launchd job submission (job-creation),
	// distributed notifications, and signals to any process the command did
	// not start. The signal rule is the only guard against the command
	// killing the operator's own processes, since Seatbelt has no PID
	// namespace.
	SeatbeltServiceDenials = `(deny lsopen)(deny appleevent-send)(deny job-creation)` +
		`(deny distributed-notification-post)(deny signal)(allow signal (target same-sandbox))`

	// SeatbeltDeviceWrites re-allows the device writes a shell needs after a
	// blanket write denial: the bit bucket, the controlling terminal, and the
	// descriptor aliases (/dev/stdout and /dev/stderr resolve under /dev/fd).
	SeatbeltDeviceWrites = `(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/fd/"))`
)

const (
	// seatbeltFirstPrintable and seatbeltDelete bound the characters a
	// Seatbelt string literal can represent; anything below or DEL refuses.
	seatbeltFirstPrintable = ' '
	seatbeltDelete         = 0x7f

	// envSeparator splits one environment entry into its name and value.
	envSeparator = "="

	msgHomeUnset = "HOME is unset in the child environment, so the credential paths under it cannot be hidden"
)

// CredentialPaths is the one credential table both platforms hide, relative
// to every home the account answers to: directories behind an empty tmpfs,
// files behind a read-only /dev/null bind on Linux, and denied reads and
// writes under Seatbelt on macOS. Only entries that exist are mounted on
// Linux; macOS denies the whole table. New entries go at the end so the
// mount order of the existing ones never moves.
var CredentialPaths = []string{
	".aws",
	".ssh",
	filepath.Join(".config", "gh"),
	".docker",
	".kube",
	filepath.Join(".config", "gcloud"),
	".azure",
	".gnupg",
	".netrc",
	".git-credentials",
	".npmrc",
	".pypirc",
	".terraform.d",
	filepath.Join(".cargo", "credentials.toml"),
	filepath.Join(".cargo", "credentials"),
	".vault-token",
	filepath.Join("Library", "Keychains"),
	filepath.Join(".config", "glab-cli"),
	filepath.Join(".codex", "auth.json"),
	filepath.Join(".local", "share", "opencode", "auth.json"),
	filepath.Join(".pi", "agent", "auth.json"),
	filepath.Join(".claude", ".credentials.json"),
	filepath.Join(".config", "github-copilot"),
	filepath.Join(".gemini", "oauth_creds.json"),
	filepath.FromSlash(".config/git/credentials"),
	filepath.Join(".local", "share", "opencode", "mcp-auth.json"),
	// The X11 cookie: without it the display's socket, which a shared
	// network namespace leaves reachable on Linux, refuses the connection.
	".Xauthority",
	// Password-manager and hardware-key ssh agents keep their sockets under
	// HOME, outside every hidden temp root.
	".1password",
	".bitwarden-ssh-agent.sock",
	filepath.Join("Library", "Group Containers", "2BUA8C4S2C.com.1password"),
	filepath.Join("Library", "Containers", "com.maxgoedjen.Secretive.SecretAgent"),
	// opencode's whole data directory: its database keeps the logins in a
	// credential table, the same tokens auth.json held, and SQLite makes and
	// removes the database's journal and write-ahead log as connections come
	// and go — a file that does not exist when bwrap starts gets no mount of
	// its own — under a name a release channel may change.
	filepath.Join(".local", "share", "opencode"),
}

// credentialVar is one environment variable that relocates a credential
// store away from its home-relative default: the path it names (joined with
// suffix), or every entry of a path list.
type credentialVar struct {
	name   string
	suffix string
	list   bool
}

// xdgConfigHome relocates the XDG config directory several stores live in.
const xdgConfigHome = "XDG_CONFIG_HOME"

// xdgDataHome relocates the XDG data directory opencode keeps its logins
// in, and envOpenCodeDB opencode's database alone.
const (
	xdgDataHome   = "XDG_DATA_HOME"
	envOpenCodeDB = "OPENCODE_DB"
)

// credentialEnv is the relocation half of the table, read from both the
// child environment and the verdict process environment, so a store moved by
// the operator's shell is hidden even when the child never sees the
// variable. Relative values are skipped: they resolve against the reviewed
// root the command may already read.
var credentialEnv = []credentialVar{
	{name: xdgConfigHome, suffix: "gh"},
	{name: "GH_CONFIG_DIR"},
	{name: "AWS_SHARED_CREDENTIALS_FILE"},
	{name: "AWS_CONFIG_FILE"},
	{name: "DOCKER_CONFIG"},
	{name: "KUBECONFIG", list: true},
	{name: "CLOUDSDK_CONFIG"},
	{name: xdgConfigHome, suffix: "glab-cli"},
	{name: "GLAB_CONFIG_DIR"},
	{name: "CODEX_HOME", suffix: "auth.json"},
	{name: xdgDataHome, suffix: filepath.Join("opencode", "auth.json")},
	{name: "PI_CODING_AGENT_DIR", suffix: "auth.json"},
	{name: "CLAUDE_CONFIG_DIR", suffix: ".credentials.json"},
	{name: xdgConfigHome, suffix: "github-copilot"},
	{name: xdgConfigHome, suffix: filepath.FromSlash("git/credentials")},
	{name: xdgDataHome, suffix: filepath.Join("opencode", "mcp-auth.json")},
	{name: "XAUTHORITY"},
	{name: xdgDataHome, suffix: "opencode"},
}

// Inputs is what a policy resolves the credential table against: the
// environment the child runs with, the verdict process environment, and the
// account home from the passwd database. A child HOME that differs from the
// account home still leaves the account's real credential stores on disk, so
// both are hidden.
type Inputs struct {
	AccountHome string
	ChildEnv    []string
	HostEnv     []string
}

// Homes lists every home the credential table resolves under, the child's
// first. An unset child HOME is the one refusal: its paths cannot be named.
func (in Inputs) Homes() ([]string, error) {
	home, ok := Home(in.ChildEnv)
	if !ok {
		return nil, jq.NewErr(msgHomeUnset)
	}
	if !filepath.IsAbs(home) {
		return nil, jq.NewErrf(
			"HOME %q in the child environment is not an absolute path, so the credential paths under it cannot be hidden", home)
	}
	homes := []string{home}
	if host := EnvLookup(in.HostEnv, "HOME"); filepath.IsAbs(host) {
		homes = append(homes, host)
	}
	if filepath.IsAbs(in.AccountHome) {
		homes = append(homes, in.AccountHome)
	}
	return CleanUnique(homes), nil
}

// CredentialLocations resolves the shared table: every home-relative entry
// under every home, then every relocation variable from the child and host
// environments. A root inside a credential location refuses, since hiding
// the location would hide the code under review and allowing the root would
// expose the credential. expandHomes adds each home's realpath first
// (Seatbelt matches resolved vnodes; bwrap follows the literal path itself).
func (in Inputs) CredentialLocations(root string, expandHomes bool) ([]string, error) {
	homes, err := in.Homes()
	if err != nil {
		return nil, err
	}
	if expandHomes {
		homes = ExpandRealpaths(homes)
	}
	locations := homeCredentials(homes)
	for _, env := range [][]string{in.ChildEnv, in.HostEnv} {
		moved, err := envLocations(env, homes)
		if err != nil {
			return nil, err
		}
		locations = append(locations, moved...)
	}
	locations = CleanUnique(locations)
	if location, ok := Enclosing(locations, root); ok {
		return nil, jq.NewErrf(
			"the reviewed root %s is inside the credential path %s; refusing to expose or hide it", root, location)
	}
	return locations, coversAHome(locations, homes)
}

// envLocations is the credential stores one environment's relocation
// variables move, opencode's database directories among them.
func envLocations(env, homes []string) ([]string, error) {
	var locations []string
	for _, v := range credentialEnv {
		locations = append(locations, v.paths(env)...)
	}
	data, err := ResolvedOpenCodeDataDirs(env, homes)
	if err != nil {
		return nil, err
	}
	dirs, err := OpenCodeDBDirs(env, homes)
	return slices.Concat(locations, data, dirs), err
}

// ResolvedOpenCodeDataDirs is every opencode data directory as written and
// where the kernel lands for it (ResolvePath): a data directory that is a
// link — dangling included, which no mount or realpath spelling would follow
// — is hidden, and made on Linux, at its target.
func ResolvedOpenCodeDataDirs(env, homes []string) ([]string, error) {
	raw := OpenCodeDataDirs(env, homes)
	out := slices.Clone(raw)
	for _, dir := range raw {
		resolved, err := ResolvePath(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// OpenCodeDBDirs is the directory OPENCODE_DB puts opencode's database in,
// with its journal and log beside it: an absolute value's own directory, a
// relative one resolved against every opencode data directory, as opencode
// resolves it, and for a value that is a link the target's directory too;
// ":memory:" names no file.
func OpenCodeDBDirs(env, homes []string) ([]string, error) {
	value := EnvLookup(env, envOpenCodeDB)
	switch {
	case value == memoryDB:
		return nil, nil
	case filepath.IsAbs(value):
		return dbDirs([]string{filepath.Clean(value)})
	case value == "":
		value = defaultOpenCodeDB
	}
	dirs := OpenCodeDataDirs(env, homes)
	paths := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, value))
	}
	return dbDirs(paths)
}

// defaultOpenCodeDB is the database opencode opens with OPENCODE_DB unset
// (a release-channel build names its own beside it).
const defaultOpenCodeDB = "opencode.db"

// OpenCodeDataDirs is opencode's data directory under every data root the
// environment and homes name: $XDG_DATA_HOME/opencode when absolute, and
// <home>/.local/share/opencode.
func OpenCodeDataDirs(env, homes []string) []string {
	var dirs []string
	if data := EnvLookup(env, xdgDataHome); filepath.IsAbs(data) {
		dirs = append(dirs, filepath.Join(data, openCodeDir))
	}
	for _, home := range homes {
		dirs = append(dirs, filepath.Join(home, ".local", "share", openCodeDir))
	}
	return dirs
}

// openCodeDir is opencode's directory under a data root.
const openCodeDir = "opencode"

// dbDirs is the directory of each database path as written and the one
// the database's file actually lives in (ResolveDBDir): SQLite resolves a
// link to the database and keeps its journal and log beside the target,
// in a directory the path's own does not cover.
func dbDirs(paths []string) ([]string, error) {
	var dirs []string
	for _, path := range paths {
		resolved, err := ResolveDBDir(path)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, filepath.Dir(path), resolved)
	}
	return dirs, nil
}

// maxLinkHops bounds the links a database path is followed through, as the
// kernel bounds a path walk (ELOOP).
const maxLinkHops = 40

// ResolveDBDir is the directory a database path's file lives in, or will be
// created in (ResolvePath's directory).
func ResolveDBDir(path string) (string, error) {
	resolved, err := ResolvePath(path)
	return filepath.Dir(resolved), err
}

// ResolvePath is where the kernel lands for path, existing or not: each
// hop's directory resolved through EvalSymlinks (as deep as it exists, the
// missing rest appended), a relative link target joined against that
// resolved directory, followed hop by hop until the leaf exists as other
// than a link or is missing — the path a file or directory created there
// will have. A chain longer than maxLinkHops, or a link that cannot be read,
// refuses: where the path leads cannot be pinned down.
func ResolvePath(path string) (string, error) {
	cur := filepath.Clean(path)
	for range maxLinkHops + 1 {
		leaf := filepath.Join(ResolveExisting(filepath.Dir(cur)), filepath.Base(cur))
		info, err := os.Lstat(leaf)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return leaf, nil
		case err != nil:
			return "", jq.NewErrf("the path %s cannot be inspected (%v); fix it or the directories above it", leaf, err)
		case info.Mode()&fs.ModeSymlink == 0:
			return leaf, nil
		}
		target, err := os.Readlink(leaf)
		if err != nil {
			return "", jq.NewErrf("the link %s cannot be read (%v); remove it or point it at a real path", leaf, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(leaf), target)
		}
		cur = filepath.Clean(target)
	}
	return "", jq.NewErrf("the path %s runs through more than %d links, so the review sandbox cannot pin "+
		"down where it leads; point it at the real path directly", path, maxLinkHops)
}

// ResolveExisting is dir with its deepest existing ancestor resolved through
// every link and the missing rest appended as written: a component that
// does not exist cannot be a link yet.
func ResolveExisting(dir string) string {
	var rest []string
	for at := filepath.Clean(dir); ; at = filepath.Dir(at) {
		if resolved, err := filepath.EvalSymlinks(at); err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...)
		}
		if filepath.Dir(at) == at {
			return filepath.Clean(dir)
		}
		rest = append([]string{filepath.Base(at)}, rest...)
	}
}

// memoryDB is the OPENCODE_DB value that keeps the database in memory.
const memoryDB = ":memory:"

// coversAHome refuses a credential location that is the filesystem root, a
// home, or an ancestor of one, under every spelling either resolves to:
// hiding it would hide every home below it (an OPENCODE_DB directly in HOME,
// an opencode data directory linked to HOME).
func coversAHome(locations, homes []string) error {
	resolvedHomes := ExpandRealpaths(homes)
	for _, location := range locations {
		_, root := FilesystemRoot(location)
		covers := slices.ContainsFunc(ExpandRealpaths([]string{location}), func(spelled string) bool {
			return slices.ContainsFunc(resolvedHomes, func(home string) bool { return Within(home, spelled) })
		})
		if root || covers {
			return jq.NewErrf("the credential path %s is the filesystem root, a home, or above one; move what "+
				"it holds into a directory of its own (OPENCODE_DB=<dir>/opencode.db)", location)
		}
	}
	return nil
}

// homeCredentials joins the home-relative table under every home.
func homeCredentials(homes []string) []string {
	locations := make([]string, 0, len(homes)*len(CredentialPaths)+len(credentialEnv))
	for _, home := range homes {
		for _, rel := range CredentialPaths {
			locations = append(locations, filepath.Join(home, rel))
		}
	}
	return locations
}

// Enclosing returns the first location that is path or one of its
// ancestors.
func Enclosing(locations []string, path string) (string, bool) {
	for _, location := range locations {
		if Within(path, location) {
			return location, true
		}
	}
	return "", false
}

// Within reports whether path is dir or below it, both cleaned.
func Within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// paths returns the absolute credential paths one environment's value of the
// variable names; an unset, empty, or relative value names nothing.
func (v credentialVar) paths(env []string) []string {
	value := EnvLookup(env, v.name)
	values := []string{value}
	if v.list {
		values = filepath.SplitList(value)
	}
	var paths []string
	for _, path := range values {
		if filepath.IsAbs(path) {
			paths = append(paths, filepath.Join(path, v.suffix))
		}
	}
	return paths
}

// EnvLookup reads one name from an environment snapshot; the last entry
// wins, as it does for a spawned child.
func EnvLookup(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if key, v, ok := strings.Cut(kv, envSeparator); ok && key == name {
			value = v
		}
	}
	return value
}

// CleanUnique cleans every path and drops empties and duplicates while
// keeping first-seen order.
func CleanUnique(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// AccountHome is the passwd-database home of the current uid, or empty when
// the lookup fails; HOME alone can point anywhere.
func AccountHome() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// Home reads the home the child will see: the environment snapshot when it
// carries one, else the process home an inherited child environment
// resolves to. An explicit snapshot without HOME cannot be hidden.
func Home(env []string) (string, bool) {
	// os/exec keeps the last of duplicate entries, so the child's HOME is the
	// last one; hiding credentials under an earlier one would miss them.
	home, found := "", false
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, envSeparator); ok && name == "HOME" {
			home, found = value, true
		}
	}
	if found {
		return home, home != ""
	}
	if len(env) == 0 {
		home, err := os.UserHomeDir()
		return home, err == nil && home != ""
	}
	return "", false
}

// Mount is one path a bwrap wrapper replaces; Dir mounts an empty tmpfs, a
// file binds /dev/null read-only over it.
type Mount struct {
	Dest string
	Dir  bool
}

// Argv is the mount's bwrap arguments.
func (m Mount) Argv() []string {
	if m.Dir {
		return []string{Tmpfs, m.Dest}
	}
	return []string{ROBind, "/dev/null", m.Dest}
}

// CredentialMounts resolves the locations against the host, so a missing
// entry adds no mount and a file never gets a directory's tmpfs.
func CredentialMounts(locations []string) []Mount {
	mounts := make([]Mount, 0, len(locations))
	for _, dest := range locations {
		info, err := os.Stat(dest)
		if err != nil {
			continue
		}
		mounts = append(mounts, Mount{Dest: dest, Dir: info.IsDir()})
	}
	return mounts
}

// FilesystemRoot reports the spelling under which path is, or resolves to,
// the filesystem root: a root that is a symlink to `/` would otherwise make
// the whole host writable once the mechanism resolves it.
func FilesystemRoot(path string) (string, bool) {
	spellings := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		spellings = append(spellings, resolved)
	}
	for _, p := range spellings {
		if filepath.Dir(p) == p {
			return p, true
		}
	}
	return "", false
}

// ExpandRealpaths cleans every path, adds its resolved realpath, and
// removes duplicates while keeping first-seen order. Seatbelt matches the
// resolved vnode path, so the /var -> /private/var spelling of a temp root
// needs both forms. A path that does not exist yet (a git directory a `.git`
// file names, a credential file not created) resolves through its deepest
// existing ancestor, so a rule still matches the spelling the kernel will
// see when the command tries to create it. A path that is itself a symlink
// also gets its own node's spelling (its resolved parent plus its name), not
// only its target's: Seatbelt checks an unlink or rename against the link's
// canonical path, so without it the link could be replaced.
func ExpandRealpaths(paths []string) []string {
	expanded := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		expanded = append(expanded, path)
		if resolved, ok := Realpath(path); ok {
			expanded = append(expanded, resolved)
		}
		if parent, ok := Realpath(filepath.Dir(path)); ok {
			expanded = append(expanded, filepath.Join(parent, filepath.Base(path)))
		}
	}
	return CleanUnique(expanded)
}

// Realpath resolves path, or the deepest existing ancestor of it with the
// missing tail appended.
func Realpath(path string) (string, bool) {
	tail := ""
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, tail), true
		}
		if filepath.Dir(current) == current {
			return "", false
		}
		tail = filepath.Join(filepath.Base(current), tail)
	}
}

// SeatbeltLiteral quotes one path for a Seatbelt profile. Backslashes and
// quotes are escaped; a control character (including a newline) or invalid
// UTF-8 refuses, because the profile must never be built from a path it
// cannot represent.
func SeatbeltLiteral(path string) (string, error) {
	if !utf8.ValidString(path) {
		return "", jq.NewErrf("the sandbox path %q is not valid UTF-8, which the Seatbelt profile cannot represent", path)
	}
	if strings.ContainsFunc(path, func(r rune) bool {
		return r < seatbeltFirstPrintable || r == seatbeltDelete
	}) {
		return "", jq.NewErrf(
			"the sandbox path %q contains a control character the Seatbelt profile cannot represent", path)
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path)
	return `"` + quoted + `"`, nil
}

// SeatbeltRegexQuote escapes every regex metacharacter in a path so a
// Seatbelt regex matches only that path.
func SeatbeltRegexQuote(path string) string {
	var b strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`\.^$|()[]{}*+?`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ReadRules renders one read-only allow per path; a blanket write denial
// before it still covers the path.
func ReadRules(b *strings.Builder, paths []string) error {
	return subpathRules(b, "(allow file-read* (subpath %s))", paths)
}

// ReadDenials renders one read denial per path, leaving writes to the rules
// before it.
func ReadDenials(b *strings.Builder, paths []string) error {
	return subpathRules(b, "(deny file-read* (subpath %s))", paths)
}

// WriteDenials renders one write denial per path, leaving reads to the rules
// before it.
func WriteDenials(b *strings.Builder, paths []string) error {
	return subpathRules(b, "(deny file-write* (subpath %s))", paths)
}

// WriteRules renders one read-and-write subpath rule per path with the given
// action, allow or deny.
func WriteRules(b *strings.Builder, action string, paths []string) error {
	return subpathRules(b, "("+action+" file-read* file-write* (subpath %s))", paths)
}

// subpathRules renders format once per path, refusing a path whose control
// characters have no safe Seatbelt literal.
func subpathRules(b *strings.Builder, format string, paths []string) error {
	for _, path := range paths {
		literal, err := SeatbeltLiteral(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, format, literal)
	}
	return nil
}

// AncestorMetadata renders one rule allowing metadata reads (stat, never a
// listing or a file's contents) of every proper ancestor of the allowed
// paths, matched literally.
func AncestorMetadata(b *strings.Builder, paths []string) error {
	ancestors := Ancestors(paths)
	if len(ancestors) == 0 {
		return nil
	}
	b.WriteString("(allow file-read-metadata")
	for _, dir := range ancestors {
		literal, err := SeatbeltLiteral(dir)
		if err != nil {
			return err
		}
		b.WriteString(" (literal " + literal + ")")
	}
	b.WriteString(")")
	return nil
}

// Ancestors lists every proper ancestor of the paths, nearest first, without
// duplicates.
func Ancestors(paths []string) []string {
	var ancestors []string
	for _, path := range paths {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			ancestors = append(ancestors, dir)
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return CleanUnique(ancestors)
}

// PrivateEnv is the child-environment override that keeps scratch and
// caches inside the one writable private tree.
func PrivateEnv(private string) []string {
	return []string{
		"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private, "GOTMPDIR=" + private,
		"GOCACHE=" + filepath.Join(private, GoCacheDir),
		"XDG_CACHE_HOME=" + filepath.Join(private, CacheDir),
	}
}

// OverrideEnv applies env overrides to a base snapshot, replacing the base
// value for each overridden name and keeping every other entry.
func OverrideEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	overridden := make(map[string]bool, len(overrides))
	for _, kv := range overrides {
		name, _, _ := strings.Cut(kv, envSeparator)
		overridden[name] = true
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if name, _, ok := strings.Cut(kv, envSeparator); ok && overridden[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, overrides...)
}

// MakePrivateDir creates a private scratch tree under base (the system temp
// directory when empty) with its cache directory inside.
func MakePrivateDir(base string) (string, error) {
	private, err := os.MkdirTemp(base, PrivateDirPrefix)
	if err != nil {
		return "", jq.Sealed("create the private temporary directory", err)
	}
	if err := os.Mkdir(filepath.Join(private, CacheDir), PrivateDirMode); err != nil {
		return "", jq.Sealed("create the private cache directory", errors.Join(err, RemovePrivateDir(private)))
	}
	return private, nil
}

// RemovePrivateDir restores owner access to every directory in the private
// tree, then removes it, so a child that ran `chmod 0` on its own scratch
// cannot strand the tree. Symlinks are never followed: the walk reports them
// as non-directories, and only directories are re-permissioned.
func RemovePrivateDir(private string) error {
	var restoreErrs []error
	walkErr := filepath.WalkDir(private, func(path string, d fs.DirEntry, err error) error {
		restoreErrs = append(restoreErrs, restoreOwnerDir(path, d, err))
		return nil
	})
	if err := os.RemoveAll(private); err != nil {
		return jq.Sealed("remove "+private, errors.Join(append([]error{err, walkErr}, restoreErrs...)...))
	}
	return nil
}

// restoreOwnerDir gives one walked directory back its owner access; a walk
// error or a non-directory (a symlink included) is left as found.
func restoreOwnerDir(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if !d.IsDir() {
		return nil
	}
	return os.Chmod(path, PrivateDirMode) //nolint:wrapcheck // joined and sealed by RemovePrivateDir
}
