package verifycore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/stainless-code/verdict/internal/atomic"
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
	receiptSchema = "2"
	// bundleReadCap bounds a bundle file read through the pinned root.
	bundleReadCap = 1 << 20
)

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
		return &core.RefusalError{Code: codeVerifyFailed, Message: core.Msgf(
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
	return &core.RefusalError{Code: codeVerifyFailed, Message: core.Msgf(
		"bundle directory %s changed while the command ran — nothing was recorded and no receipt was written; "+
			"restore the bundle and re-run verify", b.dir)}
}

// writeReceipt records this run's receipt through the pinned root, after one
// last identity check.
func (b *bundleHandle) writeReceipt(receipt *core.VerifyReceipt) *core.RefusalError {
	if ref := b.unchanged(); ref != nil {
		return ref
	}
	raw, err := jq.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return failed(err)
	}
	if err := atomic.WriteFileIn(b.root, ReceiptFile, append(raw, '\n')); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: core.Msgf(
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
	if problem := decodeStrict(raw, &receipt); problem != "" {
		return receipt, true, problem
	}
	if receipt.SchemaVersion != receiptSchema {
		return receipt, true, fmt.Sprintf("schema_version %q, want %s", receipt.SchemaVersion, receiptSchema)
	}
	return receipt, true, ""
}

// decodeStrict decodes exactly one JSON object with no unknown fields and
// nothing after it but whitespace: a second value or a malformed suffix
// makes the whole file unreadable rather than letting the first value pass.
func decodeStrict(raw []byte, into any) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err.Error()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "trailing data after the receipt object"
	}
	return ""
}
