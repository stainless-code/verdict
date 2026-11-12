package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	sourceDirPrefix = "dir:"
	sourceGitPrefix = "git:"
	refSep          = "@"
	schemeSep       = "://"
	schemeSlash     = ":/"
	invalidSource   = "<invalid source>"
	packDirPerm     = 0o755
	shaHexLen       = 40
	stagingDot      = "."
	revParseArgs    = 3
	revParseIndex   = 1
	maxSourceUser   = 64
	maxHostLen      = 253
	maxHostLabel    = 63
	maxPortLen      = 5
)

// packCloneTimeout bounds the clone, rev-parse, and checkout group so a slow
// remote cannot hold pack add open indefinitely.
const packCloneTimeout = 5 * time.Minute

// gitRef gates the optional @ref before it reaches a git argv.
var gitRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)

// CodeBadPackSource is the refusal for a remote-shaped source the git
// allowlist rejects.
const CodeBadPackSource = "bad-pack-source"

// CodePackGitFailed is the refusal for a git call the pack clone could not
// complete: no network, no such repository or ref, or a refused credential.
const CodePackGitFailed = "pack-git-failed"

// AddRequest wires one `pack add <dir|git-url>[@ref]` run.
type AddRequest struct {
	core.ReadRequest
	Source string
}

// AddData is the pack add --json data profile.
type AddData struct {
	Pack         Entry   `json:"pack"`
	PreviousHash *string `json:"previous_hash"`
	Replaced     bool    `json:"replaced"`
}

// Wire renders the add data in published wire order.
func (d *AddData) Wire() *jq.Obj {
	if d == nil {
		return jq.NewObj("pack", nil, "replaced", false, "previous_hash", nil)
	}
	return jq.NewObj(
		"pack", core.Wire(&d.Pack),
		"replaced", d.Replaced,
		"previous_hash", d.PreviousHash,
	)
}

// AddResponse carries the envelope inputs, the human lines, and any warning a
// successful add collected after the lock became durable.
//
//nolint:govet // the envelope fields read in render order
type AddResponse struct {
	Data     *AddData
	Human    []string
	Warnings []string
	At       string
	Next     string
}

// RunAdd vendors one validated pack under .verdict/packs/<slug>/ and pins it
// in .verdict/verdict.lock. Re-adding a name replaces the tree and reports the
// old -> new hash.
func RunAdd(ctx context.Context, req *AddRequest) (*AddResponse, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: canceledCode, Message: "canceled before pack add completed"}
	}
	src, ref := addSource(ctx, req)
	if ref != nil {
		return nil, ref
	}
	defer src.cleanup()
	root := req.RepoRootPath()
	lock, p, old, ref := loadAdd(root, src)
	if ref != nil {
		return nil, ref
	}
	slug := packSlug(p.Name)
	entry := LockEntry{
		Name: p.Name, Source: src.label, Version: p.Version, Hash: p.Hash,
	}
	entry.Sig = LockSig(&entry)
	next := upsertLock(lock, &entry)
	if _, ref := encodeLock(next); ref != nil {
		return nil, ref
	}
	backup, ref := vendorPack(root, src.dir, slug, p)
	if ref != nil {
		return nil, ref
	}
	warning, ref := commitAdd(root, slug, backup, next)
	if ref != nil {
		return nil, ref
	}
	resp := addResponse(p, &entry, old, slug)
	if warning != "" {
		resp.Warnings = []string{warning}
		req.Warn(warning)
	}
	return resp, nil
}

// commitAdd writes the new lock, restoring the previous tree when the write
// fails, and drops the backup after the lock is durable. A failed drop is a
// warning, not a refusal: the tree and lock are already committed.
func commitAdd(root, slug, backup string, lock *Lock) (string, *core.RefusalError) {
	if ref := writeLock(root, lock); ref != nil {
		if err := undoVendored(root, slug, backup); err != nil {
			return "", cleanupStageFailure(ref, err)
		}
		return "", ref
	}
	if err := dropVendoredBackup(root, backup); err != nil {
		leftover := path.Join(RepoPacksDir, backup)
		return fmt.Sprintf(
			"pack add installed %s/%s, but the previous tree at %s could not be removed: %v — delete it by hand; the loader ignores it",
			RepoPacksDir, slug, leftover, err), nil
	}
	return "", nil
}

// loadAdd validates the source and returns the current lock, the loaded pack,
// and the row a re-add replaces (nil on a first add).
func loadAdd(root string, src *packSource) (*Lock, *Pack, *LockEntry, *core.RefusalError) {
	if sameTree(filepath.Join(root, filepath.FromSlash(RepoPacksDir)), src.dir) {
		return nil, nil, nil, badPack(fmt.Sprintf(
			"pack source %q is inside %s — it is already vendored", displaySource(src.label), RepoPacksDir))
	}
	p, ref := LoadDir(src.dir)
	if ref != nil {
		return nil, nil, nil, ref
	}
	if p == nil {
		return nil, nil, nil, badPack(fmt.Sprintf("pack source %q carries no %s", displaySource(src.label), ManifestFile))
	}
	lock, ref := loadLock(root)
	if ref != nil {
		return nil, nil, nil, ref
	}
	slug := packSlug(p.Name)
	if ref := checkSlugOwner(root, lock, p.Name, slug); ref != nil {
		return nil, nil, nil, ref
	}
	var old *LockEntry
	if existing := lockEntry(lock, p.Name); existing != nil {
		previous := *existing
		old = &previous
	}
	return lock, p, old, nil
}

