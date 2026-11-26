package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/gitsource"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	sourceDirPrefix = "dir:"
	sourceGitPrefix = "git:"
	refSep          = "@"
	invalidSource   = "<invalid source>"
	packDirPerm     = 0o755
	shaHexLen       = 40
	stagingDot      = "."
	revParseArgs    = 3
	revParseIndex   = 1
)

// packCloneTimeout bounds the clone, rev-parse, and checkout group so a slow
// remote cannot hold pack add open indefinitely.
const packCloneTimeout = 5 * time.Minute

// CodeBadPackSource is the refusal for a remote-shaped source the git
// allowlist rejects.
const CodeBadPackSource = "bad-pack-source"

// CodePackGitFailed is the refusal for a git call the pack clone could not
// complete: no network, no such repository or ref, or a refused credential.
const CodePackGitFailed = "pack-git-failed"

// CodePackStagingStale is the refusal for a crashed re-add's staging or
// backup sibling the sweep cannot remove.
const CodePackStagingStale = "pack-staging-stale"

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
		return cloneSource(ctx, req, src.url, src.ref, refByName, rootSelf)
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
	return &core.RefusalError{Code: CodePackGitFailed, Message: core.Msgf("%s", reason)}
}

// badPackSource builds the git-source refusal.
func badPackSource(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodeBadPackSource, Message: core.Msgf("%s", reason)}
}

// gitSource is one allowlisted git remote and its optional @ref, in the shape
// the pack layer reads: the canonical URL a lock row records plus the @ref the
// source carried.
type gitSource struct {
	url string
	ref string
}

// parseGitSource parses one source through the shared git-remote allowlist.
// The grammar itself lives in internal/gitsource, so pack add, pack sync, and
// the base-owned org pin cannot drift from the config parser.
func parseGitSource(s string) (gitSource, bool) {
	src, ok := gitsource.Parse(s)
	if !ok {
		return gitSource{}, false
	}
	return gitSource{url: src.URL, ref: src.Ref}, true
}

// looksLikeGitSource reports whether a source asks to be a git remote, so a
// rejected remote shape refuses instead of falling through to a directory
// label.
func looksLikeGitSource(s string) bool { return gitsource.LooksLike(s) }

// secretShaped reports whether a label carries a URL or userinfo shape that
// must never be echoed, including inside a dir: label.
func secretShaped(s string) bool { return gitsource.SecretShaped(s) }

// displaySource renders one source for output. It prints only a source the
// grammar accepts, canonicalising a git URL; anything else becomes a fixed
// placeholder so no refusal can echo a credential.
func displaySource(raw string) string {
	switch {
	case raw == "builtin" || strings.HasPrefix(raw, "builtin:") || strings.HasPrefix(raw, RepoPacksDir+"/"):
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

const checkoutSub = "checkout"

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
	case sub == checkoutSub:
		return "the resolved commit could not be checked out"
	}
	return fmt.Sprintf("exit %d", exit)
}

// refMode says how a clone resolves the requested ref. An ordinary `pack add`
// source keeps `git clone --branch`, which resolves a branch before a tag of
// the same name and is what an operator expects from a mutable source ref. An
// org pin is immutable, so it is fetched by its full Git ref instead.
type refMode int

const (
	// refByName clones with `git clone --branch <ref>`.
	refByName refMode = iota
	// refByGitRef fetches the exact Git ref a pin names: refs/tags/<tag> for a
	// semver tag, the object id for a 40-hex commit.
	refByGitRef
)

// cloneSource fetches one git URL hermetically into a temp directory and
// checks out the resolved HEAD commit before loading the pack. bound is the
// pin path the caller vendors out of that clone: "." caps the whole tree,
// a subdirectory caps only the pack it will actually load.
func cloneSource(
	ctx context.Context, req *AddRequest, url, refName string, mode refMode, bound string,
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
	clone := cloneDir(tmp)
	gctx, cancel := context.WithTimeout(ctx, packCloneTimeout)
	defer cancel()
	if ref := gitClone(gctx, cmdr, env, tmp, url, refName, bound, mode); ref != nil {
		cleanup()
		return nil, ref
	}
	sha, ref := resolveClone(gctx, cmdr, env, clone, url, bound)
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
	ctx context.Context, cmdr core.Commander, env []string, clone, url, bound string,
) (string, *core.RefusalError) {
	sha, ref := runGitPack(ctx, cmdr, env, clone, "rev-parse", "HEAD")
	if ref != nil {
		return "", ref
	}
	if !hex40(sha) {
		return "", badPack(fmt.Sprintf("git remote %q resolved HEAD to %q, not a 40-hex commit",
			displaySource(url), sha))
	}
	if ref := runGitPackOK(ctx, cmdr, env, clone, checkoutSub, "--detach", sha); ref != nil {
		return "", ref
	}
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		return "", badPack(fmt.Sprintf("clean the pack clone metadata: %v", err))
	}
	return sha, checkCloneBounds(clone, bound)
}

