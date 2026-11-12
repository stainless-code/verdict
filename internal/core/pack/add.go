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
	userinfoSep     = ":"
	schemeSep       = "://"
	httpsPrefix     = "https://"
	sshPrefix       = "ssh://"
	packDirPerm     = 0o755
	shaHexLen       = 40
	gitEnvFixed     = 11
	revParseArgs    = 3
	revParseIndex   = 1
)

// packCloneTimeout bounds the clone, rev-parse, and checkout group so a slow
// remote cannot hold pack add open indefinitely.
const packCloneTimeout = 5 * time.Minute

// gitRef gates the optional @ref before it reaches a git argv.
var gitRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)

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

// AddResponse carries the envelope inputs and the human lines.
//
//nolint:govet // the envelope fields read in render order
type AddResponse struct {
	Data  *AddData
	Human []string
	At    string
	Next  string
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
	if ref := vendorPack(root, src.dir, slug, p); ref != nil {
		return nil, ref
	}
	entry := LockEntry{
		Name: p.Name, Source: src.label, Version: p.Version, Hash: p.Hash,
	}
	entry.Sig = LockSig(&entry)
	if ref := writeLock(root, upsertLock(lock, &entry)); ref != nil {
		return nil, ref
	}
	return addResponse(p, &entry, old, slug), nil
}

// loadAdd validates the source and returns the current lock, the loaded pack,
// and the row a re-add replaces (nil on a first add).
func loadAdd(root string, src *packSource) (*Lock, *Pack, *LockEntry, *core.RefusalError) {
	if sameTree(filepath.Join(root, filepath.FromSlash(RepoPacksDir)), src.dir) {
		return nil, nil, nil, badPack(fmt.Sprintf(
			"pack source %q is inside %s — it is already vendored", redactURL(src.label), RepoPacksDir))
	}
	p, ref := LoadDir(src.dir)
	if ref != nil {
		return nil, nil, nil, ref
	}
	if p == nil {
		return nil, nil, nil, badPack(fmt.Sprintf("pack source %q carries no %s", redactURL(src.label), ManifestFile))
	}
	lock, ref := loadLock(root)
	if ref != nil {
		return nil, nil, nil, ref
	}
	var old *LockEntry
	if existing := lockEntry(lock, p.Name); existing != nil {
		previous := *existing
		old = &previous
	}
	return lock, p, old, nil
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
	source := redactURL(entry.Source)
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

// addSource resolves a directory or a hermetic git clone.
func addSource(ctx context.Context, req *AddRequest) (*packSource, *core.RefusalError) {
	source := strings.TrimSpace(req.Source)
	sourceURL, _, _ := refSplit(source)
	if ref := userinfoRefusal(sourceURL); ref != nil {
		return nil, ref
	}
	if url, refName, ok := splitGitSource(source); ok {
		return cloneSource(ctx, req, url, refName)
	}
	if strings.HasPrefix(source, "-") {
		return nil, badPack(fmt.Sprintf("pack source %q starts with '-'", redactURL(source)))
	}
	dir, err := filepath.Abs(source)
	if err != nil {
		return nil, badPack(fmt.Sprintf("pack source %q cannot be resolved: %v", redactURL(source), err))
	}
	return &packSource{dir: dir, label: sourceDirPrefix + filepath.ToSlash(source), cleanup: func() {}}, nil
}

// splitGitSource recognizes the published https and ssh forms and the
// optional @ref suffix.
func splitGitSource(source string) (string, string, bool) {
	url, refName, hasRef := refSplit(source)
	if hasRef {
		if refName == "" || !gitRef.MatchString(refName) {
			return "", "", false
		}
	}
	if !gitURL(url) || strings.HasPrefix(url, "-") || userinfoRefusal(url) != nil {
		return "", "", false
	}
	return url, refName, true
}

// userinfoRefusal reports the refusal a source's userinfo earns: http and
// https sources carry none at all, and ssh and scp sources carry a bare user
// only.
func userinfoRefusal(raw string) *core.RefusalError {
	auth, ok := parseSourceAuthority(raw)
	if !ok || auth.at < 0 {
		return nil
	}
	scheme := strings.ToLower(auth.scheme)
	if scheme == "http" || scheme == "https" {
		return badPack(fmt.Sprintf(
			"pack source %q carries userinfo — %s URLs must carry no userinfo; "+
				"use ssh (ssh://git@host/path or git@host:path) or a public https URL",
			redactURL(raw), scheme))
	}
	if strings.Contains(auth.userinfo, userinfoSep) {
		return badPack(fmt.Sprintf(
			"pack source %q carries a userinfo password — use a bare ssh user, or a public https URL",
			redactURL(raw)))
	}
	return nil
}

// redactURL masks each source's whole userinfo, keeping a dir:/git: wrapper
// and a git lock's @<sha> suffix intact.
func redactURL(raw string) string {
	if dir, ok := strings.CutPrefix(raw, sourceDirPrefix); ok {
		return sourceDirPrefix + redactURL(dir)
	}
	if rest, ok := strings.CutPrefix(raw, sourceGitPrefix); ok {
		if at := strings.LastIndex(rest, refSep); at > 0 && hex40(rest[at+1:]) {
			return sourceGitPrefix + redactSourceURL(rest[:at]) + rest[at:]
		}
		return sourceGitPrefix + redactSourceURL(rest)
	}
	return redactSourceURL(raw)
}

// redactSourceURL masks the userinfo one URL or scp source carries.
func redactSourceURL(raw string) string {
	auth, ok := parseSourceAuthority(raw)
	if !ok || auth.at < 0 {
		return raw
	}
	return raw[:auth.start] + "***" + raw[auth.at:]
}

// sourceAuthority is one source's authority split at the last @ before the
// host, the delimiter git itself uses.
type sourceAuthority struct {
	scheme   string
	userinfo string
	at       int
	start    int
	end      int
}

// parseSourceAuthority splits a URL authority or an scp user@host at git's
// last @. A scheme-less string counts only when a host:path colon follows the
// @, so a local path with an @ stays a path.
func parseSourceAuthority(raw string) (sourceAuthority, bool) {
	if scheme, rest, ok := strings.Cut(raw, schemeSep); ok && validScheme(scheme) {
		start := len(raw) - len(rest)
		end := len(raw)
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			end = start + slash
		}
		if start >= end {
			return sourceAuthority{}, false
		}
		return splitLastAt(raw, sourceAuthority{scheme: scheme, start: start, end: end})
	}
	at := strings.LastIndex(raw, refSep)
	if at <= 0 || strings.Contains(raw[:at], "/") || !strings.Contains(raw[at+1:], userinfoSep) {
		return sourceAuthority{}, false
	}
	return splitLastAt(raw, sourceAuthority{start: 0, end: len(raw)})
}

