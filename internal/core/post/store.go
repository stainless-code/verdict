package postcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const providerGitHub = "github"

// providerGitLab is the second host with a submit path: GitLab carries a
// review as one discussion per finding, one summary note, and an approve.
const providerGitLab = "gitlab"

// providerForgejo is the third host with a submit path: one atomic review over
// the REST API, because Forgejo ships no CLI.
const providerForgejo = "forgejo"

func typedPostItems(entries []any) []core.FindingItem {
	items := make([]core.FindingItem, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sv, _ := m.Get("sig")
		tv, _ := m.Get("status")
		av, _ := m.Get("anchored")
		items = append(items, core.FindingItem{
			Sig:      jq.StrOf(sv),
			Status:   jq.StrOf(tv),
			Anchored: av,
			Locator:  readcommon.Locator(m),
		})
	}
	return items
}

func intOrNilPost(v any) *int {
	if v == nil {
		return nil
	}
	if n, ok := jq.IntOf(v); ok {
		return &n
	}
	return nil
}

func strOrNilPost(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func postDiagramValue(dstat *diagram.Status) any {
	if dstat.State == "absent" {
		return nil
	}
	var keyword, note any
	if dstat.Keyword != "" {
		keyword = dstat.Keyword
	}
	if dstat.Note != "" {
		note = dstat.Note
	}
	return jq.NewObj(
		"status", dstat.State,
		"blocks", dstat.Blocks,
		"keyword", keyword,
		"wrapped", dstat.Wrapped,
		"collapsed", dstat.Collapsed,
		"detail", dstat.Detail,
		"note", note,
	)
}

func postCounts(entries []any) (open, deferred, addressed int) {
	return countByStatus(entries, statusOpen),
		countByStatus(entries, "deferred"),
		countByStatus(entries, "addressed")
}

func countByStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok && m != nil {
			if s, _ := m.Get("status"); s == status {
				n++
			}
		}
	}
	return n
}

func decodeProvenanceTyped(root, dir string) *core.Provenance {
	doc, err := jq.DecodeOrdered([]byte(provenance.BundleProvenance(root, dir)))
	if err != nil {
		return nil
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return nil
	}
	tool, _ := obj.Get("tool_version")
	hash, _ := obj.Get("prompts_hash")
	modelRaw, _ := obj.Get("model")
	var model *string
	if s, ok := modelRaw.(string); ok && s != "" {
		model = &s
	}
	return &core.Provenance{ToolVersion: jq.StrOf(tool), PromptsHash: jq.StrOf(hash), Model: model}
}