// prepareGitEnv creates the scrubbed HOME/XDG tree and the hooks directory a
// Windows clone uses in place of /dev/null.
func prepareGitEnv(tmp string) *core.RefusalError {
	for _, dir := range []string{"home", "cache", "config", "data", "state"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), packDirPerm); err != nil {
			return badPack(fmt.Sprintf("could not create the pack work dir: %v", err))
		}
	}
	return nil
}

// gitClone runs the hermetic, hook-free clone. A pinned
// source — an org pin's semver tag or SHA — and a 40-hex commit, which is not a
// branch or tag name `--branch` could take, go through the fetch path.
func gitClone(
	ctx context.Context, cmdr core.Commander, env []string, tmp, url, refName, bound string, mode refMode,
) *core.RefusalError {
	clone := cloneDir(tmp)
	if mode == refByGitRef || hex40(refName) {
		return gitClonePinned(ctx, cmdr, env, url, refName, bound, clone)
	}
	args := []string{"--depth=1", "--no-tags", "--no-recurse-submodules"}
	if refName != "" {
		args = append(args, "--branch", refName)
	}
	args = append(args, url, clone)
	_, ref := runGitPack(ctx, cmdr, env, "", "clone", args...)
	return ref
}

// gitClonePinned fetches one immutable ref by its exact Git ref: a hermetic
// init, remote registration, a shallow fetch of that one ref, an optional
// sparse checkout of the pin's own subtree, and a detached checkout of
// FETCH_HEAD.
//
// The full ref matters: `git clone --branch v1.2.3` resolves refs/heads/v1.2.3
// before refs/tags/v1.2.3, so a branch named after the pin hijacks it. A tag
// pin is fetched as refs/tags/<tag> and cannot.
func gitClonePinned(
	ctx context.Context, cmdr core.Commander, env []string, url, ref, bound, clone string,
) *core.RefusalError {
	if _, got := runGitPack(ctx, cmdr, env, "", "init", clone); got != nil {
		return got
	}
	if got := runGitPackOK(ctx, cmdr, env, clone, "remote", "add", "origin", url); got != nil {
		return got
	}
	if _, got := runGitPack(ctx, cmdr, env, clone, "fetch",
		"--depth=1", "--no-tags", "--no-recurse-submodules", "origin", pinRefspec(ref)); got != nil {
		return got
	}
	if got := sparseCheckoutBound(ctx, cmdr, env, clone, bound); got != nil {
		return got
	}
	return runGitPackOK(ctx, cmdr, env, clone, checkoutSub, "--detach", "FETCH_HEAD")
}

// cloneDir is the one clone directory under the pack work dir.
func cloneDir(tmp string) string { return filepath.Join(tmp, "repo") }

// tagsPrefix is the Git ref namespace a tag pin resolves under.
const tagsPrefix = "refs/tags/"

// pinRefspec renders the exact Git ref a pinned fetch asks for: a 40-hex commit
// by its object id, and the pin's semver tag under refs/tags/ so a branch of
// the same name can never answer for it.
func pinRefspec(ref string) string {
	if hex40(ref) {
		return ref
	}
	return tagsPrefix + ref
}

// sparseCheckoutBound narrows the working tree to the pin's own subtree before
// the checkout materializes it, so a large central repository's unrelated packs
// never land on disk. A root pin, and an ordinary `pack add`, check out the
// whole tree. Cone mode needs git 2.26; every other step here needs less.
func sparseCheckoutBound(
	ctx context.Context, cmdr core.Commander, env []string, clone, bound string,
) *core.RefusalError {
	if bound == "" || bound == rootSelf {
		return nil
	}
	if _, ref := runGitPack(ctx, cmdr, env, clone, "sparse-checkout", "init", "--cone"); ref != nil {
		return ref
	}
	return runGitPackOK(ctx, cmdr, env, clone, "sparse-checkout", "set", bound)
}

