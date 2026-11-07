package calibratecore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (st *state) runLive(ctx context.Context, dir string) *core.RefusalError {
	entries, reviewID, head0, refusal := st.liveBundle(dir)
	if refusal != nil {
		return refusal
	}
	threads, ref := st.liveThreads(ctx)
	if ref != nil {
		return ref
	}
	live, ref := st.livePR(ctx)
	if ref != nil {
		return ref
	}
	changed := st.liveChanged(ctx, head0, liveHead(live))
	outcomes := joinOutcomes(entries, threads, changed)
	st.emitLiveHuman(reviewID, head0, live, outcomes, dir)
	st.live = &liveResult{
		reviewID: reviewID,
		head0:    head0,
		live:     live,
		outcomes: outcomes,
		dir:      dir,
	}
	return nil
}

type liveResult struct { //nolint:govet // field grouping documents the loaded dimensions
	reviewID string
	head0    string
	live     map[string]any
	outcomes []outcome
	dir      string
}

func (st *state) liveBundle(dir string) ([]any, string, string, *core.RefusalError) {
	if isLocalDir(dir) {
		return nil, "", "", st.withWarnings(&core.RefusalError{
			Code: codeNoThreads,
			Message: "calibrate joins live threads — nothing to settle on a local bundle " +
				"(adopt + post first, then calibrate the PR)",
		})
	}
	if prov := readcommon.ProviderOf(dir); prov != "" && prov != "github" {
		return nil, "", "", st.withWarnings(&core.RefusalError{Code: codeProviderUnimplemented, Message: fmt.Sprintf(
			"calibrate on %s is not implemented yet (no live thread join for it)", prov)})
	}
	if !isRegular(dir + postFileSuffix) {
		return nil, "", "", st.withWarnings(&core.RefusalError{Code: codeMissingPost, Message: fmt.Sprintf(
			"missing %s/post.json (post for real first — calibration needs a posted review id)", dir)})
	}
	return st.liveFindings(dir)
}

func (st *state) liveFindings(dir string) ([]any, string, string, *core.RefusalError) {
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		if refusal, ok := errors.AsType[*core.RefusalError](err); ok {
			return nil, "", "", st.withWarnings(refusal)
		}
		return nil, "", "", st.withWarnings(&core.RefusalError{Code: codeMissingPost, Message: "post.json carries no review id/head"})
	}
	reviewID := nestedReviewID(dir)
	head0 := readcommon.JSONRaw(dir+postFileSuffix, "head")
	if reviewID == "" || head0 == "" {
		return nil, "", "", st.withWarnings(&core.RefusalError{Code: codeMissingPost, Message: "post.json carries no review id/head"})
	}
	return entries, reviewID, head0, nil
}

func isLocalDir(dir string) bool {
	return readcommon.IsLocalBundleDir(dir)
}

func nestedReviewID(dir string) string {
	raw, err := os.ReadFile(dir + postFileSuffix)
	if err != nil {
		return ""
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	rev, ok := m["review"].(map[string]any)
	if !ok {
		return ""
	}
	id, ok := rev["id"]
	if !ok || id == nil || id == false {
		return ""
	}
	return jq.R(id)
}

func (st *state) liveThreads(ctx context.Context) ([]any, *core.RefusalError) {
	owner, name := readcommon.SplitOwner(st.req.Repo)
	nodes, _, err := readcommon.Threads(ctx, st.commander(), owner, name, st.req.Pr, true)
	if err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: "could not read live review threads"})
	}
	return nodes, nil
}

func (st *state) livePR(ctx context.Context) (map[string]any, *core.RefusalError) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(),
		"pr", "view", st.req.Pr, "--repo", st.req.Repo, "--json", "headRefOid,state,mergedAt")
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codePRReadFailed, Message: "could not read live PR state"})
	}
	if m, ok := decodeLivePR(stdout); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

func decodeLivePR(stdout string) (map[string]any, bool) {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	return m, true
}

func liveHead(live map[string]any) string {
	return jq.StrOf(live["headRefOid"])
}

