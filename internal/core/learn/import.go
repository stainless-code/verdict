package learncore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/kb"
)

func (st *state) runImport() *core.RefusalError {
	p, ref := pack.LoadDir(st.req.Pack)
	if ref != nil {
		return ref
	}
	if p == nil {
		return st.refuse(pack.CodeBadPack, "explicit pack directory has no pack.json")
	}
	for i := range p.Rules {
		rule := &p.Rules[i]
		if rule.ID != st.req.ID {
			continue
		}
		return st.importRule(p, rule)
	}
	return st.refuse(codeUnknownRule, fmt.Sprintf("pack has no rule %q", st.req.ID))
}

func (st *state) importRule(p *pack.Pack, rule *pack.Rule) *core.RefusalError {
	if strings.TrimSpace(rule.Category) == "" || strings.TrimSpace(rule.Description) == "" || len(rule.Examples) == 0 {
		return st.refuse(codeBadRule, "import requires category, description and examples")
	}
	if st.req.Provenance != "" && st.req.Provenance != defaultProvenance {
		return st.refuse(codeBadFlags, "pack import determines provenance")
	}
	st.req.Rule = rule.Description
	st.req.Scope = strings.Join(rule.Globs, ",")
	st.req.Provenance = "pack:" + p.Name + "@" + p.Version
	in, ref := st.validateAppend()
	if ref != nil {
		return ref
	}
	if ref := st.rejectAppendDup(in.rule, in.lid); ref != nil {
		return ref
	}
	row := appendRowDoc(in)
	row.Set("category", rule.Category)
	row.Set("description", rule.Description)
	row.Set("examples", rule.Examples)
	row.Set("severity", rule.Level)
	row.Set("pack_hash", p.Hash)
	row.Set("body", string(rule.Body))
	if err := kb.AppendRow(st.root, row.MarshalCompact()+"\n"); err != nil {
		return st.refuse(codeLearnFailed, fmt.Sprintf(msgWriteKB, st.kbDisplay(), err))
	}
	st.data = &Data{Rule: row}
	st.next = nextAppend
	st.humanf("imported %s from %s@%s sha256:%s\n", rule.ID, p.Name, p.Version, p.Hash)
	return nil
}
