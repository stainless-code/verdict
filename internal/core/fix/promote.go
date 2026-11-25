package fixcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/repro"
)

const (
	codeNoReproduction = "no-reproduction"
	codeHeadMismatch   = "head-mismatch"
	codeTestConflict   = "test-conflict"

	// promotedDir is the worktree-relative promotion directory: promoted
	// regression tests live under .verdict/tests, inside the worktree state
	// directory promotion refuses to reach through a symlink.
	promotedDir     = ".verdict/tests"
	worktreeState   = ".verdict"
	keyRigor        = "rigor"
	promotedDirPerm = 0o755
	promotedFileMs  = 0o644
)

// runPromotion promotes one reproduced finding's retained artifact into the
// worktree: <worktree>/.verdict/tests/<sig>-<hash>.<ext>, with the receipt
// command rewritten to the promoted path. It never writes the ledger or the
// fix patch; only --commit adds the staged promoted file to a commit.
func (st *state) runPromotion(
	ctx context.Context,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
) *core.RefusalError {
	full, rec, ref := st.resolveTest(entries)
	if ref != nil {
		return ref
	}
	receiptPath, receiptCmd, ref := st.testReceipt(rec, full)
	if ref != nil {
		return ref
	}
	artifact, err := repro.Read(dir, full, receiptPath)
	if err != nil {
		return st.refuse(codeNoReproduction, fmt.Sprintf(
			"fix --test: sig %s has no retained artifact: %s",
			readcommon.Prefix(full), err.Error()))
	}
	destRel := promotedDir + "/" + filepath.Base(receiptPath)
	if ref := st.writePromoted(destRel, artifact); ref != nil {
		return ref
	}
	promotedCmd := promotedCommand(receiptCmd, receiptPath, destRel)
	committed := false
	if st.req.Commit {
		var ref *core.RefusalError
		committed, ref = st.commitPromoted(ctx, destRel, full)
		if ref != nil {
			return ref
		}
	}
	st.humanf("» test: %s (%d bytes)\n", destRel, len(artifact))
	st.humanf("run: %s\n", promotedCmd)
	st.next = fmt.Sprintf("promoted regression test at %s — run %s", destRel, promotedCmd)
	st.data = &Data{
		Dir:        dir,
		Ledger:     ledgerPath,
		Sig:        &full,
		MaxPasses:  maxPasses,
		OpenInline: fixOpenInline,
		Fixed:      fixedCount(entries),
		Open:       fixOpen,
		Stopped:    stoppedCount(store),
		Complete:   false,
		TestPath:   &destRel,
		Committed:  committed,
	}
	return nil
}

// resolveTest resolves --test like resolveExplicit — unique prefix, inline,
// open — and additionally requires rigor=reproduced with a reproduction
// receipt.
func (st *state) resolveTest(entries []any) (string, *jq.Obj, *core.RefusalError) {
	flag := st.req.Test
	full := readcommon.ResolveSig(entries, flag)
	switch full {
	case resolveNone:
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix --test: sig prefix '%s' not in store "+
				"(list open inline sigs with report --status open --kind inline)",
			readcommon.PrefixN(flag, sigPrefixMsgLen)))
	case resolveMany:
		return "", nil, st.refuse("ambiguous-sig", fmt.Sprintf(
			"fix --test: sig prefix '%s' is ambiguous "+
				"(copy the full prefix from report output)",
			readcommon.PrefixN(flag, sigPrefixMsgLen)))
	}
	rec := findEntry(entries, full)
	if kindOf(rec) != kindInline {
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix --test: sig %s is a %s finding (--test picks inline only)",
			readcommon.Prefix(full), kindOf(rec)))
	}
	if statusOf(rec) != statusOpen {
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix --test: sig %s is %s (--test promotes open sigs only)",
			readcommon.Prefix(full), statusOf(rec)))
	}
	if rigor := interpOf(rec, keyRigor); rigor != "reproduced" {
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix --test: sig %s is %s (--test promotes reproduced findings only)",
			readcommon.Prefix(full), rigor))
	}
	return full, rec, nil
}

