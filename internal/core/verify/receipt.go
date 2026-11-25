package verifycore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// ReceiptFile is the tool-only patch-path receipt beside patch.json.
	ReceiptFile   = "verify-receipt.json"
	receiptSchema = "1"
	// cannotBack ends every warning for a run whose receipt carries no head.
	cannotBack = " — " + ReceiptFile + " records no head, so this run cannot back a VERIFIED claim"
	// statusProbeTimeout and statusProbeCap bound the status read: only
	// emptiness matters, so a capped listing still reads dirty.
	statusProbeTimeout = 30 * time.Second
	statusProbeCap     = 4 << 10
)

// treeProbe is one read of the checkout verify runs in: the commit and
// whether anything outside verdict's own .verdict/ state differs from it.
type treeProbe struct {
	ref   *core.RefusalError
	head  string
	dirty bool
}

// probeTree reads the checked-out head and the working-tree status at root
// through the injected commander. --no-optional-locks keeps the status read
// from rewriting the index of the tree it is vouching for.
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
			"--", ".", ":(exclude).verdict",
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

// patchSig is the finding sig patch.json names (findingSig, else sig),
// copied into the receipt so a receipt for one finding never backs a claim
// for another.
func patchSig(dir string) string {
	raw, err := noblock.ReadFile(patchPath(dir))
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

// writeVerifyReceipt replaces the receipt with this run's. Every completed run
// writes one, failed or not, so an earlier pass never outlives a later failure.
func writeVerifyReceipt(dir string, receipt *core.VerifyReceipt) *core.RefusalError {
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return failed(err)
	}
	path := dir + "/" + ReceiptFile
	if err := atomic.WriteFile(path, append(raw, '\n')); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf("could not write %s: %v", path, err)}
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