// splitLastAt fills the userinfo fields from the last @ inside bounds.
func splitLastAt(raw string, auth sourceAuthority) (sourceAuthority, bool) {
	authorityText := raw[auth.start:auth.end]
	at := strings.LastIndexByte(authorityText, '@')
	if at < 0 {
		auth.at = -1
		return auth, true
	}
	auth.at = auth.start + at
	auth.userinfo = authorityText[:at]
	return auth, true
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

// refSplit cuts the @ref suffix at the last @ outside the source's authority.
func refSplit(source string) (string, string, bool) {
	auth, ok := parseSourceAuthority(source)
	if ok && auth.scheme != "" {
		start := auth.end
		if at := strings.Index(source[start:], refSep); at >= 0 {
			split := start + at
			return source[:split], source[split+1:], true
		}
		return source, "", false
	}
	at := strings.LastIndex(source, refSep)
	if at <= 0 {
		return source, "", false
	}
	if !hasSCPForm(source[:at]) {
		return source, "", false
	}
	return source[:at], source[at+1:], true
}

// hasSCPForm reports whether raw is an scp user@host:path source.
func hasSCPForm(raw string) bool {
	auth, ok := parseSourceAuthority(raw)
	return ok && auth.scheme == ""
}

// redactURLText masks the userinfo of every scheme:// URL in text, so a
// failed clone cannot echo a credential git was handed.
func redactURLText(text string) string {
	var out strings.Builder
	last := 0
	for from := 0; ; {
		i := strings.Index(text[from:], schemeSep)
		if i < 0 {
			break
		}
		i += from
		start := i
		for start > 0 && isSchemeByte(text[start-1]) {
			start--
		}
		authStart := i + len(schemeSep)
		end := authStart
		for end < len(text) && !urlTextStop(text[end]) {
			end++
		}
		at := strings.LastIndexByte(text[authStart:end], '@')
		if at < 0 || at == 0 {
			from = end
			continue
		}
		at += authStart
		out.WriteString(text[last:start])
		out.WriteString(text[start:authStart])
		out.WriteString("***")
		last = at
		from = end
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// urlTextStop bounds a URL in a message: git quotes the URL, and a path,
// query, or fragment starts at the first of these.
func urlTextStop(c byte) bool {
	switch c {
	case '/', '?', '#', '\'', '"', '<', '>', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// gitURL reports whether url is an https or ssh git remote.
func gitURL(url string) bool {
	if rest, ok := strings.CutPrefix(url, httpsPrefix); ok {
		return rest != ""
	}
	if rest, ok := strings.CutPrefix(url, sshPrefix); ok {
		return rest != ""
	}
	at, colon := strings.Index(url, refSep), strings.Index(url, userinfoSep)
	return at > 0 && colon > at+1 && !strings.Contains(url, schemeSep)
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
		dir: clone, label: sourceGitPrefix + redactURL(url) + refSep + sha, cleanup: cleanup,
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
			redactURL(url), sha))
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
	out := make([]string, 0, len(base)+gitEnvFixed)
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
		return "", badPack(fmt.Sprintf("git %s failed: %s", sub, redactURLText(jq.Sealed(res.Stderr, err).Error())))
	}
	if res.ExitCode != 0 {
		return "", badPack(fmt.Sprintf("git %s failed: %s", sub, redactURLText(strings.TrimSpace(res.Stderr))))
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
func vendorPack(root, srcDir, slug string, p *Pack) *core.RefusalError {
	src, err := os.OpenRoot(srcDir)
	if err != nil {
		return badPack(fmt.Sprintf("open pack source %s: %v", srcDir, err))
	}
	defer func() { _ = src.Close() }()
	dest, packsDir, ref := openPacksRoot(root)
	if ref != nil {
		return ref
	}
	defer func() { _ = dest.Close() }()
	stage, ref := stageVendored(src, packsDir, slug, p)
	if ref != nil {
		return ref
	}
	defer func() { _ = os.RemoveAll(stage) }()
	return swapVendored(dest, packsDir, slug, stage)
}

// stageVendored writes and verifies the new tree in a temp sibling inside the
// pack root.
func stageVendored(src *os.Root, packsDir, slug string, p *Pack) (string, *core.RefusalError) {
	stage, err := os.MkdirTemp(packsDir, "."+slug+".tmp-")
	if err != nil {
		return "", badPack(fmt.Sprintf("create a staging directory under %s: %v", RepoPacksDir, err))
	}
	stageRoot, err := os.OpenRoot(stage)
	if err != nil {
		_ = os.RemoveAll(stage)
		return "", badPack(fmt.Sprintf("open the pack staging directory: %v", err))
	}
	defer func() { _ = stageRoot.Close() }()
	if ref := writeVendored(stageRoot, src, rootSelf, p); ref != nil {
		_ = os.RemoveAll(stage)
		return "", ref
	}
	if ref := verifyVendored(stage, rootSelf, p); ref != nil {
		_ = os.RemoveAll(stage)
		return "", ref
	}
	if err := stageRoot.Close(); err != nil {
		_ = os.RemoveAll(stage)
		return "", badPack(fmt.Sprintf("close the pack staging directory: %v", err))
	}
	return stage, nil
}

// swapVendored moves the verified stage into slug, keeping a backup of any
// previous tree until the new one is in place.
func swapVendored(dest *os.Root, packsDir, slug, stage string) *core.RefusalError {
	backup, ref := backupVendored(dest, packsDir, slug)
	if ref != nil {
		return ref
	}
	if err := dest.Rename(filepath.Base(stage), slug); err != nil {
		if backup != "" {
			_ = dest.Rename(backup, slug) //nolint:errcheck // best-effort restore while the refusal is formed
		}
		return badPack(fmt.Sprintf("install %s/%s: %v", RepoPacksDir, slug, err))
	}
	if backup == "" {
		return nil
	}
	if err := dest.RemoveAll(backup); err != nil {
		return badPack(fmt.Sprintf("remove the previous %s/%s: %v", RepoPacksDir, slug, err))
	}
	return nil
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
	holder, err := os.MkdirTemp(packsDir, "."+slug+".old-")
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