// testReceipt reads the reproduction receipt path and command from the
// finding; a missing or malformed receipt refuses no-reproduction.
func (st *state) testReceipt(rec *jq.Obj, full string) (string, string, *core.RefusalError) {
	raw, _ := rec.Get("reproduction")
	obj, ok := raw.(*jq.Obj)
	if !ok || obj == nil {
		return "", "", st.refuse(codeNoReproduction, fmt.Sprintf(
			"fix --test: sig %s carries no reproduction receipt "+
				"(re-run %s with a reproduction block)",
			readcommon.Prefix(full), st.landVerb()))
	}
	pathRaw, _ := obj.Get("path")
	cmdRaw, _ := obj.Get("command")
	pathStr, okP := pathRaw.(string)
	cmdStr, okC := cmdRaw.(string)
	if !okP || !okC || pathStr == "" || cmdStr == "" {
		return "", "", st.refuse(codeNoReproduction, fmt.Sprintf(
			"fix --test: sig %s carries an unreadable reproduction receipt",
			readcommon.Prefix(full)))
	}
	return pathStr, cmdStr, nil
}

// existingPromotedRefusal checks an existing target: a non-regular file or
// different bytes refuse, identical bytes are a no-op success. The read goes
// through the same [os.Root] handle as the install, so the bytes compared are
// the bytes inside the confined worktree.
func (st *state) existingPromotedRefusal(root *os.Root, info os.FileInfo, destRel string, artifact []byte) *core.RefusalError {
	if !info.Mode().IsRegular() {
		return st.refuse(codeTestConflict, fmt.Sprintf(
			"fix --test: target %s is not a regular file — refusing to overwrite it", destRel))
	}
	existing, err := root.ReadFile(destRel)
	if err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not read existing %s: %v", destRel, err))
	}
	if bytes.Equal(existing, artifact) {
		return nil
	}
	return st.refuse(codeTestConflict, fmt.Sprintf(
		"fix --test: target %s already exists with different bytes — refusing to overwrite it",
		destRel))
}

// writePromoted lands the artifact bytes at the worktree destination: an
// identical existing file is a no-op success, different bytes refuse, and a
// symlink or directory at the target refuses without following it. Every read
// and write runs through an [os.Root] confined to the worktree, and a symlinked
// .verdict or .verdict/tests refuses before anything is created, exactly as the
// reproduction writer confines its own bundle writes.
func (st *state) writePromoted(destRel string, artifact []byte) *core.RefusalError {
	root, ref := st.promotedRoot()
	if ref != nil {
		return ref
	}
	defer func() { _ = root.Close() }()
	if info, err := root.Lstat(destRel); err == nil {
		return st.existingPromotedRefusal(root, info, destRel, artifact)
	} else if !errors.Is(err, os.ErrNotExist) {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stat %s: %v", destRel, err))
	}
	if err := root.MkdirAll(promotedDir, promotedDirPerm); err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not create %s: %v", promotedDir, err))
	}
	return st.installPromoted(root, destRel, artifact)
}

// installPromoted creates the destination exclusively inside root and writes
// artifact. The exclusive create is the install-time guard: a destination that
// appeared after the check above costs an [os.ErrExist] here, which resolves
// exactly like that check — identical bytes are an idempotent success, anything
// else refuses test-conflict — so no window between check and write can
// overwrite a file.
func (st *state) installPromoted(root *os.Root, destRel string, artifact []byte) *core.RefusalError {
	f, err := root.OpenFile(destRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, promotedFileMs)
	switch {
	case errors.Is(err, os.ErrExist):
		return st.existingPromoted(root, destRel, artifact)
	case err != nil:
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not write %s: %v", destRel, err))
	}
	if _, err := f.Write(artifact); err != nil {
		_ = f.Close() // write error already rules; close adds nothing
		return st.abandonPromoted(root, destRel, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // sync error already rules; close adds nothing
		return st.abandonPromoted(root, destRel, err)
	}
	if err := f.Close(); err != nil {
		return st.abandonPromoted(root, destRel, err)
	}
	return nil
}

// existingPromoted re-checks a destination the exclusive create found present
// and answers it exactly as the earlier stat would.
func (st *state) existingPromoted(root *os.Root, destRel string, artifact []byte) *core.RefusalError {
	info, err := root.Lstat(destRel)
	if err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stat %s: %v", destRel, err))
	}
	return st.existingPromotedRefusal(root, info, destRel, artifact)
}

// abandonPromoted drops a half-written install and refuses, so a failed write
// never lingers as a different-bytes conflict on the next retry.
func (st *state) abandonPromoted(root *os.Root, destRel string, err error) *core.RefusalError {
	_ = root.Remove(destRel) //nolint:errcheck // the partial bytes are dead; the refusal carries the failure
	return st.refuse(codeFixFailed, fmt.Sprintf(
		"fix --test: could not write %s: %v", destRel, err))
}

