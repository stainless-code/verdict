package calibrate

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runLive ports the live-ground-truth join: thread states plus files
// changed since the review head, with tier precision.
func runLive(ctx context.Context, r *cli.Runner, req Request, dir string) int {
	if isLocalDir(dir) {
		return r.Refuse(&cli.RefusalError{Code: "no-threads", Message: "calibrate joins live threads — nothing to settle on a local bundle (adopt + post first, then calibrate the PR)"})
	}
	if prov := readcommon.ProviderOf(dir); prov != "" && prov != "github" {
		return r.Refuse(&cli.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf("calibrate on %s is not implemented yet (no live thread join for it)", prov)})
	}
	if !isRegular(dir + "/post.json") {
		return r.Refuse(&cli.RefusalError{Code: "missing-post", Message: fmt.Sprintf("missing %s/post.json (post for real first — calibration needs a posted review id)", dir)})
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return r.Refuse(err)
	}
	reviewID := nestedReviewID(dir)
	head0 := readcommon.JsonRaw(dir+"/post.json", "head")
	if reviewID == "" || head0 == "" {
		return r.Refuse(&cli.RefusalError{Code: "missing-post", Message: "post.json carries no review id/head"})
	}
	threads, err := liveThreads(ctx, req)
	if err != nil {
		return r.Refuse(err)
	}
	live, err := livePR(ctx, req)
	if err != nil {
		return r.Refuse(err)
	}
	changed := liveChanged(ctx, r, req, head0, liveHead(live))
	outcomes := joinOutcomes(entries, threads, changed)
	emitLiveHuman(r, req, reviewID, head0, live, outcomes, dir)
	if r.JSONMode {
		emitLiveEnvelope(r, req, reviewID, head0, live, outcomes, dir)
	}
	return 0
}

// isLocalDir reports whether dir is a local bundle.
func isLocalDir(dir string) bool {
	return strings.HasPrefix(dir, ".review/local-")
}

