package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

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
	Replaced     bool    `json:"replaced"`
	PreviousHash *string `json:"previous_hash"`
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
type AddResponse struct {
	At    string
	Next  string
	Human []string
	Data  *AddData
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
	if sameTree(filepath.Join(root, filepath.FromSlash(RepoPacksDir)), src.dir) {
		return nil, badPack(fmt.Sprintf(
			"pack source %q is inside %s — it is already vendored", src.label, RepoPacksDir))
	}
	p, ref := LoadDir(src.dir)
	if ref != nil {
		return nil, ref
	}
	if p == nil {
		return nil, badPack(fmt.Sprintf("pack source %q carries no %s", src.label, ManifestFile))
	}
	lock, ref := loadLock(root)
	if ref != nil {
		return nil, ref
	}
	var old *LockEntry
	if existing := lockEntry(lock, p.Name); existing != nil {
		previous := *existing
		old = &previous
	}
	slug := packSlug(p.Name)
	if ref := vendorPack(root, src.dir, slug, p); ref != nil {
		return nil, ref
	}
	entry := LockEntry{
		Name: p.Name, Source: src.label, Version: p.Version, Hash: p.Hash,
	}
	entry.Sig = LockSig(entry)
	if ref := writeLock(root, upsertLock(lock, entry)); ref != nil {
		return nil, ref
	}
	return addResponse(p, entry, old, slug), nil
}

// addResponse renders the added row and its human line.
func addResponse(p *Pack, entry LockEntry, old *LockEntry, slug string) *AddResponse {
	data := &AddData{
		Pack: Entry{
			Name: p.Name, Version: p.Version, Layer: string(LayerRepo),
			Source: RepoPacksDir + "/" + slug, Hash: p.Hash,
			Lenses: lensIDsOf(p), Rules: ruleIDsOf(p),
		},
	}
	line := fmt.Sprintf("added %s@%s source=%s sha256:%s", p.Name, p.Version, entry.Source, p.Hash)
	if old != nil {
		data.Replaced = true
		previous := old.Hash
		data.PreviousHash = &previous
		line = fmt.Sprintf("updated %s@%s source=%s sha256:%s -> sha256:%s",
			p.Name, p.Version, entry.Source, previous, p.Hash)
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
	dir     string
	label   string
	cleanup func()
}

// addSource resolves a directory or a hermetic git clone.
func addSource(ctx context.Context, req *AddRequest) (*packSource, *core.RefusalError) {
	source := strings.TrimSpace(req.Source)
	if url, refName, ok := splitGitSource(source); ok {
		return cloneSource(ctx, req, source, url, refName)
	}
	dir, err := filepath.Abs(source)
	if err != nil {
		return nil, badPack(fmt.Sprintf("pack source %q cannot be resolved: %v", source, err))
	}
	return &packSource{dir: dir, label: "dir:" + filepath.ToSlash(source), cleanup: func() {}}, nil
}

// splitGitSource recognizes the published https and ssh forms and the
// optional @ref suffix. A ref is the last @ after the last slash, so URL
// userinfo such as git@host stays part of the URL.
func splitGitSource(source string) (string, string, bool) {
	url, refName, hasRef := refSplit(source)
	if hasRef {
		if refName == "" || !gitRef.MatchString(refName) {
			return "", "", false
		}
	}
	if !gitURL(url) {
		return "", "", false
	}
	return url, refName, true
}

// refSplit cuts the @ref suffix. The first @ is URL userinfo, never the
// separator; the first @ after the authority (or after the scp user) is.
func refSplit(source string) (string, string, bool) {
	start := 0
	if scheme := strings.Index(source, "://"); scheme >= 0 {
		rest := source[scheme+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return source, "", false
		}
		start = scheme + 3 + slash
	} else {
		at := strings.Index(source, "@")
		if at < 0 {
			return source, "", false
		}
		start = at + 1
	}
	if at := strings.Index(source[start:], "@"); at >= 0 {
		split := start + at
		return source[:split], source[split+1:], true
	}
	return source, "", false
}

// gitURL reports whether url is an https or ssh git remote.
func gitURL(url string) bool {
	if rest, ok := strings.CutPrefix(url, "https://"); ok {
		return rest != ""
	}
	if rest, ok := strings.CutPrefix(url, "ssh://"); ok {
		return rest != ""
	}
	at, colon := strings.Index(url, "@"), strings.Index(url, ":")
	return at > 0 && colon > at+1 && !strings.Contains(url, "://")
}

// cloneSource fetches one git URL hermetically into a temp directory and
// checks out the resolved HEAD commit before loading the pack.
func cloneSource(
	ctx context.Context, req *AddRequest, source, url, refName string,
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
	if ref := gitClone(ctx, cmdr, env, tmp, url, refName, clone); ref != nil {
		cleanup()
		return nil, ref
	}
	sha, ref := runGitPack(ctx, cmdr, env, clone, "rev-parse", "HEAD")
	if ref != nil {
		cleanup()
		return nil, ref
	}
	if !hex40(sha) {
		cleanup()
		return nil, badPack(fmt.Sprintf("git remote %q resolved HEAD to %q, not a 40-hex commit", url, sha))
	}
	if ref := runGitPackOK(ctx, cmdr, env, clone, "checkout", "--detach", sha); ref != nil {
		cleanup()
		return nil, ref
	}
	return &packSource{
		dir: clone, label: "git:" + url + "@" + sha, cleanup: cleanup,
	}, nil
}

// prepareGitEnv creates the scrubbed HOME/XDG tree and the hooks directory a
// Windows clone uses in place of /dev/null.
func prepareGitEnv(tmp string) *core.RefusalError {
	for _, dir := range []string{"home", "cache", "config", "data", "state", "hooks"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0o755); err != nil {
			return badPack(fmt.Sprintf("could not create the pack work dir: %v", err))
		}
	}
	return nil
}

