package fixcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/repro"
)

const (
	codeNoReproduction = "no-reproduction"
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
	destAbs := filepath.Join(st.req.RepoRootPath(), filepath.FromSlash(destRel))
	if ref := st.writePromoted(destAbs, destRel, artifact); ref != nil {
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
				"(re-run record with a reproduction block)",
			readcommon.Prefix(full)))
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
// different bytes refuse, identical bytes are a no-op success.
func (st *state) existingPromotedRefusal(info os.FileInfo, destAbs, destRel string, artifact []byte) *core.RefusalError {
	if !info.Mode().IsRegular() {
		return st.refuse(codeTestConflict, fmt.Sprintf(
			"fix --test: target %s is not a regular file — refusing to overwrite it", destRel))
	}
	existing, err := os.ReadFile(destAbs)
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
// symlink or directory at the target refuses without following it. The write
// itself runs through an [os.Root] confined to the worktree, and a symlinked
// .verdict or .verdict/tests refuses before anything is created, exactly as
// the reproduction writer confines its own bundle writes.
func (st *state) writePromoted(destAbs, destRel string, artifact []byte) *core.RefusalError {
	root, ref := st.promotedRoot()
	if ref != nil {
		return ref
	}
	defer func() { _ = root.Close() }()
	info, err := os.Lstat(destAbs)
	if err == nil {
		return st.existingPromotedRefusal(info, destAbs, destRel, artifact)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stat %s: %v", destRel, err))
	}
	if err := root.MkdirAll(promotedDir, promotedDirPerm); err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not create %s: %v", promotedDir, err))
	}
	if err := atomic.WriteFileModeIn(root, destRel, artifact, promotedFileMs); err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not write %s: %v", destRel, err))
	}
	return nil
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
// A match only counts at a path-token boundary, so the `tests/<base>` inside
// `.verdict/tests/<base>` or an absolute bundle path is never rewritten twice.
func rewriteBundleRef(cmd, from, to string) string {
	var sb strings.Builder
	for idx := 0; idx < len(cmd); {
		i := strings.Index(cmd[idx:], from)
		if i < 0 {
			sb.WriteString(cmd[idx:])
			break
		}
		start := idx + i
		if !refBoundary(cmd, start) {
			sb.WriteString(cmd[idx : start+len(from)])
			idx = start + len(from)
			continue
		}
		sb.WriteString(cmd[idx:start])
		sb.WriteString(to)
		idx = start + len(from)
	}
	return sb.String()
}

// refBoundary reports whether a match at start begins a path token: the byte
// before it is absent or is not part of a path.
func refBoundary(cmd string, start int) bool {
	if start == 0 {
		return true
	}
	switch prev := cmd[start-1]; {
	case prev >= 'a' && prev <= 'z', prev >= 'A' && prev <= 'Z', prev >= '0' && prev <= '9':
		return false
	case prev == '/', prev == '.', prev == '-', prev == '_', prev == '+':
		return false
	default:
		return true
	}
}

// commitPromoted stages the promoted file and commits it as
// `test: regression for <sig>` through a pathspec commit: an unrelated file
// already staged in the index stays staged and out of the commit, and every
// unrelated dirty file stays untouched. An already-committed identical file
// reports committed without a new commit.
func (st *state) commitPromoted(ctx context.Context, destRel, full string) (bool, *core.RefusalError) {
	if _, err := st.runGit(ctx, "git", "add", "--", destRel); err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stage %s: %v", destRel, err))
	}
	cached, err := st.runGit(ctx, "git", "diff", "--cached", "--name-only", "--", destRel)
	if err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not inspect staged %s: %v", destRel, err))
	}
	if strings.TrimSpace(cached.Stdout) == "" {
		return true, nil
	}
	msg := "test: regression for " + full
	if _, err := st.runGit(ctx, "git", "commit", "-m", msg, "--", destRel); err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not commit %s: %v", destRel, err))
	}
	return true, nil
}
