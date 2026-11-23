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
	// regression tests live under .verdict/tests.
	promotedDir     = ".verdict/tests"
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
// symlink or directory at the target refuses without following it.
func (st *state) writePromoted(destAbs, destRel string, artifact []byte) *core.RefusalError {
	info, err := os.Lstat(destAbs)
	if err == nil {
		return st.existingPromotedRefusal(info, destAbs, destRel, artifact)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not stat %s: %v", destRel, err))
	}
	if err := os.MkdirAll(filepath.Dir(destAbs), promotedDirPerm); err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not create %s: %v", promotedDir, err))
	}
	if err := atomic.WriteFileMode(destAbs, artifact, promotedFileMs); err != nil {
		return st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not write %s: %v", destRel, err))
	}
	return nil
}

// promotedCommand rewrites the retained receipt command from the bundle
// artifact path to the promoted worktree path.
func promotedCommand(receiptCmd, receiptPath, destRel string) string {
	base := filepath.Base(receiptPath)
	bundleRef := "tests/" + base
	promotedRef := promotedDir + "/" + base
	if strings.Contains(receiptCmd, bundleRef) {
		return strings.ReplaceAll(receiptCmd, bundleRef, promotedRef)
	}
	return receiptCmd
}

// commitPromoted stages only the promoted file and commits it as
// `test: regression for <sig>`, leaving every unrelated dirty file
// untouched and uncommitted. An already-committed identical file reports
// committed without a new commit.
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
	if _, err := st.runGit(ctx, "git", "commit", "-m", msg); err != nil {
		return false, st.refuse(codeFixFailed, fmt.Sprintf(
			"fix --test: could not commit %s: %v", destRel, err))
	}
	return true, nil
}
