// Package contenttree names checkout content by git tree id and rebuilds it
// elsewhere. A verify receipt is bound to the tree the command ran on, and
// record and post compare that tree with the tree they grade, so "verified"
// means this exact content, whatever HEAD, the index, or the working tree
// say later.
//
// The content is the paths in the index — what a working-tree bundle's diff
// covers: tracked files and intent-to-add (`git add -N`) entries — each with
// its working-tree bytes cleaned as `git add` cleans them: git's built-in
// conversions (core.autocrlf, core.eol, the text/eol attributes, ident,
// working-tree-encoding) apply, so a CRLF checkout of an LF commit names that
// commit's tree. No program runs: every configured filter driver is switched
// off for the call ([gitcmd.FilterOptions]), so a path a clean or process
// filter (git-lfs included) would handle is named by its working-tree bytes,
// and no fsmonitor or hook runs. A snapshot holds the committed form — the
// blobs the tree names, so LF line endings wherever the commit has them —
// except the paths git's checkout expands (ident, working-tree-encoding),
// which it writes as git's built-in checkout does, with every filter driver
// still off, and which its re-check cleans back to their blobs. Untracked
// and ignored files are outside the content, as they are outside the
// bundle, and the command sees no dependency or config the checkout keeps
// in ignored files.
//
// Naming writes the blobs and the tree into the repository's object store —
// only the indexed files', never an untracked or ignored file's — and a
// snapshot adds one unreferenced commit, which git's normal gc collects.
// Every index write goes through a throwaway index: the user's index, refs,
// and working tree are never written.
package contenttree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// excluded is verdict's own runtime state, never reviewed content: bundle
// state, `verdict loop` round receipts, and the generated .gitignore. The
// tree keeps their index version and ignores working-tree changes, so a
// committed .verdict/.gitignore still matches the commit's tree.
var excluded = []string{
	config.VerdictDir + "/" + config.StateDirName,
	config.VerdictDir + "/loop",
	config.GitignorePath,
}

// ErrUnresolved marks a revision the repository does not hold.
var ErrUnresolved = jq.NewErr("revision not in the local repository")

// Git runs the reads against one checkout root through an injected commander.
// Env is the base child environment (nil inherits the process environment).
type Git struct {
	Cmdr core.Commander
	// recheck, set on a snapshot's re-check, names the bytes on disk as
	// they are, with no clean step: the snapshot already holds cleaned
	// blobs, and its re-check must name exactly those, not clean them again.
	recheck *recheck
	Root    string
	Env     []string
}

// recheck is a snapshot's re-check: every path named byte for byte except
// reclean, the paths the snapshot holds as git's checkout expands them,
// which are cleaned back to their blob.
type recheck struct {
	reclean []string
}

// Content is what a snapshot holds and what it left out.
type Content struct {
	// Tree is the tree id of exactly the snapshot's bytes.
	Tree string
	// Untracked are untracked, unignored paths: outside the bundle's diff
	// and outside the tree (`git add -N` brings one in).
	Untracked []string
	// Ignored are the top-level entries .gitignore excludes that exist in
	// the checkout: dependencies, build output, local config.
	Ignored []string
	// Gitlinks are submodules: recorded by their commit, left empty.
	Gitlinks []string
	// Filtered are the paths whose attributes select a configured filter
	// driver and whose cleaned blob is neither the index's nor HEAD's, by
	// driver: verdict ran none, so the tree holds their working-tree bytes,
	// which may differ from what the configured filter would produce (a
	// git-lfs pointer), and matches only a commit that stores those same
	// bytes.
	Filtered []Filtered
}

// Worktree returns the tree id of the checkout's content: every index path's
// working-tree bytes, a path deleted from disk left out, and verdict's
// state at its index version.
func (g Git) Worktree(ctx context.Context) (string, error) {
	g, err := g.absolute()
	if err != nil {
		return "", err
	}
	entries, kept, err := g.list(ctx)
	if err != nil {
		return "", err
	}
	tree, _, err := g.name(ctx, entries, kept, "")
	return tree, err
}

// Snapshot copies the checkout's content into dest (an absent or empty
// directory) and names exactly the bytes it copied, so the name and the copy
// cannot disagree however the checkout changes meanwhile. dest becomes a
// detached linked worktree of an unreferenced commit of that tree, with its
// index read from the tree, so git works inside it; before returning, dest
// is named again and must still be that tree. No branch or ref is created;
// RemoveCheckout undoes it.
func (g Git) Snapshot(ctx context.Context, dest string) (content Content, err error) {
	g, err = g.absolute()
	if err != nil {
		return Content{}, err
	}
	// The staging directory is named after dest, so whatever sweeps a
	// stranded dest sweeps its staging directory too.
	staging, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".staging-")
	if err != nil {
		return Content{}, jq.Sealed("create the staging directory", err)
	}
	defer func() { err = errors.Join(err, RemoveTree(staging)) }()
	entries, kept, err := g.list(ctx)
	if err != nil {
		return Content{}, err
	}
	content.Gitlinks = gitlinks(entries)
	var st staged
	if content.Tree, st, err = g.name(ctx, entries, kept, staging); err != nil {
		return Content{}, err
	}
	content.Filtered = st.filtered
	if err := g.materialize(ctx, content.Tree, staging, dest, st.expanded); err != nil {
		return Content{}, err
	}
	content.Untracked, content.Ignored = g.leftOut(ctx)
	return content, nil
}