// nestedReviewID reads the nested review.id with `jq -r` rendering ("" when
// absent, null, or false).
func nestedReviewID(dir string) string {
	raw, err := os.ReadFile(dir + "/post.json")
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
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

// liveThreads reads the live review threads with comments.
func liveThreads(ctx context.Context, req Request) ([]any, error) {
	owner, name := readcommon.SplitOwner(req.Repo)
	res, err := readcommon.Gh(ctx, readcommon.ThreadsArgv(owner, name, req.Pr, readcommon.QueryCalibrate)...)
	if err != nil {
		return nil, &cli.RefusalError{Code: "threads-failed", Message: "could not read live review threads"}
	}
	nodes, err := readcommon.SlurpThreads(res.Stdout)
	if err != nil {
		return nil, &cli.RefusalError{Code: "threads-failed", Message: "could not read live review threads"}
	}
	return nodes, nil
}

// livePR reads the live PR state.
func livePR(ctx context.Context, req Request) (map[string]any, error) {
	res, err := readcommon.Gh(ctx, "pr", "view", req.Pr, "--repo", req.Repo, "--json", "headRefOid,state,mergedAt")
	if err != nil {
		return nil, &cli.RefusalError{Code: "pr-read-failed", Message: "could not read live PR state"}
	}
	doc, err := jq.Decode([]byte(res.Stdout))
	if err != nil {
		return map[string]any{}, nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return map[string]any{}, nil
	}
	return m, nil
}

// liveHead reads the live head SHA.
func liveHead(live map[string]any) string {
	return jq.StrOf(live["headRefOid"])
}

// liveChanged ports the changed-file evidence: the compare file list, or
// empty with a warning when the compare is unavailable.
func liveChanged(ctx context.Context, r *cli.Runner, req Request, head0, liveHead string) []any {
	if head0 == liveHead {
		return []any{}
	}
	res, err := readcommon.Gh(ctx, "api", "repos/"+req.Repo+"/compare/"+head0+"..."+liveHead, "--jq", "[.files[].filename]")
	if err != nil {
		r.Warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	doc, derr := jq.Decode([]byte(res.Stdout))
	if derr != nil {
		r.Warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	list, ok := doc.([]any)
	if !ok {
		r.Warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	return list
}

// outcome is one joined finding outcome.
type outcome struct {
	entry   any
	sig     string
	status  string
	sev     string
	locator string
	result  string
	detail  string
	kb      any
	hasKb   bool
}

// joinOutcomes ports the outcomes join over open, addressed, and deferred
// findings.
func joinOutcomes(entries, threads, changed []any) []outcome {
	var out []outcome
	for _, e := range entries {
		status := readcommon.StatusOf(e)
		if status != "open" && status != "addressed" && status != "deferred" {
			continue
		}
		out = append(out, joinOne(e, threads, changed))
	}
	return out
}

// joinOne ports one outcome row.
func joinOne(entry any, threads, changed []any) outcome {
	locator := ""
	m, _ := entry.(*jq.Obj)
	if m != nil {
		locator = readcommon.Locator(entry)
	} else {
		locator = locatorOf(entry)
	}
	out := outcome{
		entry:   entry,
		sig:     sigString(entry),
		status:  readcommon.StatusOf(entry),
		sev:     severityOf(entry),
		locator: locator,
	}
	kind, _ := fieldOf(entry, "kind").(string)
	if kind != "inline" {
		out.result = "unknown"
		out.detail = "body section — not a thread comment"
		return withKb(out, entry)
	}
	pb := postedBody(entry)
	hit := findThread(threads, pb, fieldOf(entry, "path"))
	if hit == nil {
		out.result = "unknown"
		out.detail = "comment not found live"
		return withKb(out, entry)
	}
	if !threadResolved(hit) {
		out.result = "open"
		out.detail = "thread still open"
		return withKb(out, entry)
	}
	if pathChanged(changed, fieldOf(entry, "path")) {
		out.result = "fixed"
		out.detail = "thread resolved + cited file changed since review head"
		return withKb(out, entry)
	}
	out.result = "resolved-unfixed"
	out.detail = "thread resolved but cited file unchanged since review head"
	return withKb(out, entry)
}

// withKb attaches the cited kb value when it reads non-empty (jq length
// semantics with booleans reading 0, so the shell's length never errors).
func withKb(out outcome, entry any) outcome {
	kb := fieldOf(entry, "kb")
	if kb == nil {
		return out
	}
	if kbLen(kb) > 0 {
		out.kb = kb
		out.hasKb = true
	}
	return out
}

// kbLen ports the safe kb length: booleans read 0, everything else reads
// jq length.
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

// locatorOf renders a fallback locator for non-object entries (unreachable
// post-guard, kept for totality).
func locatorOf(entry any) string {
	return "?"
}

// severityOf ports the outcome severity (untagged default).
func severityOf(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return "untagged"
	}
	sev, _ := m.Get("severity")
	if s, ok := sev.(string); ok {
		switch s {
		case "critical", "important", "informational":
			return s
		}
	}
	return "untagged"
}

// fieldOf reads one entry field (nil-safe for null entries).
func fieldOf(entry any, key string) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

// postedBody ports the posted-body match key: body plus the fenced
// suggestion block. Bodies and suggestions read raw (the entry guard keeps
// them null-or-string, so only a present non-empty string appends).
func postedBody(entry any) string {
	body, _ := fieldOf(entry, "body").(string)
	suggestion, _ := fieldOf(entry, "suggestion").(string)
	if suggestion != "" {
		return body + "\n\n```suggestion\n" + suggestion + "\n```"
	}
	return body
}

// findThread finds the first thread with a matching comment.
func findThread(threads []any, pb string, path any) any {
	for _, t := range threads {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		comments := commentNodes(m["comments"])
		for _, c := range comments {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if readcommon.Cmp(cm["body"], pb) == 0 && readcommon.Cmp(cm["path"], path) == 0 {
				return t
			}
		}
	}
	return nil
}

// commentNodes ports `.comments[]` over a thread: the nodes list for the
// usual object shape, the elements for an array shape, nothing otherwise.
func commentNodes(comments any) []any {
	switch t := comments.(type) {
	case map[string]any:
		nodes, _ := t["nodes"].([]any)
		return nodes
	case []any:
		return t
	default:
		return nil
	}
}

// commentStr renders a thread field for comparison.
func commentStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return jq.Str(v)
}

// threadResolved reports the thread's resolution flag.
func threadResolved(thread any) bool {
	m, ok := thread.(map[string]any)
	if !ok {
		return false
	}
	resolved, _ := m["isResolved"].(bool)
	return resolved
}

// pathChanged reports whether the cited path changed since review head.
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

// precision is the outcome tallies plus the per-tier split.
type precision struct {
	fixed           int
	resolvedUnfixed int
	open            int
	unknown         int
	judged          int
	byTier          map[string]map[string]int
}

// countPrecision tallies outcomes.
func countPrecision(outcomes []outcome) precision {
	p := precision{byTier: map[string]map[string]int{}}
	ensure := func(tier string) map[string]int {
		if _, ok := p.byTier[tier]; !ok {
			p.byTier[tier] = map[string]int{"fixed": 0, "resolved_unfixed": 0, "open": 0, "unknown": 0}
		}
		return p.byTier[tier]
	}
	for _, o := range outcomes {
		tier := ensure(o.sev)
		switch o.result {
		case "fixed":
			p.fixed++
			tier["fixed"]++
		case "resolved-unfixed":
			p.resolvedUnfixed++
			tier["resolved_unfixed"]++
		case "open":
			p.open++
			tier["open"]++
		default:
			p.unknown++
			tier["unknown"]++
		}
	}
	p.judged = p.fixed + p.resolvedUnfixed + p.open
	return p
}

// emitLiveHuman ports the live human lines plus the provenance tail.
func emitLiveHuman(r *cli.Runner, req Request, reviewID, head0 string, live map[string]any, outcomes []outcome, dir string) {
	p := countPrecision(outcomes)
	liveHead := liveHead(live)
	prState := jq.StrOf(live["state"])
	mergedAt := jq.StrOf(live["mergedAt"])
	stale := "current"
	if head0 != liveHead {
		stale = "stale — " + readcommon.ShortSHA(head0) + " → " + readcommon.ShortSHA(liveHead)
	}
	merged := ""
	if mergedAt != "" {
		merged = ", merged " + mergedAt
	}
	fmt.Fprintf(r.Human, "calibrate: %s#%s — review %s @ %s (%s), PR %s%s\n",
		req.Repo, req.Pr, reviewID, readcommon.ShortSHA(head0), stale, prState, merged)
	for _, o := range outcomes {
		kbText := ""
		if list, ok := o.kb.([]any); ok && len(list) > 0 {
			kbText = " kb:" + kbJoin(list)
		}
		fmt.Fprintf(r.Human, "%s [%s] %s — %s%s\n", o.sig, o.result, o.locator, o.detail, kbText)
	}
	fmt.Fprintf(r.Human, "precision: fixed=%d resolved-unfixed=%d open=%d unknown=%d (judged %d)\n",
		p.fixed, p.resolvedUnfixed, p.open, p.unknown, p.judged)
	fmt.Fprintf(r.Human, "provenance: tool v%s prompts %s model %s\n",
		provOrUnmarked(dir, "tool_version"), provHashPrefix(dir), provOrUnmarked(dir, "model"))
}

// kbJoin renders the cited rule ids, one tostring per element.
func kbJoin(list []any) string {
	parts := make([]string, 0, len(list))
	for _, id := range list {
		parts = append(parts, readcommon.Tostring(id))
	}
	return strings.Join(parts, ",")
}

// provOrUnmarked reads one provenance field with the unmarked fallback.
func provOrUnmarked(dir, key string) string {
	raw, err := os.ReadFile(dir + "/post.json")
	if err != nil {
		return "unmarked"
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return "unmarked"
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return "unmarked"
	}
	v, ok := m[key]
	if !ok || v == nil {
		return "unmarked"
	}
	return jq.Str(v)
}

// provHashPrefix reads the prompts hash prefix with the unmarked fallback.
func provHashPrefix(dir string) string {
	hash := provRaw(dir, "prompts_hash")
	if hash == "" {
		return "unmarked"
	}
	runes := []rune(hash)
	if len(runes) > provHashRunes {
		runes = runes[:provHashRunes]
	}
	return string(runes)
}

// provHashRunes caps the provenance hash display.
const provHashRunes = 12

// provRaw reads one raw provenance value as a string.
func provRaw(dir, key string) string {
	raw, err := os.ReadFile(dir + "/post.json")
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
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

// emitLiveEnvelope ports the live --json envelope.
func emitLiveEnvelope(r *cli.Runner, req Request, reviewID, head0 string, live map[string]any, outcomes []outcome, dir string) {
	p := countPrecision(outcomes)
	items := make([]any, 0, len(outcomes))
	for _, o := range outcomes {
		items = append(items, outcomeDoc(o))
	}
	byTier := jq.NewObj()
	for _, tier := range sortedTierKeys(p.byTier) {
		counts := p.byTier[tier]
		byTier.Set(tier, jq.NewObj(
			"fixed", counts["fixed"],
			"resolved_unfixed", counts["resolved_unfixed"],
			"open", counts["open"],
			"unknown", counts["unknown"],
		))
	}
	var mergedAt any
	if merged := jq.StrOf(live["mergedAt"]); merged != "" {
		mergedAt = merged
	}
	prState := jq.StrOf(live["state"])
	next := "calibration recorded"
	if p.open > 0 {
		next = "address open threads"
	} else if p.unknown > 0 {
		next = "re-run after threads settle"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "calibrate",
		"repo", req.Repo,
		"pr", cli.PrJSON(req.Pr),
		"at", cli.NowUTC(r.Now()),
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"review_id", reviewIDValue(reviewID),
			"head", jq.NewObj("reviewed", head0, "live", liveHead(live)),
			"pr", jq.NewObj("state", prState, "merged_at", mergedAt),
			"outcomes", items,
			"precision", jq.NewObj(
				"fixed", p.fixed,
				"resolved_unfixed", p.resolvedUnfixed,
				"open", p.open,
				"unknown", p.unknown,
				"judged", p.judged,
				"by_tier", byTier,
			),
			"provenance", jq.NewObj(
				"tool_version", provValue(dir, "tool_version"),
				"prompts_hash", provValue(dir, "prompts_hash"),
				"model", provValue(dir, "model"),
			),
		),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// outcomeDoc renders one outcome row in shell key order: sig, status,
// severity, locator, outcome, detail, plus kb when cited.
func outcomeDoc(o outcome) *jq.Obj {
	doc := jq.NewObj(
		"sig", outcomeSig(o),
		"status", o.status,
		"severity", o.sev,
		"locator", o.locator,
		"outcome", o.result,
		"detail", o.detail,
	)
	if o.hasKb {
		doc.Set("kb", o.kb)
	}
	return doc
}

// outcomeSig renders the outcome sig raw.
func outcomeSig(o outcome) any {
	m, ok := o.entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	sig, _ := m.Get("sig")
	return sig
}

// sortedTierKeys sorts tiers in jq group_by order (byte order).
func sortedTierKeys(byTier map[string]map[string]int) []string {
	keys := make([]string, 0, len(byTier))
	for k := range byTier {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// reviewIDValue ports ($rid | tonumber? // $rid): numeric ids read as
// numbers, everything else reads raw.
func reviewIDValue(rid string) any {
	if f, err := strconv.ParseFloat(rid, 64); err == nil {
		return f
	}
	return rid
}

// provValue reads one envelope provenance value (null when missing).
func provValue(dir, key string) any {
	raw, err := os.ReadFile(dir + "/post.json")
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
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