func (st *state) liveChanged(ctx context.Context, head0, liveHead string) []any {
	if head0 == liveHead {
		return []any{}
	}
	stdout, _, ok := coreexec.Gh(ctx, st.commander(),
		"api", "repos/"+st.req.Repo+"/compare/"+head0+"..."+liveHead, "--jq", "[.files[].filename]")
	if !ok {
		st.warn("compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed")
		return []any{}
	}
	doc, derr := jq.Decode([]byte(stdout))
	if derr != nil {
		st.warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		st.warn("compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed")
		return []any{}
	}
	list, ok := doc.([]any)
	if !ok {
		st.warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	return list
}

type outcome struct {
	entry   any
	kb      any
	sig     string
	status  string
	sev     string
	cat     string
	locator string
	result  string
	detail  string
	hasKb   bool
}

func joinOutcomes(entries, threads, changed []any) []outcome {
	var out []outcome
	for _, e := range entries {
		status := readcommon.StatusOf(e)
		if status != statusOpen && status != statusAddressed && status != statusDeferred {
			continue
		}
		out = append(out, joinOne(e, threads, changed))
	}
	return out
}

func joinOne(entry any, threads, changed []any) outcome {
	out := outcome{
		entry:   entry,
		sig:     sigHuman(entry),
		status:  readcommon.StatusOf(entry),
		sev:     severityOf(entry),
		cat:     core.CategoryOf(entry),
		locator: locatorFor(entry),
	}
	kind, ok := readcommon.FieldOf(entry, "kind").(string)
	if !ok {
		kind = ""
	}
	if kind != "inline" {
		out.result = outcomeUnknown
		out.detail = "body section — not a thread comment"
		withKb(&out, entry)
		return out
	}
	pb := postedBody(entry)
	hit := findThread(threads, pb, readcommon.FieldOf(entry, "path"))
	if hit == nil {
		out.result = outcomeUnknown
		out.detail = "comment not found live"
		withKb(&out, entry)
		return out
	}
	if !readcommon.ThreadResolved(hit) {
		out.result = statusOpen
		out.detail = "thread still open"
		withKb(&out, entry)
		return out
	}
	if pathChanged(changed, readcommon.FieldOf(entry, "path")) {
		out.result = outcomeFixed
		out.detail = "thread resolved + cited file changed since review head"
		withKb(&out, entry)
		return out
	}
	out.result = outcomeResolvedUnfixed
	out.detail = "thread resolved but cited file unchanged since review head"
	withKb(&out, entry)
	return out
}

func withKb(out *outcome, entry any) {
	kb := readcommon.FieldOf(entry, "kb")
	if kb == nil {
		return
	}
	if kbLen(kb) > 0 {
		out.kb = kb
		out.hasKb = true
	}
}

func kbLen(kb any) float64 {
	switch t := kb.(type) {
	case nil:
		return 0
	case bool:
		return 0
	case string:
		return float64(len([]rune(t)))
	case []any:
		return float64(len(t))
	case map[string]any:
		return float64(len(t))
	case *jq.Obj:
		return float64(len(t.Keys))
	default:
		if f, ok := readcommon.NumVal(kb); ok {
			if f < 0 {
				return -f
			}
			return f
		}
		return 0
	}
}

// locatorFor owns the outcome locator: the ranked locator on objects,
// "?" otherwise (unreachable post-guard, kept for totality).
func locatorFor(entry any) string {
	m, ok := entry.(*jq.Obj)
	if ok && m != nil {
		return readcommon.Locator(entry)
	}
	return "?"
}

func severityOf(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return outcomeUntagged
	}
	sev, _ := m.Get("severity")
	if s, ok := sev.(string); ok {
		switch s {
		case "critical", "important", "informational":
			return s
		}
	}
	return outcomeUntagged
}

func postedBody(entry any) string {
	body, ok := readcommon.FieldOf(entry, "body").(string)
	if !ok {
		body = ""
	}
	suggestion, ok := readcommon.FieldOf(entry, "suggestion").(string)
	if !ok {
		suggestion = ""
	}
	if suggestion != "" {
		return body + "\n\n```suggestion\n" + suggestion + "\n```"
	}
	return body
}

func findThread(threads []any, pb string, path any) any {
	for _, t := range threads {
		if threadMatches(t, pb, path) {
			return t
		}
	}
	return nil
}

func threadMatches(t any, pb string, path any) bool {
	m, ok := t.(map[string]any)
	if !ok {
		return false
	}
	for _, c := range commentNodes(m["comments"]) {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if readcommon.Cmp(cm["body"], pb) == 0 && readcommon.Cmp(cm["path"], path) == 0 {
			return true
		}
	}
	return false
}

func commentNodes(comments any) []any {
	switch t := comments.(type) {
	case map[string]any:
		nodes, ok := t["nodes"].([]any)
		if !ok {
			return nil
		}
		return nodes
	case []any:
		return t
	default:
		return nil
	}
}

func pathChanged(changed []any, path any) bool {
	if path == nil {
		return false
	}
	for _, c := range changed {
		if readcommon.Cmp(c, path) == 0 {
			return true
		}
	}
	return false
}

type precision struct {
	byTier          map[string]map[string]int
	byCategory      map[string]*categoryCell
	fixed           int
	resolvedUnfixed int
	open            int
	unknown         int
	judged          int
}

// categoryCell is one per-category precision bucket: the outcome counts plus
// the judged denominator. The resolution rate renders at wire time so a zero
// denominator reads null instead of zero.
type categoryCell struct {
	fixed           int
	resolvedUnfixed int
	open            int
	unknown         int
}