// materialize makes dest a linked worktree at a commit of tree holding the
// staged bytes, then re-names them as they are — the expanded paths cleaned
// back — : what the command will see must be the tree.
func (g Git) materialize(ctx context.Context, tree, staging, dest string, expanded []string) error {
	commit, err := g.commitTree(ctx, tree)
	if err != nil {
		return err
	}
	// --no-checkout: git writes no file, so no smudge filter or checkout
	// hook could run; the staged bytes move in instead.
	if _, err := g.runEnv(ctx, nil, cmdWorktree, "add", "--no-checkout", "--detach", "--quiet", dest, commit); err != nil {
		return err
	}
	in := Git{Cmdr: g.Cmdr, Root: dest, Env: g.Env, recheck: &recheck{reclean: expanded}}
	if err := in.fill(ctx, staging); err != nil {
		return err
	}
	got, err := in.Worktree(ctx)
	if err != nil {
		return err
	}
	if got != tree {
		return jq.NewErrf("the snapshot at %s holds tree %s, not the %s it was made from", dest, got, tree)
	}
	return nil
}

// commitTree makes the commit a snapshot checks out: HEAD itself when the
// tree is HEAD's, so git inside the snapshot reads the checkout's own history
// and commit; otherwise one unreferenced commit of the tree on top of HEAD
// (parentless when the checkout has no HEAD). Either way the commit's tree is
// exactly the snapshot's.
func (g Git) commitTree(ctx context.Context, tree string) (string, error) {
	args := []string{"commit-tree", tree, "-m", "verdict verify snapshot"}
	if head, ok := g.head(ctx); ok {
		if headTree, err := g.Commit(ctx, head); err == nil && headTree == tree {
			return head, nil
		}
		args = append(args, "-p", head)
	}
	res, err := g.runEnv(ctx, identity, args...)
	if err != nil {
		return "", err
	}
	return objectID("commit-tree", res.Stdout)
}

// head is the checkout's HEAD commit, ok false when it has none (an unborn
// branch) or git printed anything but an object id.
func (g Git) head(ctx context.Context) (string, bool) {
	res, err := g.exec(ctx, cmdRevParse, "--verify", "-q", "HEAD^{commit}")
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	id, err := objectID(cmdRevParse, res.Stdout)
	return id, err == nil
}

// fill moves the staged bytes into a fresh linked worktree at g.Root and reads
// its index from HEAD (no working-tree write, so no filter or hook).
func (g Git) fill(ctx context.Context, staging string) error {
	if err := linkedCheckout(g.Root); err != nil {
		return err
	}
	if err := moveEntries(staging, g.Root); err != nil {
		return err
	}
	_, err := g.runEnv(ctx, nil, "read-tree", "HEAD")
	return err
}

func (g Git) absolute() (Git, error) {
	root, err := filepath.Abs(g.Root)
	if err != nil {
		return g, jq.Sealed("resolve "+g.Root, err)
	}
	g.Root = root
	return g, nil
}

// Commit returns the tree id of rev, or ErrUnresolved when the repository
// does not hold it.
func (g Git) Commit(ctx context.Context, rev string) (string, error) {
	res, err := g.exec(ctx, cmdRevParse, "--verify", "-q", rev+"^{tree}")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", ErrUnresolved
	}
	return objectID(cmdRevParse, res.Stdout)
}

// objectID returns git's output as an object id (SHA-1 or SHA-256 hex), or
// an error when it is anything else: a receipt names content only by an id
// git produced, never by whatever a commander printed.
func objectID(op, out string) (string, error) {
	id := strings.TrimSpace(out)
	if (len(id) == sha1Hex || len(id) == sha256Hex) && strings.Trim(id, "0123456789abcdef") == "" {
		return id, nil
	}
	return "", jq.NewErrf("git %s printed %q, not an object id", op, truncate(id))
}

func truncate(s string) string {
	if len(s) > sha256Hex {
		return s[:sha256Hex] + "…"
	}
	return s
}

// sha1Hex and sha256Hex are the hex lengths of git's two object formats.
const (
	sha1Hex   = 40
	sha256Hex = 64
)

// identity is the fixed author of a snapshot commit, so making one never
// depends on (or prompts for) the user's git identity.
var identity = []string{
	"GIT_AUTHOR_NAME=verdict", "GIT_AUTHOR_EMAIL=verdict@localhost",
	"GIT_COMMITTER_NAME=verdict", "GIT_COMMITTER_EMAIL=verdict@localhost",
}

// linkedCheckout checks dest holds the `.git` file of a linked worktree: a
// `gitdir:` file naming its admin directory. Anything else is not the
// checkout git makes, and not one the sandbox can protect, since it pins the
// root's git pointer.
func linkedCheckout(dest string) error {
	dotgit := filepath.Join(dest, ".git")
	info, err := os.Lstat(dotgit)
	if err != nil || !info.Mode().IsRegular() {
		return jq.NewErrf("git worktree add left no .git file at %s", dest)
	}
	raw, err := os.ReadFile(dotgit)
	if err != nil {
		return jq.Sealed("read "+dotgit, err)
	}
	if !strings.HasPrefix(string(raw), "gitdir: ") {
		return jq.NewErrf("%s is not a linked worktree's gitdir: file", dotgit)
	}
	return nil
}

// RemoveCheckout removes a Snapshot's worktree and its registration, even
// when the command left files in it or made them read-only.
func (g Git) RemoveCheckout(ctx context.Context, dest string) error {
	_, err := g.runEnv(ctx, nil, cmdWorktree, "remove", "--force", dest)
	if rerr := RemoveTree(dest); err == nil && rerr != nil {
		err = rerr
	}
	if perr := g.Prune(ctx); err == nil {
		err = perr
	}
	return err
}

// Prune drops the registration of every linked worktree whose directory is
// gone — a snapshot stranded by a killed run.
func (g Git) Prune(ctx context.Context) error {
	_, err := g.runEnv(ctx, nil, cmdWorktree, "prune")
	return err
}
