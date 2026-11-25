package verifycore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// verifyOutName and verifyTimeoutName are the run's raw output and its
	// timeout marker beside patch.json.
	verifyOutName     = ".verify.out"
	verifyTimeoutName = ".verify.timeout"
	// ReceiptFile is the tool-only patch-path receipt beside patch.json.
	ReceiptFile   = "verify-receipt.json"
	receiptSchema = "1"
	// cannotBack ends every warning for a run whose receipt carries no head.
	cannotBack = " — " + ReceiptFile + " records no head, so this run cannot back a VERIFIED claim"
	// statusProbeTimeout and statusProbeCap bound the status read: only
	// emptiness matters, so a capped listing still reads dirty.
	statusProbeTimeout = 30 * time.Second
	statusProbeCap     = 4 << 10
	// bundleReadCap bounds a bundle file read through the pinned root.
	bundleReadCap = 1 << 20
)

// treeProbe is one read of the checkout verify runs in: the commit and
// whether anything outside verdict's own .verdict/ state differs from it.
type treeProbe struct {
	ref   *core.RefusalError
	head  string
	dirty bool
}

// stateDirRel is verdict's own bundle state, the one part of the tree verify
// writes; everything else under .verdict/ (config, kb, packs, tests,
// verify-env) is reviewed input and counts toward a dirty tree.
var stateDirRel = config.VerdictDir + "/" + config.StateDirName

// probeTree reads the checked-out head and the working-tree status at root
// through the injected commander. --no-optional-locks keeps the status read
// from rewriting the index of the tree it is vouching for. Besides the state
// directory it skips only the generated .verdict/.gitignore, untracked on a
// checkout's first run, which keeps verdict's other runtime files ignored.
func probeTree(ctx context.Context, commander core.Commander, root string) treeProbe {
	head, ref := gitHead(ctx, commander, root)
	if ref != nil {
		return treeProbe{ref: ref}
	}
	tctx, cancel := context.WithTimeout(ctx, statusProbeTimeout)
	defer cancel()
	res, err := commander.Run(tctx, &core.Command{
		Argv: []string{
			"git", "--no-optional-locks", "-C", root, "status", "--porcelain",
			"--", ".", ":(exclude)" + stateDirRel, ":(exclude)" + config.GitignorePath,
		},
		OutputCap: statusProbeCap,
	})
	if err != nil {
		return treeProbe{ref: failed(err)}
	}
	if res.ExitCode != 0 {
		return treeProbe{ref: &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
			"could not read the working tree status at %s (git exit %d)", root, res.ExitCode)}}
	}
	return treeProbe{head: head, dirty: strings.TrimSpace(res.Stdout) != ""}
}

// boundHead is the commit the receipt vouches for: both probes must read the
// same head over a clean tree. HEAD alone says nothing about uncommitted
// edits, so a dirty tree binds no head. Anything else warns and returns "".
func (st *state) boundHead(root string, before, after treeProbe) string {
	gap := probeGap(root, before)
	if gap == "" {
		gap = probeGap(root, after)
	}
	if gap == "" && before.head != after.head {
		gap = fmt.Sprintf("checked-out HEAD moved from %s to %s during verify",
			readcommon.ShortSHA(before.head), readcommon.ShortSHA(after.head))
	}
	if gap != "" {
		st.warn(gap + cannotBack)
		return ""
	}
	return before.head
}

// probeGap names why one probe cannot vouch for a commit, or "".
func probeGap(root string, p treeProbe) string {
	switch {
	case p.ref != nil:
		return fmt.Sprintf("could not read the checked-out tree at %s (%s)", root, p.ref.Message)
	case p.head == "":
		return fmt.Sprintf("could not read the checked-out HEAD at %s (git printed none)", root)
	case p.dirty:
		return fmt.Sprintf("the working tree at %s has uncommitted changes — commit the fix, then run verdict verify", root)
	}
	return ""
}

// bundleHandle pins the bundle directory verify writes into. It is opened
// before the command runs, every write goes through root, and identity is
// the directory as opened: a command that renames the bundle away and puts a
// symlink or another bundle in its place cannot redirect a single write.
type bundleHandle struct {
	info fs.FileInfo
	root *os.Root
	dir  string
	// sig is the finding sig patch.json named when the bundle was pinned.
	sig string
}