// promotedRoot opens the worktree as an [os.Root] and refuses a symlink on the
// promotion path: .verdict or .verdict/tests pointing outside the worktree
// would land the promoted test outside it.
func (st *state) promotedRoot() (*os.Root, *core.RefusalError) {
	root, err := os.OpenRoot(st.req.RepoRootPath())
	if err != nil {
		return nil, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not open the worktree root: %v", err))
	}
	for _, rel := range []string{worktreeState, promotedDir} {
		if ref := st.checkPromotedComponent(root, rel); ref != nil {
			_ = root.Close()
			return nil, ref
		}
	}
	return root, nil
}

// checkPromotedComponent refuses a symlink or non-directory at one promotion
// path component; an absent component is the ordinary first promotion.
func (st *state) checkPromotedComponent(root *os.Root, rel string) *core.RefusalError {
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not inspect %s: %v", rel, err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return st.refuse(codeTestConflict, fmt.Sprintf(
			"fix --test: %s is a symlink — refusing to write outside the worktree", rel))
	}
	if !info.IsDir() {
		return st.refuse(codeTestConflict, fmt.Sprintf(
			"fix --test: %s is not a directory — refusing to promote into it", rel))
	}
	return nil
}

// promotedCommand rewrites the retained receipt command from the bundle
// artifact path to the promoted worktree path. Both the bare `tests/<base>` and
// the `./tests/<base>` spelling rewrite; a reference inside an absolute path is
// left alone because that spelling names the retained bundle copy, which stays
// runnable from the bundle.
func promotedCommand(receiptCmd, receiptPath, destRel string) string {
	ref := "tests/" + filepath.Base(receiptPath)
	return rewriteBundleRef(rewriteBundleRef(receiptCmd, "./"+ref, destRel), ref, destRel)
}

// rewriteBundleRef replaces every bundle-relative occurrence of from with to.
// A match counts only as a whole path token — neither the byte before it nor
// the byte after it may continue a path — so the `tests/<base>` inside
// `.verdict/tests/<base>` or an absolute bundle path is never rewritten twice,
// and a sibling file such as `tests/<base>.expected` keeps its own spelling.
func rewriteBundleRef(cmd, from, to string) string {
	var sb strings.Builder
	for idx := 0; idx < len(cmd); {
		i := strings.Index(cmd[idx:], from)
		if i < 0 {
			sb.WriteString(cmd[idx:])
			break
		}
		start := idx + i
		end := start + len(from)
		if !refBoundary(cmd, start) || !refEndBoundary(cmd, end) {
			sb.WriteString(cmd[idx:end])
			idx = end
			continue
		}
		sb.WriteString(cmd[idx:start])
		sb.WriteString(to)
		idx = end
	}
	return sb.String()
}

// pathByte reports whether b can continue a path token, so a bundle reference
// touching it names a longer path instead of the token itself.
func pathByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '/', b == '.', b == '-', b == '_', b == '+':
		return true
	default:
		return false
	}
}

// refBoundary reports whether a match at start begins a path token: the byte
// before it is absent or is not part of a path.
func refBoundary(cmd string, start int) bool {
	if start == 0 {
		return true
	}
	return !pathByte(cmd[start-1])
}

// refEndBoundary reports whether a match ending at end ends a path token: the
// byte after it is absent or is not part of a path. A pytest `::test_case`
// selector, a quote, whitespace, and a shell separator all end the token; a
// `.expected` suffix, a `_2` variant, or a `/inner` path do not.
func refEndBoundary(cmd string, end int) bool {
	if end >= len(cmd) {
		return true
	}
	return !pathByte(cmd[end])
}

// commitPromoted stages the promoted file and commits it as
// `test: regression for <sig>` through a pathspec commit: an unrelated file
// already staged in the index stays staged and out of the commit, and every
// unrelated dirty file stays untouched. An already-committed identical file
// reports committed without a new commit. Each command names the repository
// root with `-C`, so the repo-relative destination resolves the same from a
// checkout subdirectory as from the root.
func (st *state) commitPromoted(ctx context.Context, destRel, full string) (bool, *core.RefusalError) {
	root := st.req.RepoRootPath()
	if _, err := st.runGit(ctx, "git", "-C", root, "add", "--", destRel); err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stage %s: %v", destRel, err))
	}
	cached, err := st.runGit(ctx, "git", "-C", root, "diff", "--cached", "--name-only", "--", destRel)
	if err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not inspect staged %s: %v", destRel, err))
	}
	if strings.TrimSpace(cached.Stdout) == "" {
		return true, nil
	}
	msg := "test: regression for " + full
	if _, err := st.runGit(ctx, "git", "-C", root, "commit", "-m", msg, "--", destRel); err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not commit %s: %v", destRel, err))
	}
	return true, nil
}