func (c *categoryCell) judged() int {
	if c == nil {
		return 0
	}
	return c.fixed + c.resolvedUnfixed + c.open
}

func countPrecision(outcomes []outcome) precision {
	p := precision{byTier: map[string]map[string]int{}, byCategory: map[string]*categoryCell{}}
	ensure := func(tier string) map[string]int {
		if _, ok := p.byTier[tier]; !ok {
			p.byTier[tier] = map[string]int{
				outcomeFixed: 0, tierResolvedUnfixed: 0, statusOpen: 0, outcomeUnknown: 0,
			}
		}
		return p.byTier[tier]
	}
	for i := range outcomes {
		o := &outcomes[i]
		tier := ensure(o.sev)
		cell := ensureCategory(&p, o.cat)
		switch o.result {
		case outcomeFixed:
			p.fixed++
			tier[outcomeFixed]++
			cell.fixed++
		case outcomeResolvedUnfixed:
			p.resolvedUnfixed++
			tier[tierResolvedUnfixed]++
			cell.resolvedUnfixed++
		case statusOpen:
			p.open++
			tier[statusOpen]++
			cell.open++
		default:
			p.unknown++
			tier[outcomeUnknown]++
			cell.unknown++
		}
	}
	p.judged = p.fixed + p.resolvedUnfixed + p.open
	return p
}

// ensureCategory returns the precision bucket for one outcome's category,
// creating it on first sight so only observed categories are emitted.
func ensureCategory(p *precision, cat string) *categoryCell {
	cell, ok := p.byCategory[cat]
	if !ok {
		cell = &categoryCell{}
		p.byCategory[cat] = cell
	}
	return cell
}

func (st *state) emitLiveHuman(reviewID, head0 string, live map[string]any, outcomes []outcome, dir string) {
	p := countPrecision(outcomes)
	head := liveHead(live)
	prState := jq.StrOf(live["state"])
	mergedAt := jq.StrOf(live["mergedAt"])
	stale := "current"
	if head0 != head {
		stale = "stale — " + readcommon.ShortSHA(head0) + " → " + readcommon.ShortSHA(head)
	}
	merged := ""
	if mergedAt != "" {
		merged = ", merged " + mergedAt
	}
	st.human(fmt.Sprintf("calibrate: %s#%s — review %s @ %s (%s), PR %s%s",
		st.req.Repo, st.req.Pr, reviewID, readcommon.ShortSHA(head0), stale, prState, merged))
	for i := range outcomes {
		o := &outcomes[i]
		kbText := ""
		if showKbSuffix(o.kb) {
			kbText = kbRowText(o)
		}
		st.human(fmt.Sprintf("%s [%s] %s — %s%s", o.sig, o.result, o.locator, o.detail, kbText))
	}
	st.human(fmt.Sprintf("precision: fixed=%d resolved-unfixed=%d open=%d unknown=%d (judged %d)%s",
		p.fixed, p.resolvedUnfixed, p.open, p.unknown, p.judged, precisionCategorySuffix(&p)))
	st.human(fmt.Sprintf("provenance: tool v%s prompts %s model %s",
		provOrUnmarked(dir, "tool_version"), provHashPrefix(dir), provOrUnmarked(dir, "model")))
}

// precisionCategorySuffix names per-category fixed/judged pairs once more
// than one category shows up, bounded to four names plus an other count so
// the line stays readable on wide portfolios.
func precisionCategorySuffix(p *precision) string {
	if len(p.byCategory) < 2 {
		return ""
	}
	names := make([]string, 0, len(p.byCategory))
	for name := range p.byCategory {
		names = append(names, name)
	}
	slices.Sort(names)
	const maxNamed = 4
	parts := make([]string, 0, maxNamed+1)
	for i, name := range names {
		if i >= maxNamed {
			parts = append(parts, fmt.Sprintf("other %d", len(names)-maxNamed))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d", name, p.byCategory[name].fixed, p.byCategory[name].judged()))
	}
	return ", " + strings.Join(parts, ", ")
}

func sigHuman(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return "null"
	}
	sig, _ := m.Get("sig")
	if s, ok := sig.(string); ok {
		return readcommon.Prefix(s)
	}
	return "null"
}

func showKbSuffix(kb any) bool {
	if kb == nil {
		return true
	}
	_, ok := kb.([]any)
	return ok
}

func kbRowText(o *outcome) string {
	vals := []any{outcomeSig(o), o.status, o.sev, o.locator, o.result, o.detail}
	if o.hasKb {
		vals = append(vals, o.kb)
	}
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, readcommon.Tostring(v))
	}
	return " kb:" + strings.Join(parts, ",")
}