// checkSlugOwner refuses a first add that would overwrite another pack's
// vendored slug, or a directory no lock row owns.
func checkSlugOwner(root string, lock *Lock, name, slug string) *core.RefusalError {
	if ref := lockSlugConflict(lock, name, slug); ref != nil {
		return ref
	}
	dir := filepath.Join(root, filepath.FromSlash(RepoPacksDir), slug)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return badPack(fmt.Sprintf("inspect %s/%s: %v", RepoPacksDir, slug, err))
	}
	if lockEntry(lock, name) != nil {
		return nil
	}
	existing, ref := LoadDir(dir)
	if ref == nil && existing != nil && existing.Name == name {
		return nil
	}
	return badPack(fmt.Sprintf(
		"%s/%s is not named by %s — remove it or re-add the pack it holds",
		RepoPacksDir, slug, LockFile))
}

// lockSlugConflict refuses a lock row whose name maps to the same directory.
func lockSlugConflict(lock *Lock, name, slug string) *core.RefusalError {
	if lock == nil {
		return nil
	}
	for i := range lock.Packs {
		other := &lock.Packs[i]
		if other.Name != name && packSlug(other.Name) == slug {
			return badPack(fmt.Sprintf(
				"pack %s would vendor over %s at %s/%s — rename one pack",
				name, other.Name, RepoPacksDir, slug))
		}
	}
	return nil
}

// addResponse renders the added row and its human line.
func addResponse(p *Pack, entry *LockEntry, old *LockEntry, slug string) *AddResponse {
	data := &AddData{
		Pack: Entry{
			Name: p.Name, Version: p.Version, Layer: string(LayerRepo),
			Source: path.Join(RepoPacksDir, slug), Hash: p.Hash,
			Lenses: lensIDsOf(p), Rules: ruleIDsOf(p),
		},
	}
	source := displaySource(entry.Source)
	line := fmt.Sprintf("added %s@%s source=%s sha256:%s", p.Name, p.Version, source, p.Hash)
	if old != nil {
		data.Replaced = true
		previous := old.Hash
		data.PreviousHash = &previous
		line = fmt.Sprintf("updated %s@%s source=%s sha256:%s -> sha256:%s",
			p.Name, p.Version, source, previous, p.Hash)
	}
	return &AddResponse{
		Next:  "commit .verdict/verdict.lock with the vendored pack, then run 'verdict pack list'",
		Human: []string{line},
		Data:  data,
	}
}

// lensIDsOf returns one pack's declared lens ids in manifest order.
func lensIDsOf(p *Pack) []string {
	ids := make([]string, len(p.Lenses))
	for i := range p.Lenses {
		ids[i] = p.Lenses[i].ID
	}
	return ids
}

// ruleIDsOf returns one pack's declared rule ids in manifest order.
func ruleIDsOf(p *Pack) []string {
	ids := make([]string, len(p.Rules))
	for i := range p.Rules {
		ids[i] = p.Rules[i].ID
	}
	return ids
}

// packSource is one resolved add source and its cleanup.
type packSource struct {
	cleanup func()
	dir     string
	label   string
}

// addSource resolves an allowlisted git remote into a hermetic clone, or a
// local directory into a pack source. A remote-shaped source the allowlist
// rejects refuses before any git call.
func addSource(ctx context.Context, req *AddRequest) (*packSource, *core.RefusalError) {
	source := strings.TrimSpace(req.Source)
	if src, ok := parseGitSource(source); ok {
		return cloneSource(ctx, req, src.url, src.ref)
	}
	if strings.HasPrefix(source, "-") {
		return nil, badPack(fmt.Sprintf("pack source %q starts with '-'", displaySource(source)))
	}
	if looksLikeGitSource(source) || secretShaped(source) {
		return nil, badPackSource(fmt.Sprintf(
			"pack source %q is not an allowlisted git remote", displaySource(source)))
	}
	dir, err := filepath.Abs(source)
	if err != nil {
		return nil, badPack(fmt.Sprintf("pack source %q cannot be resolved: %v", displaySource(source), err))
	}
	label := sourceDirPrefix + filepath.ToSlash(source)
	if !validLockSource(label) {
		return nil, badPack(fmt.Sprintf("pack source %q cannot be recorded in %s", displaySource(label), LockFile))
	}
	return &packSource{dir: dir, label: label, cleanup: func() {}}, nil
}

// badPackGitFailed builds the failed-git-call refusal.
func badPackGitFailed(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodePackGitFailed, Message: reason}
}

// badPackSource builds the git-source refusal.
func badPackSource(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodeBadPackSource, Message: reason}
}

// gitSource is one allowlisted git remote and its optional @ref.
type gitSource struct {
	url string
	ref string
}

