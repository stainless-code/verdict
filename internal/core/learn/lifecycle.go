package learncore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

const rejectionDir = ".verdict/learning-rejections"

func (st *state) rejected(id string) bool {
	_, err := os.Lstat(filepath.Join(st.root, rejectionDir, id+".json"))
	return !errors.Is(err, fs.ErrNotExist)
}

func (st *state) loadProposal(id string) (*pack.Pack, *core.RefusalError) {
	if !kb.ValidID(id) {
		return nil, st.refuse(codeBadID, fmt.Sprintf(msgBadID, id))
	}
	if st.rejected(id) {
		return nil, st.refuse(codeBadRule, "learning was rejected; its tombstone prevents acceptance")
	}
	p, ref := pack.LoadDir(filepath.Join(st.root, learningsDir, id))
	if ref != nil {
		return nil, ref
	}
	if p == nil || len(p.Rules) != 1 || p.Rules[0].ID != id {
		return nil, st.refuse(codeUnknownRule, "no matching one-rule proposal pack")
	}
	return p, nil
}

func (st *state) runAccept() *core.RefusalError {
	p, ref := st.loadProposal(st.req.Accept)
	if ref != nil {
		return ref
	}
	st.req.ID = st.req.Accept
	if st.req.Rigor == "" {
		st.req.Rigor = rigorPointed
	}
	return st.importRule(p, &p.Rules[0])
}

func (st *state) runReject() *core.RefusalError {
	id := st.req.Reject
	if !kb.ValidID(id) {
		return st.refuse(codeBadID, fmt.Sprintf(msgBadID, id))
	}
	if utf8.RuneCountInString(st.req.Reason) < reasonMin {
		return st.refuse(codeBadReason, "learn --reject needs --reason with 30+ chars")
	}
	rows, err := kb.ReadRows(st.root)
	if err != nil && !errors.Is(err, kb.ErrMissing) {
		return st.corruptStore()
	}
	if kb.FindRow(rows, id) != nil {
		return st.refuse(codeBadRule, "learning is already in the KB; use --retire to veto an accepted rule")
	}
	if !st.rejected(id) {
		if _, ref := st.loadProposal(id); ref != nil {
			return ref
		}
	}
	row := jq.NewObj("id", id, "status", "rejected", "reason", st.req.Reason)
	st.data = &Data{Rule: row}
	st.next = "rejected learning ids are never proposed again"
	st.humanf("reject %s (dry_run=%t)", id, st.req.DryRun)
	if st.req.DryRun {
		return nil
	}
	root, err := os.OpenRoot(st.root)
	if err != nil {
		return st.refuse(codeLearnFailed, err.Error())
	}
	defer root.Close()
	if err := root.MkdirAll(rejectionDir, 0o755); err != nil {
		return st.refuse(codeLearnFailed, err.Error())
	}
	if !st.rejected(id) {
		if err := atomic.WriteFileIn(root, path.Join(rejectionDir, id+".json"), []byte(row.MarshalCompact()+"\n")); err != nil {
			return st.refuse(codeLearnFailed, err.Error())
		}
	}
	if err := root.RemoveAll(path.Join(learningsDir, id)); err != nil {
		return st.refuse(codeLearnFailed, err.Error())
	}
	return nil
}