func provOrUnmarked(dir, key string) string {
	raw, err := os.ReadFile(dir + postFileSuffix)
	if err != nil {
		return unmarkedValue
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return unmarkedValue
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return unmarkedValue
	}
	v, ok := m[key]
	if !ok || v == nil {
		return unmarkedValue
	}
	return jq.Str(v)
}

func provHashPrefix(dir string) string {
	hash := provRaw(dir, "prompts_hash")
	if hash == "" {
		return unmarkedValue
	}
	runes := []rune(hash)
	if len(runes) > provHashRunes {
		runes = runes[:provHashRunes]
	}
	return string(runes)
}

const provHashRunes = 12

func provRaw(dir, key string) string {
	raw, err := os.ReadFile(dir + postFileSuffix)
	if err != nil {
		return ""
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return readcommon.Tostring(v)
}

func nextLive(p precision) string {
	next := "calibration recorded"
	if p.open > 0 {
		next = "address open threads"
	} else if p.unknown > 0 {
		next = "re-run after threads settle"
	}
	return next
}

// Wire renders the live join data in published wire order.
func (r *liveResult) Wire() *jq.Obj {
	p := countPrecision(r.outcomes)
	items := make([]any, 0, len(r.outcomes))
	for i := range r.outcomes {
		items = append(items, outcomeDoc(&r.outcomes[i]))
	}
	byTier := jq.NewObj()
	for _, tier := range sortedTierKeys(p.byTier) {
		counts := p.byTier[tier]
		byTier.Set(tier, jq.NewObj(
			outcomeFixed, counts[outcomeFixed],
			tierResolvedUnfixed, counts[tierResolvedUnfixed],
			statusOpen, counts[statusOpen],
			outcomeUnknown, counts[outcomeUnknown],
		))
	}
	byCategory := jq.NewObj()
	for _, name := range sortedCategoryKeys(p.byCategory) {
		cell := p.byCategory[name]
		var rate any
		if judged := cell.judged(); judged > 0 {
			rate = float64(cell.fixed) / float64(judged)
		}
		byCategory.Set(name, jq.NewObj(
			outcomeFixed, cell.fixed,
			tierResolvedUnfixed, cell.resolvedUnfixed,
			statusOpen, cell.open,
			outcomeUnknown, cell.unknown,
			"judged", cell.judged(),
			"resolution_rate", rate,
		))
	}
	var mergedAt any
	if merged := jq.StrOf(r.live["mergedAt"]); merged != "" {
		mergedAt = merged
	}
	return jq.NewObj(
		"review_id", reviewIDValue(r.reviewID),
		"head", jq.NewObj("reviewed", r.head0, "live", liveHead(r.live)),
		"pr", jq.NewObj("state", jq.StrOf(r.live["state"]), "merged_at", mergedAt),
		"outcomes", items,
		"precision", jq.NewObj(
			"fixed", p.fixed,
			"resolved_unfixed", p.resolvedUnfixed,
			"open", p.open,
			"unknown", p.unknown,
			"judged", p.judged,
			"by_tier", byTier,
			"by_category", byCategory,
		),
		"provenance", jq.NewObj(
			"tool_version", provValue(r.dir, "tool_version"),
			"prompts_hash", provValue(r.dir, "prompts_hash"),
			"model", provValue(r.dir, "model"),
		),
	)
}

func outcomeDoc(o *outcome) *jq.Obj {
	doc := jq.NewObj(
		"sig", outcomeSig(o),
		"status", o.status,
		"severity", o.sev,
		"category", outcomeCategory(o),
		"locator", o.locator,
		"outcome", o.result,
		"detail", o.detail,
	)
	if o.hasKb {
		doc.Set("kb", o.kb)
	}
	return doc
}

// outcomeCategory reads the row's category for the audit: the label when
// published, null when the finding is untagged.
func outcomeCategory(o *outcome) any {
	if o.cat == "" || o.cat == core.CategoryUntagged {
		return nil
	}
	return o.cat
}

func outcomeSig(o *outcome) any {
	m, ok := o.entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	sig, _ := m.Get("sig")
	return sig
}

func sortedTierKeys(byTier map[string]map[string]int) []string {
	keys := make([]string, 0, len(byTier))
	for k := range byTier {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedCategoryKeys orders one precision map's buckets for stable wire.
func sortedCategoryKeys(byCategory map[string]*categoryCell) []string {
	keys := make([]string, 0, len(byCategory))
	for k := range byCategory {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

const idFloatBits = 64

func reviewIDValue(rid string) any {
	if f, err := strconv.ParseFloat(rid, idFloatBits); err == nil {
		return f
	}
	return rid
}

func provValue(dir, key string) any {
	raw, err := os.ReadFile(dir + postFileSuffix)
	if err != nil {
		return nil
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	return v
}
