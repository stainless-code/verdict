package postcore

import (
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/sig"
)

// providerGitHub is the default provider marker.
const providerGitHub = "github"

// sigSumOf seals the payload hash for the duplicate guard.
func sigSumOf(b []byte) string { return sig.Sum(b) }

// stOpen passes the open count through (kept as a helper so the dry-run
// response reads symmetrically with the envelope tally).
func stOpen(n int) int { return n }

// typedPostItems builds the typed envelope summaries alongside the ordered
// items the wire renders verbatim.
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
		pv, _ := m.Get("path")
		lv, _ := m.Get("line")
		hv, _ := m.Get("heading")
		items = append(items, core.FindingItem{
			Sig:      jq.StrOf(sv),
			Status:   jq.StrOf(tv),
			Anchored: av,
			Locator:  itemLocatorPost(pv, lv, hv),
		})
	}
	return items
}

// itemLocatorPost renders an entry's path:line or heading fallback.
func itemLocatorPost(pv, lv, hv any) string {
	if jq.Falsey(pv) {
		return jq.Interp(jq.Alt(hv, "?"))
	}
	locator := jq.Interp(pv)
	if !jq.Falsey(lv) {
		locator += ":" + jq.Interp(lv)
	}
	return locator
}

// intOrNilPost reads an agent confidence as an int (nil when absent).
func intOrNilPost(v any) *int {
	if v == nil {
		return nil
	}
	if n, ok := jq.IntOf(v); ok {
		return &n
	}
	return nil
}

// strOrNilPost reads an agent security summary as a string (nil when absent).
func strOrNilPost(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// postDiagramValue ports the dry-run diagram disposition: null when the body
// carried no candidates, else the validated shape with null keyword/note.
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

// postCounts tallies the merged findings for preflight and envelopes.
func postCounts(entries []any) (open, deferred, addressed int) {
	return countByStatus(entries, statusOpen),
		countByStatus(entries, "deferred"),
		countByStatus(entries, "addressed")
}

// countByStatus counts entries with status.
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

// postProvider ports the provider read with the shell's github default:
// missing, unreadable, or undecodable PR.json reads github (only the
// explicit provider marker routes elsewhere).
func postProvider(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return providerGitHub
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return providerGitHub
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return providerGitHub
	}
	prov, _ := m.Get("provider")
	return jq.R(jq.Alt(prov, providerGitHub))
}

// prState ports pr_field's state read: GitHub states lowercase, provider
// states verbatim, "" when unknown.
func prState(dir string) string {
	m := prDoc(dir)
	if m == nil {
		return ""
	}
	prov, _ := m.Get("provider")
	if s, ok := prov.(string); ok && s != "" {
		state, _ := m.Get("state")
		return prScalar(state)
	}
	state, _ := m.Get("state")
	return strings.ToLower(prScalar(state))
}

// prAuthor ports pr_field's author read: `.author.login` on GitHub bundles,
// `.author` on provider bundles, "" when unknown.
func prAuthor(dir string) string {
	m := prDoc(dir)
	if m == nil {
		return ""
	}
	prov, _ := m.Get("provider")
	if s, ok := prov.(string); ok && s != "" {
		author, _ := m.Get("author")
		return prScalar(author)
	}
	return prAuthorLogin(m)
}

// prAuthorLogin reads the GitHub author login from a decoded bundle record.
func prAuthorLogin(m *jq.Obj) string {
	author, _ := m.Get("author")
	am, ok := author.(*jq.Obj)
	if !ok || am == nil {
		return ""
	}
	login, _ := am.Get("login")
	return prScalar(login)
}

// prDoc decodes the bundle PR record, or nil when it is missing or
// undecodable (callers degrade to defaults, like the shell's `|| true`).
func prDoc(dir string) *jq.Obj {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	return m
}

// prScalar renders a pr_field value the way `jq -r '... // empty'` does:
// missing, null, and false read "", everything else renders -r style.
func prScalar(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.R(v)
}

// postHead ports `jq -r '.head // empty' post.json`: the stored reviewed
// head, with readable reporting whether the record decoded at all (missing,
// unreadable, empty, and non-object records never arm the finalizer).
func postHead(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return "", false
	}
	if doc == nil {
		return "", true
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return "", false
	}
	head, _ := m.Get("head")
	return prScalar(head), true
}

// postTime ports `jq -r '.posted_at // "unknown time"' post.json` for the
// already-posted refusal.
func postTime(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return unknownTime
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return unknownTime
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return unknownTime
	}
	posted, _ := m.Get("posted_at")
	return jq.R(jq.Alt(posted, unknownTime))
}

// decodeProvenanceTyped parses the bundle provenance triple into the typed
// shape for the envelope.
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
	} else if modelRaw != nil {
		// Explicit null stays null (nil pointer renders null).
		model = nil
	}
	return &core.Provenance{ToolVersion: jq.StrOf(tool), PromptsHash: jq.StrOf(hash), Model: model}
}