// parseGitSource accepts exactly the published git sources: https://host/path
// with no userinfo, ssh://user@host/path, and user@host:path, each with an
// optional @ref. The scheme is matched case-insensitively and stored
// lower-case. A query or fragment refuses: a source is recorded in the
// committed lock, so nothing after the path may carry a token.
func parseGitSource(s string) (gitSource, bool) {
	if strings.ContainsAny(s, "?#") {
		return gitSource{}, false
	}
	if base, ref, ok := splitSourceRef(s); ok {
		url, ok := parseSourceURL(base)
		if !ok {
			return gitSource{}, false
		}
		return gitSource{url: url, ref: ref}, true
	}
	url, ok := parseSourceURL(s)
	if !ok {
		return gitSource{}, false
	}
	return gitSource{url: url}, true
}

// splitSourceRef cuts the optional @ref at the first @ after a URL authority,
// or at the last @ of a possible scp source. The suffix must match the ref
// grammar, so a bare git@host:path stays one source.
func splitSourceRef(s string) (string, string, bool) {
	if schemeEnd := strings.Index(s, schemeSep); schemeEnd > 0 {
		return splitURLRef(s, schemeEnd)
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || !gitRef.MatchString(s[at+1:]) {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}

// splitURLRef cuts a URL source at the first @ after its authority.
func splitURLRef(s string, schemeEnd int) (string, string, bool) {
	rest := schemeEnd + len(schemeSep)
	slash := strings.IndexByte(s[rest:], '/')
	if slash < 0 {
		return s, "", false
	}
	rest += slash
	at := strings.IndexByte(s[rest:], '@')
	if at < 0 {
		return s, "", false
	}
	at += rest
	if !gitRef.MatchString(s[at+1:]) {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}

// parseSourceURL parses one allowlisted git URL without its @ref.
func parseSourceURL(s string) (string, bool) {
	if scheme, rest, ok := sourceScheme(s); ok {
		switch strings.ToLower(scheme) {
		case "https":
			return parseHTTPSource(rest)
		case "ssh":
			return parseSSHSource(rest)
		}
		return "", false
	}
	return parseSCPSource(s)
}

// sourceScheme splits one scheme:// prefix.
func sourceScheme(s string) (string, string, bool) {
	i := strings.Index(s, schemeSep)
	if i <= 0 {
		return "", "", false
	}
	scheme := s[:i]
	if !validScheme(scheme) {
		return "", "", false
	}
	return scheme, s[i+len(schemeSep):], true
}

// parseHTTPSource accepts https://HOST[:PORT]/PATH with no userinfo.
func parseHTTPSource(rest string) (string, bool) {
	authority, rel, ok := splitAuthorityPath(rest)
	if !ok || strings.Contains(authority, refSep) || !validHostPort(authority) || !validPath(rel) {
		return "", false
	}
	return "https://" + authority + "/" + rel, true
}

// parseSSHSource accepts ssh://USER@HOST[:PORT]/PATH.
func parseSSHSource(rest string) (string, bool) {
	authority, rel, ok := splitAuthorityPath(rest)
	if !ok {
		return "", false
	}
	user, hostPort, ok := splitSourceUser(authority)
	if !ok || !validSourceUser(user) || !validHostPort(hostPort) || !validPath(rel) {
		return "", false
	}
	return "ssh://" + user + "@" + hostPort + "/" + rel, true
}

// splitAuthorityPath cuts a URL authority at its first slash; both sides must
// be non-empty.
func splitAuthorityPath(rest string) (string, string, bool) {
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

// splitSourceUser splits one user@host authority at its single @.
func splitSourceUser(authority string) (string, string, bool) {
	at := strings.IndexByte(authority, '@')
	if at <= 0 || at == len(authority)-1 || strings.Contains(authority[at+1:], refSep) {
		return "", "", false
	}
	return authority[:at], authority[at+1:], true
}

// parseSCPSource accepts the scp form USER@HOST:PATH.
func parseSCPSource(s string) (string, bool) {
	if strings.Contains(s, schemeSep) {
		return "", false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "", false
	}
	user, rest := s[:at], s[at+1:]
	host, rel, ok := splitSCPHost(rest)
	if !ok || !validSourceUser(user) || !validHost(host) || !validPath(rel) {
		return "", false
	}
	return user + "@" + host + ":" + rel, true
}

// splitSCPHost cuts one scp destination at the colon after its host, or after
// a bracketed IPv6 literal.
func splitSCPHost(rest string) (string, string, bool) {
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ':' {
			return "", "", false
		}
		return rest[:end+1], rest[end+2:], true
	}
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return "", "", false
	}
	return rest[:colon], rest[colon+1:], true
}

// validHostPort validates HOST or HOST:PORT, with a bracketed IPv6 host.
func validHostPort(s string) bool {
	if strings.HasPrefix(s, "[") {
		return validBracketedHostPort(s)
	}
	host, port, hasPort := strings.Cut(s, ":")
	return (!hasPort || validPort(port)) && validHost(host)
}

// validBracketedHostPort validates [ipv6] with its optional :port.
func validBracketedHostPort(s string) bool {
	end := strings.IndexByte(s, ']')
	if end < 0 || !validHost(s[:end+1]) {
		return false
	}
	rest := s[end+1:]
	return rest == "" || (strings.HasPrefix(rest, ":") && validPort(rest[1:]))
}

// validHost accepts a DNS name, an IPv4 address, or a bracketed IPv6 literal.
func validHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		return strings.HasSuffix(host, "]") && validIPv6(host[1:len(host)-1])
	}
	if host == "" || len(host) > maxHostLen {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !validHostLabel(label) {
			return false
		}
	}
	return true
}