// gitOptions are the options every pack-add git call carries ahead of the
// shared hook-free settings ([gitcmd.Argv]): no transport over the file
// protocol. The scrubbed environment ([gitEnv]) reads no user or system
// config, so the fresh clone names no filter driver for a checkout to run.
var gitOptions = []string{"-c", "protocol.file.allow=never"}

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
// from the temporary tree: the variables every git call drops
// ([gitcmd.Inherited]), the operator's transport helpers
// ([gitcmd.TransportHelper]), the home and config locations, and the
// template directory.
func scrubGitEnv(key string) bool {
	switch key {
	case "HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
		"GIT_TERMINAL_PROMPT", "GIT_TEMPLATE_DIR":
		return true
	}
	return gitcmd.Inherited(key) || gitcmd.TransportHelper(key) || strings.HasPrefix(key, "GIT_CONFIG_")
}

// runGitPack runs one git command with the shared hermetic prefix and returns
// its trimmed stdout.
func runGitPack(
	ctx context.Context, cmdr core.Commander, env []string, dir, sub string, args ...string,
) (string, *core.RefusalError) {
	argv := gitcmd.Argv(dir, slices.Concat(gitOptions, []string{sub}, args)...)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: gitcmd.Env(env), OutputCap: MaxPackBytes})
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
	ctx context.Context, cmdr core.Commander, env []string, dir, sub string, args ...string,
) *core.RefusalError {
	_, ref := runGitPack(ctx, cmdr, env, dir, sub, args...)
	return ref
}

// checkCloneBounds refuses a clone that already exceeds the pack caps before
// the loader walks it: the clone is on disk by then, but a hostile tree should
// not reach the parse path at all. A subdirectory pin caps only the subtree it
// vendors, so a large central repository's unrelated sibling packs do not
// refuse the sync; the loader caps that pack again on the way in.
func checkCloneBounds(dir, bound string) *core.RefusalError {
	if ref := checkBoundComponents(dir, bound); ref != nil {
		return ref
	}
	root, ok, ref := cloneBoundRoot(dir, bound)
	if ref != nil {
		return ref
	}
	if !ok {
		// A missing bound is the loader's missing-pack refusal, not a cap one.
		return nil
	}
	count := 0
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
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

// cloneBoundRoot resolves the subtree the clone caps apply to: the whole
// clone for a root pin, or the pin's own directory. A bound that is absent
// answers ok=false: the loader reports the missing pack, not the cap.
func cloneBoundRoot(dir, bound string) (string, bool, *core.RefusalError) {
	root := dir
	if bound != "" && bound != rootSelf {
		root = filepath.Join(dir, filepath.FromSlash(bound))
	}
	if _, err := os.Lstat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, badPack(fmt.Sprintf("inspect the git clone: %v", err))
	}
	return root, true, nil
}

// checkBoundComponents proves every component of the bound is a real directory
// inside the clone before the cap walk resolves it: the walk descends whatever
// directory it is handed, so a symlinked intermediate component would let a
// hostile clone point its pack root outside the clone and read a size refusal
// for a tree the caps were never meant to describe. An absent component stays
// the loader's missing-pack refusal, not a cap one.
func checkBoundComponents(dir, bound string) *core.RefusalError {
	if bound == "" || bound == rootSelf {
		return nil
	}
	cur := dir
	for seg := range strings.SplitSeq(bound, "/") {
		cur = filepath.Join(cur, seg)
		if ref, stop := checkBoundComponent(cur, bound); stop {
			return ref
		}
	}
	return nil
}

// checkBoundComponent proves one bound component is a real directory. stop
// reports that the walk ends here: with a refusal, or with none because the
// component is absent and the loader names the missing pack.
func checkBoundComponent(component, bound string) (*core.RefusalError, bool) {
	info, err := os.Lstat(component)
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return nil, true
	case err != nil:
		return badPack(fmt.Sprintf("inspect the git clone: %v", err)), true
	case !info.IsDir():
		return &core.RefusalError{Code: CodePackUnsafePath, Message: core.Msgf(
			"the git clone's %q is not a real directory under the clone root — "+
				"symlinked and special pack roots are refused", filepath.ToSlash(bound))}, true
	}
	return nil, false
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
			return &core.RefusalError{
				Code: CodePackStagingStale,
				Message: core.Msgf("remove the stale %s/%s: %v — delete that path by hand, then re-run",
					RepoPacksDir, name, err),
			}
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
		Message: core.Msgf("%s (staging cleanup failed: %v)", ref.Message, err),
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