// openPatchBundle pins a patch-path bundle and snapshots its finding sig. A
// bundle directory that does not exist reads as the missing patch it implies.
func openPatchBundle(dir string) (*bundleHandle, *core.RefusalError) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, missingPatchRefusal(dir)
	}
	bundle, ref := openBundle(dir)
	if ref != nil {
		return nil, ref
	}
	bundle.sig = bundle.patchSig()
	return bundle, nil
}

// openBundle pins dir. The caller resolved it with ResolveWritableBundleDir,
// so a symlinked bundle directory was already refused.
func openBundle(dir string) (*bundleHandle, *core.RefusalError) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, failed(err)
	}
	info, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, failed(err)
	}
	return &bundleHandle{info: info, root: root, dir: dir}, nil
}

func (b *bundleHandle) close() { _ = b.root.Close() }

// read loads one bundle file through the pinned root, capped like a store.
func (b *bundleHandle) read(name string) ([]byte, error) {
	f, err := readcommon.OpenNoBlockIn(b.root, name)
	if err != nil {
		//nolint:wrapcheck // callers map any read failure to their own refusal
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, bundleReadCap+1))
	if err != nil {
		//nolint:wrapcheck // callers map any read failure to their own refusal
		return nil, err
	}
	return raw, nil
}

// patchSig is the finding sig patch.json names (findingSig, else sig), read
// before the command runs so the command cannot widen its own receipt by
// editing patch.json; a receipt for one finding never backs another.
func (b *bundleHandle) patchSig() string {
	raw, err := b.read(patchFile)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	m, ok := jq.AsMap(doc)
	if err != nil || !ok {
		return ""
	}
	for _, key := range []string{"findingSig", "sig"} {
		if sig, ok := m[key].(string); ok && sig != "" {
			return sig
		}
	}
	return ""
}

// dropReceipt removes the previous receipt before the command starts, so a
// run that fails, aborts, or cannot record leaves no receipt at all rather
// than an earlier pass. A receipt that cannot be removed refuses the run.
func (b *bundleHandle) dropReceipt() *core.RefusalError {
	if err := b.root.Remove(ReceiptFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
			"could not clear the previous %s/%s: %v", b.dir, ReceiptFile, err)}
	}
	return nil
}

// unchanged refuses when the bundle path no longer names the directory verify
// pinned: the command moved it, or replaced it with a symlink or another
// directory. verify then writes nothing, so no receipt lands anywhere.
func (b *bundleHandle) unchanged() *core.RefusalError {
	now, err := os.Lstat(b.dir)
	if err == nil && now.IsDir() && os.SameFile(now, b.info) {
		return nil
	}
	return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
		"bundle directory %s changed while the command ran — nothing was recorded and no receipt was written; "+
			"restore the bundle and re-run verify", b.dir)}
}

// writeReceipt records this run's receipt through the pinned root, after one
// last identity check.
func (b *bundleHandle) writeReceipt(receipt *core.VerifyReceipt) *core.RefusalError {
	if ref := b.unchanged(); ref != nil {
		return ref
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return failed(err)
	}
	if err := atomic.WriteFileIn(b.root, ReceiptFile, append(raw, '\n')); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
			"could not write %s/%s: %v", b.dir, ReceiptFile, err)}
	}
	return nil
}

// ReadVerifyReceipt loads the bundle's receipt with strict capped parsing.
// found is false when no receipt exists; problem names why one that exists
// cannot be read (malformed bytes, an unknown field, another schema version),
// so a damaged receipt never reads as one that passed.
func ReadVerifyReceipt(dir string) (receipt core.VerifyReceipt, found bool, problem string) {
	path := dir + "/" + ReceiptFile
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return receipt, false, ""
	}
	raw, err := jq.CappedFile(path, receiptCap)
	if err != nil {
		return receipt, true, err.Error()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return receipt, true, err.Error()
	}
	if receipt.SchemaVersion != receiptSchema {
		return receipt, true, fmt.Sprintf("schema_version %q, want %s", receipt.SchemaVersion, receiptSchema)
	}
	return receipt, true, ""
}