// validHostLabel accepts one DNS label: 1..63 bytes, alphanumeric at both
// ends, hyphen inside.
func validHostLabel(label string) bool {
	if label == "" || len(label) > maxHostLabel {
		return false
	}
	for i := range len(label) {
		c := label[i]
		if !isAlnum(c) && (c != '-' || i == 0 || i == len(label)-1) {
			return false
		}
	}
	return true
}

// validIPv6 accepts the inside of a bracketed IPv6 literal: hex digits,
// colons, and dots, with at least one colon.
func validIPv6(inner string) bool {
	if inner == "" || !strings.ContainsRune(inner, ':') {
		return false
	}
	for i := range len(inner) {
		c := inner[i]
		if !isHex(c) && c != ':' && c != '.' {
			return false
		}
	}
	return true
}

// validSourceUser accepts the bare ssh user grammar: 1..64 of [A-Za-z0-9._-].
func validSourceUser(user string) bool {
	if user == "" || len(user) > maxSourceUser {
		return false
	}
	for i := range len(user) {
		c := user[i]
		if !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validPath accepts a source path with no whitespace, control character, @,
// or leading '-'.
func validPath(rel string) bool {
	if rel == "" || rel[0] == '-' {
		return false
	}
	for i := range len(rel) {
		c := rel[i]
		if c <= ' ' || c == controlDEL || c == '@' {
			return false
		}
	}
	return true
}

// validPort accepts 1..5 decimal digits.
func validPort(port string) bool {
	if port == "" || len(port) > maxPortLen {
		return false
	}
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// looksLikeGitSource reports whether s asks to be a git remote. Anything that
// does refuses when the allowlist rejects it, instead of falling through to a
// directory label.
func looksLikeGitSource(s string) bool {
	if strings.Contains(s, schemeSep) || schemeColonSlash(s) {
		return true
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || strings.ContainsAny(s[:at], `/\`) {
		return false
	}
	return strings.IndexByte(s[at+1:], ':') > 0
}

// schemeColonSlash matches a scheme and a single slash, the mangled URL shape
// that would otherwise read as a directory path. Two-letter-plus schemes keep
// a Windows drive prefix ("C:/packs") a path.
func schemeColonSlash(s string) bool {
	i := strings.Index(s, schemeSlash)
	return i >= 2 && validScheme(s[:i])
}

// secretShaped reports whether a label carries a URL or userinfo shape that
// must never be echoed, including inside a dir: label.
func secretShaped(s string) bool {
	return strings.Contains(s, schemeSep) || schemeColonSlash(s) ||
		(strings.Contains(s, refSep) && strings.ContainsRune(s, ':'))
}

// displaySource renders one source for output. It prints only a source the
// grammar accepts, canonicalising a git URL; anything else becomes a fixed
// placeholder so no refusal can echo a credential.
func displaySource(raw string) string {
	switch {
	case raw == "builtin" || strings.HasPrefix(raw, RepoPacksDir+"/"):
		return raw
	case strings.HasPrefix(raw, sourceDirPrefix):
		return displayDirSource(raw)
	case strings.HasPrefix(raw, sourceGitPrefix):
		return displayGitSource(raw)
	}
	if g, ok := parseGitSource(raw); ok {
		return g.url
	}
	if validDirSource(raw) && !secretShaped(raw) {
		return sourceDirPrefix + raw
	}
	return invalidSource
}

// displayDirSource renders one dir: label, masking a payload that looks like a
// URL.
func displayDirSource(raw string) string {
	dir, _ := strings.CutPrefix(raw, sourceDirPrefix)
	if validDirSource(dir) && !secretShaped(dir) {
		return raw
	}
	return invalidSource
}

// displayGitSource renders one git: lock label, masking anything that is not
// the canonical URL plus its pinned sha.
func displayGitSource(raw string) string {
	rest, _ := strings.CutPrefix(raw, sourceGitPrefix)
	at := strings.LastIndex(rest, refSep)
	if at <= 0 || !hex40(rest[at+1:]) {
		return invalidSource
	}
	g, ok := parseGitSource(rest[:at])
	if !ok || g.ref != "" || raw != sourceGitPrefix+g.url+rest[at:] {
		return invalidSource
	}
	return raw
}

// validScheme reports whether s is an RFC 3986 URL scheme.
func validScheme(s string) bool {
	if s == "" || !isSchemeLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isSchemeByte(s[i]) {
			return false
		}
	}
	return true
}

func isSchemeLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSchemeByte(c byte) bool {
	return isSchemeLetter(c) || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// gitFatalExit is git's generic fatal exit status: a clone that reaches the
// remote and fails there (not found, auth, network) exits with it.
const gitFatalExit = 128

// gitFailure renders a failed git call without git's own text: that text can
// carry the source URL, its query, or the ref, each operator-supplied and
// possibly a credential. The wording is fixed per phase and exit code.
func gitFailure(sub string, exit int) string {
	switch {
	case sub == "clone" && exit == gitFatalExit:
		return "the remote refused the clone (the repository or ref was not found, " +
			"authentication failed, or the network is down) — check the source, the ref, and your access"
	case sub == "clone":
		return fmt.Sprintf("the clone exited %d — check the source and your access", exit)
	case sub == "rev-parse":
		return "the clone carries no resolvable HEAD commit"
	case sub == "checkout":
		return "the resolved commit could not be checked out"
	}
	return fmt.Sprintf("exit %d", exit)
}

// cloneSource fetches one git URL hermetically into a temp directory and
// checks out the resolved HEAD commit before loading the pack.
func cloneSource(
	ctx context.Context, req *AddRequest, url, refName string,
) (*packSource, *core.RefusalError) {
	cmdr := req.Commander
	if cmdr == nil {
		return nil, &core.RefusalError{Code: "internal", Message: "pack add git source needs a command runner"}
	}
	tmp, err := os.MkdirTemp("", "verdict-pack-")
	if err != nil {
		return nil, badPack(fmt.Sprintf("could not create a pack work dir: %v", err))
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if ref := prepareGitEnv(tmp); ref != nil {
		cleanup()
		return nil, ref
	}
	env := gitEnv(runtimeEnv(req), tmp)
	clone := filepath.Join(tmp, "repo")
	gctx, cancel := context.WithTimeout(ctx, packCloneTimeout)
	defer cancel()
	if ref := gitClone(gctx, cmdr, env, tmp, url, refName, clone); ref != nil {
		cleanup()
		return nil, ref
	}
	sha, ref := resolveClone(gctx, cmdr, env, clone, tmp, url)
	if ref != nil {
		cleanup()
		return nil, ref
	}
	return &packSource{
		dir: clone, label: sourceGitPrefix + url + refSep + sha, cleanup: cleanup,
	}, nil
}

// resolveClone pins the clone to its resolved HEAD and checks the working tree
// before the loader walks it.
func resolveClone(
	ctx context.Context, cmdr core.Commander, env []string, clone, tmp, url string,
) (string, *core.RefusalError) {
	sha, ref := runGitPack(ctx, cmdr, env, clone, tmp, "rev-parse", "HEAD")
	if ref != nil {
		return "", ref
	}
	if !hex40(sha) {
		return "", badPack(fmt.Sprintf("git remote %q resolved HEAD to %q, not a 40-hex commit",
			displaySource(url), sha))
	}
	if ref := runGitPackOK(ctx, cmdr, env, clone, tmp, "checkout", "--detach", sha); ref != nil {
		return "", ref
	}
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		return "", badPack(fmt.Sprintf("clean the pack clone metadata: %v", err))
	}
	return sha, checkCloneBounds(clone)
}

// prepareGitEnv creates the scrubbed HOME/XDG tree and the hooks directory a
// Windows clone uses in place of /dev/null.
func prepareGitEnv(tmp string) *core.RefusalError {
	for _, dir := range []string{"home", "cache", "config", "data", "state", "hooks"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), packDirPerm); err != nil {
			return badPack(fmt.Sprintf("could not create the pack work dir: %v", err))
		}
	}
	return nil
}

// gitClone runs the hermetic clone with the resolved hooks path.
func gitClone(
	ctx context.Context, cmdr core.Commander, env []string, tmp, url, refName, clone string,
) *core.RefusalError {
	args := []string{"--depth=1", "--no-tags", "--no-recurse-submodules"}
	if refName != "" {
		args = append(args, "--branch", refName)
	}
	args = append(args, url, clone)
	_, ref := runGitPack(ctx, cmdr, env, "", tmp, "clone", args...)
	return ref
}

// gitPrefix is the one argv prefix every pack-add git call carries, so no
// inherited hook can run.
func gitPrefix(tmp string) []string {
	return []string{
		"git", "-c", "core.hooksPath=" + hooksPath(tmp),
		"-c", "protocol.file.allow=never",
	}
}

// runtimeEnv renders the injected environment as the KEY=value pairs a clone
// inherits before scrubbing; PATH must survive so git can reach ssh.
func runtimeEnv(req *AddRequest) []string {
	if req.Runtime == nil {
		return nil
	}
	out := make([]string, 0, len(req.Env))
	for key, value := range req.Env {
		out = append(out, key+"="+value)
	}
	return out
}

// hooksPath is /dev/null everywhere git reads one; on Windows an empty temp
// directory is the equivalent.
func hooksPath(tmp string) string {
	if filepath.Separator == '\\' {
		return filepath.Join(tmp, "hooks")
	}
	return "/dev/null"
}

// gitEnv builds the scrubbed clone environment: the caller's entries minus
// every HOME/XDG/GIT override, then the hermetic values a clone needs.
func gitEnv(base []string, tmp string) []string {
	out := make([]string, 0, len(base))
	for _, pair := range base {
		key, _, _ := strings.Cut(pair, "=")
		if scrubGitEnv(key) {
			continue
		}
		out = append(out, pair)
	}
	out = append(out,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"HOME="+filepath.Join(tmp, "home"),
		"XDG_CACHE_HOME="+filepath.Join(tmp, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(tmp, "config"),
		"XDG_DATA_HOME="+filepath.Join(tmp, "data"),
		"XDG_STATE_HOME="+filepath.Join(tmp, "state"),
	)
	slices.Sort(out)
	return out
}

// scrubGitEnv reports whether one environment key could redirect a clone away
// from the temporary tree.
func scrubGitEnv(key string) bool {
	switch key {
	case "HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
		"GIT_TERMINAL_PROMPT", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_ASKPASS",
		"GIT_TEMPLATE_DIR", "GIT_SSH_COMMAND", "GIT_EXEC_PATH", "GIT_PROXY_COMMAND",
		"SSH_ASKPASS":
		return true
	}
	return strings.HasPrefix(key, "GIT_CONFIG_")
}

// runGitPack runs one git command with the shared hermetic prefix and returns
// its trimmed stdout.
func runGitPack(
	ctx context.Context, cmdr core.Commander, env []string, dir, tmp, sub string, args ...string,
) (string, *core.RefusalError) {
	argv := append(gitPrefix(tmp), sub)
	argv = append(argv, args...)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: env, Dir: dir, OutputCap: MaxPackBytes})
	if err != nil {
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", &core.RefusalError{Code: "missing-tool", Message: "git is not on PATH"}
		}
		return "", badPackGitFailed(fmt.Sprintf(
			"git %s could not run — check that git works in this shell, then re-run", sub))
	}
	if res.TimedOut {
		return "", badPackGitFailed(fmt.Sprintf(
			"git %s ran out of the five-minute pack budget — check the network and re-run", sub))
	}
	if res.ExitCode != 0 {
		return "", badPackGitFailed(fmt.Sprintf("git %s failed: %s", sub, gitFailure(sub, res.ExitCode)))
	}
	if res.StdoutTruncated {
		return "", tooLarge("git " + sub + " output is over its cap")
	}
	return strings.TrimSpace(res.Stdout), nil
}

// runGitPackOK is runGitPack for a call whose stdout carries nothing.
func runGitPackOK(
	ctx context.Context, cmdr core.Commander, env []string, dir, tmp, sub string, args ...string,
) *core.RefusalError {
	_, ref := runGitPack(ctx, cmdr, env, dir, tmp, sub, args...)
	return ref
}

// checkCloneBounds refuses a clone that already exceeds the pack caps before
// the loader walks it: the clone is on disk by then, but a hostile tree should
// not reach the parse path at all.
func checkCloneBounds(dir string) *core.RefusalError {
	count := 0
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, walkErr error) error {
		return chargeCloneEntry(entry, walkErr, &count, &total)
	})
	if err == nil {
		return nil
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return badPack(fmt.Sprintf("inspect the git clone: %v", err))
}

// chargeCloneEntry spends one walked clone entry against the pack caps.
func chargeCloneEntry(entry fs.DirEntry, walkErr error, count *int, total *int64) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return jq.Sealed("inspect "+entry.Name(), err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	*count++
	*total += info.Size()
	if *count > MaxFiles {
		return tooLarge(fmt.Sprintf("the git clone carries more than %d files", MaxFiles))
	}
	if *total > MaxPackBytes {
		return tooLarge(fmt.Sprintf("the git clone is over the %d-byte total cap", MaxPackBytes))
	}
	return nil
}

// vendorPack stages the loaded pack beside its destination, proves the staged
// tree hashes like the source, then swaps it in. A failed re-add leaves the
// previous tree and lock row untouched.
func vendorPack(root, srcDir, slug string, p *Pack) (string, *core.RefusalError) {
	src, err := os.OpenRoot(srcDir)
	if err != nil {
		return "", badPack(fmt.Sprintf("open pack source %s: %v", srcDir, err))
	}
	defer func() { _ = src.Close() }()
	dest, packsDir, ref := openPacksRoot(root)
	if ref != nil {
		return "", ref
	}
	defer func() { _ = dest.Close() }()
	if ref := sweepStaleVendored(packsDir, slug); ref != nil {
		return "", ref
	}
	stage, ref := stageVendored(src, packsDir, slug, p)
	if ref != nil {
		return "", ref
	}
	backup, swapRef := swapVendored(dest, packsDir, slug, stage)
	if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
		if swapRef != nil {
			return "", cleanupStageFailure(swapRef, cleanupErr)
		}
		return "", badPack(fmt.Sprintf("remove the %s staging tree: %v", RepoPacksDir, cleanupErr))
	}
	return backup, swapRef
}

// sweepStaleVendored removes a crashed re-add's staging and backup siblings
// for slug before a new stage is created.
func sweepStaleVendored(packsDir, slug string) *core.RefusalError {
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		return badPack(fmt.Sprintf("read %s: %v", RepoPacksDir, err))
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, stagingDot+slug+".tmp-") && !strings.HasPrefix(name, stagingDot+slug+".old-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(packsDir, name)); err != nil {
			return badPack(fmt.Sprintf("remove the stale %s/%s: %v", RepoPacksDir, name, err))
		}
	}
	return nil
}

// stageVendored writes and verifies the new tree in a temp sibling inside the
// pack root. Every failure closes the staging root before removing the tree:
// Windows refuses to delete a directory with an open handle.
func stageVendored(src *os.Root, packsDir, slug string, p *Pack) (string, *core.RefusalError) {
	stage, err := os.MkdirTemp(packsDir, stagingDot+slug+".tmp-")
	if err != nil {
		return "", badPack(fmt.Sprintf("create a staging directory under %s: %v", RepoPacksDir, err))
	}
	stageRoot, err := os.OpenRoot(stage)
	if err != nil {
		return "", cleanupStageFailure(badPack(
			fmt.Sprintf("open the pack staging directory: %v", err)), os.RemoveAll(stage))
	}
	if ref := writeVendored(stageRoot, src, rootSelf, p); ref != nil {
		return "", cleanupStageFailure(ref, closeStage(stageRoot, stage))
	}
	if ref := verifyVendored(stage, rootSelf, p); ref != nil {
		return "", cleanupStageFailure(ref, closeStage(stageRoot, stage))
	}
	if err := stageRoot.Close(); err != nil {
		return "", cleanupStageFailure(badPack(
			fmt.Sprintf("close the pack staging directory: %v", err)), os.RemoveAll(stage))
	}
	return stage, nil
}

// closeStage closes a staging root and removes its tree, joining both errors
// so a caller can report either failure.
func closeStage(root *os.Root, stage string) error {
	return errors.Join( //nolint:wrapcheck // wrapOp names each cause; Join only combines them
		wrapOp("close the staging root", root.Close()),
		wrapOp("remove the staging tree", os.RemoveAll(stage)),
	)
}

// wrapOp names an operation and wraps its error; a nil error stays nil.
func wrapOp(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err) //nolint:wrapcheck // %w preserves the cause; wrapcheck reads the constructor
}

// cleanupStageFailure keeps the original refusal and names a cleanup failure
// that would otherwise be swallowed.
func cleanupStageFailure(ref *core.RefusalError, err error) *core.RefusalError {
	if err == nil {
		return ref
	}
	return &core.RefusalError{
		Code:    ref.Code,
		Message: fmt.Sprintf("%s (staging cleanup failed: %v)", ref.Message, err),
	}
}

// swapVendored moves the verified stage into slug, keeping a backup of any
// previous tree until RunAdd commits the new lock.
func swapVendored(dest *os.Root, packsDir, slug, stage string) (string, *core.RefusalError) {
	backup, ref := backupVendored(dest, packsDir, slug)
	if ref != nil {
		return "", ref
	}
	if err := dest.Rename(filepath.Base(stage), slug); err != nil {
		if backup != "" {
			_ = dest.Rename(backup, slug) //nolint:errcheck // best-effort restore while the refusal is formed
		}
		return "", badPack(fmt.Sprintf("install %s/%s: %v", RepoPacksDir, slug, err))
	}
	return backup, nil
}

// undoVendored restores the tree a failed lock write replaced, or removes the
// tree a first add installed.
func undoVendored(root, slug, backup string) error {
	dest, err := os.OpenRoot(filepath.Join(root, filepath.FromSlash(RepoPacksDir)))
	if err != nil {
		return wrapOp("open the pack root", err)
	}
	defer func() { _ = dest.Close() }()
	if backup == "" {
		return wrapOp("remove the new pack tree", dest.RemoveAll(slug))
	}
	if err := dest.RemoveAll(slug); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return wrapOp("remove the new pack tree", err)
	}
	return wrapOp("restore the previous pack tree", dest.Rename(backup, slug))
}

// dropVendoredBackup removes the previous tree after the lock is durable.
func dropVendoredBackup(root, backup string) error {
	if backup == "" {
		return nil
	}
	dest, err := os.OpenRoot(filepath.Join(root, filepath.FromSlash(RepoPacksDir)))
	if err != nil {
		return wrapOp("open the pack root", err)
	}
	defer func() { _ = dest.Close() }()
	return wrapOp("remove the previous pack tree", dest.RemoveAll(backup))
}

// backupVendored renames any previous tree aside and returns its name, or ""
// when the slug is new.
func backupVendored(dest *os.Root, packsDir, slug string) (string, *core.RefusalError) {
	if _, err := dest.Lstat(slug); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", badPack(fmt.Sprintf("inspect %s/%s: %v", RepoPacksDir, slug, err))
	}
	holder, err := os.MkdirTemp(packsDir, stagingDot+slug+".old-")
	if err != nil {
		return "", badPack(fmt.Sprintf("create a backup directory under %s: %v", RepoPacksDir, err))
	}
	backup := filepath.Base(holder)
	if err := os.Remove(holder); err != nil {
		return "", badPack(fmt.Sprintf("prepare the %s/%s backup: %v", RepoPacksDir, slug, err))
	}
	if err := dest.Rename(slug, backup); err != nil {
		return "", badPack(fmt.Sprintf("move the previous %s/%s aside: %v", RepoPacksDir, slug, err))
	}
	return backup, nil
}

// verifyVendored reloads the copied tree and proves it hashes like the source.
func verifyVendored(packsDir, slug string, p *Pack) *core.RefusalError {
	check, ref := LoadAt(packsDir, slug)
	if ref != nil {
		return ref
	}
	if check == nil || check.Hash != p.Hash {
		return badPack(fmt.Sprintf("vendored %s/%s does not reproduce the loaded tree", RepoPacksDir, slug))
	}
	return nil
}

// openPacksRoot creates the repo pack root when absent and opens it as an
// [os.Root]; a symlink or non-directory refuses.
func openPacksRoot(root string) (*os.Root, string, *core.RefusalError) {
	dir := filepath.Join(root, filepath.FromSlash(RepoPacksDir))
	if err := os.MkdirAll(dir, packDirPerm); err != nil {
		return nil, "", badPack(fmt.Sprintf("create %s: %v", RepoPacksDir, err))
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, "", badPack(fmt.Sprintf("inspect %s: %v", RepoPacksDir, err))
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, "", unsafePath(dir)
	}
	handle, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", badPack(fmt.Sprintf("open %s: %v", RepoPacksDir, err))
	}
	return handle, dir, nil
}

// writeVendored writes the manifest and every loaded body under slug.
func writeVendored(dest, src *os.Root, slug string, p *Pack) *core.RefusalError {
	manifest, err := src.ReadFile(ManifestFile)
	if err != nil {
		return badPack(fmt.Sprintf("read %s from the pack source: %v", ManifestFile, err))
	}
	if err := dest.MkdirAll(slug, packDirPerm); err != nil {
		return badPack(fmt.Sprintf("create %s/%s: %v", RepoPacksDir, slug, err))
	}
	if ref := writeVendoredFile(dest, slug, ManifestFile, manifest); ref != nil {
		return ref
	}
	if ref := writeVendoredLenses(dest, slug, p.Lenses); ref != nil {
		return ref
	}
	if ref := writeVendoredRules(dest, slug, p.Rules); ref != nil {
		return ref
	}
	return writeVendoredExtras(dest, slug, p.files)
}

// writeVendoredLenses creates lenses/ and writes every loaded lens body.
func writeVendoredLenses(dest *os.Root, slug string, lenses []Lens) *core.RefusalError {
	if ref := writeVendoredDir(dest, slug, LensesDir); ref != nil {
		return ref
	}
	for i := range lenses {
		rel := path.Join(LensesDir, lenses[i].ID+mdExt)
		if ref := writeVendoredFile(dest, slug, rel, lenses[i].Body); ref != nil {
			return ref
		}
	}
	return nil
}

// writeVendoredRules creates rules/ and writes every loaded rule body.
func writeVendoredRules(dest *os.Root, slug string, rules []Rule) *core.RefusalError {
	if ref := writeVendoredDir(dest, slug, RulesDir); ref != nil {
		return ref
	}
	for i := range rules {
		rel := path.Join(RulesDir, rules[i].ID+mdExt)
		if ref := writeVendoredFile(dest, slug, rel, rules[i].Body); ref != nil {
			return ref
		}
	}
	return nil
}

// writeVendoredExtras copies the reserved examples/ and evals/ trees.
func writeVendoredExtras(dest *os.Root, slug string, files []packFile) *core.RefusalError {
	for i := range files {
		if ref := writeVendoredFile(dest, slug, files[i].path, files[i].body); ref != nil {
			return ref
		}
	}
	return nil
}

// writeVendoredDir creates one fixed layout directory under slug.
func writeVendoredDir(dest *os.Root, slug, dir string) *core.RefusalError {
	if err := dest.MkdirAll(filepath.Join(slug, dir), packDirPerm); err != nil {
		return badPack(fmt.Sprintf("create %s/%s/%s: %v", RepoPacksDir, slug, dir, err))
	}
	return nil
}

// writeVendoredFile writes one slash-relative pack file atomically inside the
// destination root.
func writeVendoredFile(dest *os.Root, slug, rel string, body []byte) *core.RefusalError {
	name := filepath.Join(slug, filepath.FromSlash(rel))
	if err := dest.MkdirAll(filepath.Dir(name), packDirPerm); err != nil {
		return badPack(fmt.Sprintf("create %s/%s/%s: %v", RepoPacksDir, slug, rel, err))
	}
	if err := atomic.WriteFileIn(dest, name, body); err != nil {
		return badPack(fmt.Sprintf("write %s/%s/%s: %v", RepoPacksDir, slug, rel, err))
	}
	return nil
}

// sameTree reports whether two paths name the same directory, following
// symlinks on both sides when they exist.
func sameTree(dir, target string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	if rel, relErr := filepath.Rel(absDir, absPath); relErr == nil && insideRel(rel, relErr) {
		return true
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realDir, realPath)
	return insideRel(rel, err)
}