// gitClone runs the hermetic clone with the resolved hooks path.
func gitClone(
	ctx context.Context, cmdr core.Commander, env []string, tmp, url, refName, clone string,
) *core.RefusalError {
	args := []string{
		"-c", "core.hooksPath=" + hooksPath(tmp),
		"-c", "protocol.file.allow=never",
		"clone", "--depth=1", "--no-tags", "--no-recurse-submodules",
	}
	if refName != "" {
		args = append(args, "--branch", refName)
	}
	args = append(args, url, clone)
	_, ref := runGitPack(ctx, cmdr, env, "", args...)
	return ref
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
	out := make([]string, 0, len(base)+8)
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
		"SSH_ASKPASS":
		return true
	}
	return strings.HasPrefix(key, "GIT_CONFIG_")
}

// runGitPack runs one git command and returns its trimmed stdout.
func runGitPack(
	ctx context.Context, cmdr core.Commander, env []string, dir string, args ...string,
) (string, *core.RefusalError) {
	argv := append([]string{"git"}, args...)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: env, Dir: dir, OutputCap: MaxPackBytes})
	if err != nil {
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", &core.RefusalError{Code: "missing-tool", Message: "git is not on PATH"}
		}
		return "", badPack(fmt.Sprintf("git %s failed: %v", args[0], jq.Sealed(res.Stderr, err)))
	}
	if res.ExitCode != 0 {
		return "", badPack(fmt.Sprintf("git %s failed: %s", args[0], strings.TrimSpace(res.Stderr)))
	}
	if res.StdoutTruncated {
		return "", tooLarge("git " + args[0] + " output is over its cap")
	}
	return strings.TrimSpace(res.Stdout), nil
}

// runGitPackOK is runGitPack for a call whose stdout carries nothing.
func runGitPackOK(
	ctx context.Context, cmdr core.Commander, env []string, dir string, args ...string,
) *core.RefusalError {
	_, ref := runGitPack(ctx, cmdr, env, dir, args...)
	return ref
}

// vendorPack copies the loaded pack tree into .verdict/packs/<slug>/, removing
// any previous tree first. Every write stays under an [os.Root].
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
	if err := dest.RemoveAll(slug); err != nil {
		return badPack(fmt.Sprintf("replace %s/%s: %v", RepoPacksDir, slug, err))
	}
	if ref := writeVendored(dest, src, slug, p); ref != nil {
		_ = dest.RemoveAll(slug)
		return ref
	}
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := dest.MkdirAll(slug, 0o755); err != nil {
		return badPack(fmt.Sprintf("create %s/%s: %v", RepoPacksDir, slug, err))
	}
	if ref := writeVendoredFile(dest, slug, ManifestFile, manifest); ref != nil {
		return ref
	}
	if ref := writeVendoredDir(dest, slug, LensesDir); ref != nil {
		return ref
	}
	for i := range p.Lenses {
		rel := LensesDir + "/" + p.Lenses[i].ID + mdExt
		if ref := writeVendoredFile(dest, slug, rel, p.Lenses[i].Body); ref != nil {
			return ref
		}
	}
	if ref := writeVendoredDir(dest, slug, RulesDir); ref != nil {
		return ref
	}
	for i := range p.Rules {
		rel := RulesDir + "/" + p.Rules[i].ID + mdExt
		if ref := writeVendoredFile(dest, slug, rel, p.Rules[i].Body); ref != nil {
			return ref
		}
	}
	return nil
}

// writeVendoredDir creates one fixed layout directory under slug.
func writeVendoredDir(dest *os.Root, slug, dir string) *core.RefusalError {
	if err := dest.MkdirAll(filepath.Join(slug, dir), 0o755); err != nil {
		return badPack(fmt.Sprintf("create %s/%s/%s: %v", RepoPacksDir, slug, dir, err))
	}
	return nil
}

// writeVendoredFile writes one slash-relative pack file atomically inside the
// destination root.
func writeVendoredFile(dest *os.Root, slug, rel string, body []byte) *core.RefusalError {
	name := filepath.Join(slug, filepath.FromSlash(rel))
	if err := atomic.WriteFileIn(dest, name, body); err != nil {
		return badPack(fmt.Sprintf("write %s/%s/%s: %v", RepoPacksDir, slug, rel, err))
	}
	return nil
}

// sameTree reports whether two paths name the same directory, following
// symlinks on both sides when they exist.
func sameTree(dir, path string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
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
