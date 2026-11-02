package report

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// readFile reads a whole file.
func readFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read "+path, err)
	}
	return raw, nil
}

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// jsonField reads one string field with || true semantics ("" when the
// file is missing, invalid, or the field is absent).
func jsonField(path, field string) string {
	raw, err := os.ReadFile(path)
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
	return jq.StrOf(m[field])
}

// runLiveProbe ports the live-thread probe: the network-backed first half
// of the gate's third dimension. Advisory runs (no gate flag) skip it with
// zero network; --live skip warns and stays neutral; structural skips
// (local bundles, non-GitHub providers) warn neutral; unreachable lookups
// warn neutral under auto and fail closed under strict.
func runLiveProbe(ctx context.Context, st *state) {
	if !st.gate {
		return
	}
	r := st.runner
	switch {
	case st.flags.LiveMode == "skip":
		r.Warn("live recheck skipped (--live skip requested — gating on local dimensions only)")
		st.live.status = liveSkipExplicit
	case isLocalDir(st.dir):
		r.Warn("live recheck skipped: local bundle threads unreadable here (gating on local dimensions only)")
		st.live.status = liveSkip
	default:
		prov := providerName(st.dir)
		if prov != "" && prov != "github" {
			r.Warn(fmt.Sprintf("live recheck skipped: %s threads unreadable here (gating on local dimensions only)", prov))
			st.live.status = liveSkip
			return
		}
		probeLiveCount(ctx, st)
	}
}

// isLocalDir reports whether dir is a local bundle.
func isLocalDir(dir string) bool {
	return strings.HasPrefix(dir, ".review/local-")
}

// providerName reads the bundle provider ("" when the record is missing).
func providerName(dir string) string {
	return readcommon.ProviderOf(dir)
}

// probeLiveCount runs the live count lookup and maps the outcome.
func probeLiveCount(ctx context.Context, st *state) {
	n, res, err := readcommon.LiveOpenThreadCount(ctx, st.req.Repo, st.req.Pr)
	writeErrFile(st.runner, st.dir+"/.recheck.err", res.Stderr)
	if err != nil {
		probeUnreachable(st, res.Stderr)
		return
	}
	removeErrFile(st.dir + "/.recheck.err")
	if n < 0 {
		n = 0
	}
	st.live.open = n
	if n > st.nAllOpen {
		st.live.status = liveFail
		return
	}
	st.live.status = livePass
}

// probeUnreachable maps a failed lookup to the live posture.
func probeUnreachable(st *state, stderr string) {
	r := st.runner
	removeErrFile(st.dir + "/.recheck.err")
	detail := firstLine(stderr)
	if detail == "" {
		detail = "lookup failed"
	}
	st.live.err = detail
	if st.flags.LiveMode == "strict" {
		r.Warn(fmt.Sprintf("live thread recheck unreachable (%s — offline posture is strict, failing closed)", detail))
		st.live.status = liveUnknownStrict
		return
	}
	r.Warn(fmt.Sprintf("live thread recheck unreachable (%s — gating on local dimensions only)", detail))
	st.live.status = liveUnknown
}

// firstLine ports `head -c 120 | tr '\n' ' '`: the first 120 bytes with
// newlines flattened to spaces.
func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// errHeadBytes caps the probe error detail.
const errHeadBytes = 120

// recheckStagePerm stages probe stderr readable only by the owner: the
// content can carry token-adjacent gh diagnostics.
const recheckStagePerm = 0o600

// writeErrFile stages probe stderr beside the bundle (removed after the
// probe either way, so readers stay side-effect free). The write is
// best-effort like the shell's 2> redirect, so a failure warns instead of
// refusing: the file is a debug side channel and the run must not fail
// over garnish.
func writeErrFile(r *cli.Runner, path, stderr string) {
	if err := os.WriteFile(path, []byte(stderr), recheckStagePerm); err != nil {
		// Un-actionable beyond the notice: the live data already loaded.
		fmt.Fprintf(r.Diag, "report: could not stage probe stderr in %s: %v\n", path, err)
	}
}

// removeErrFile removes a staged probe file.
func removeErrFile(path string) {
	_ = os.Remove(path)
}

// completeness ports the HEAD-reviewed dimension: pass iff a completed
// post record exists, parses, and carries the fetched HEAD. Stays null
// without the flag.
func completeness(dir, headSHA string, req Request) (*jq.Obj, bool, string) {
	var posted string
	if isRegular(dir + "/post.json") {
		posted = jsonField(dir+"/post.json", "head")
	}
	refetch := refetchCommand(dir, req, posted)
	reviewed := headSHA != "" && posted != "" && headSHA == posted
	var fetched, postedVal any
	if headSHA != "" {
		fetched = headSHA
	}
	if posted != "" {
		postedVal = posted
	}
	comp := jq.NewObj(
		"reviewed", reviewed,
		"fetched_head", fetched,
		"posted_head", postedVal,
	)
	_ = refetch
	return comp, reviewed, posted
}

// refetchCommand ports the re-fetch recovery command for the gate tail.
func refetchCommand(dir string, req Request, posted string) string {
	prov := ""
	if isRegular(dir + "/PR.json") {
		prov = jsonField(dir+"/PR.json", "provider")
	}
	if isLocalDir(dir) {
		return fmt.Sprintf("verdict fetch-local %s %s", req.Repo, strings.TrimPrefix(req.Pr, "local-"))
	}
	if prov != "" && prov != "github" {
		return fmt.Sprintf("verdict fetch %s %s --provider %s", req.Repo, req.Pr, prov)
	}
	if posted != "" {
		return fmt.Sprintf("verdict fetch %s %s --base-sha %s", req.Repo, req.Pr, readcommon.ShortSHA(posted))
	}
	return fmt.Sprintf("verdict fetch %s %s", req.Repo, req.Pr)
}

// emitReport prints the --json envelope or the human summary.
func emitReport(st *state) {
	if st.runner.JSONMode {
		emitEnvelope(st)
		return
	}
	emitHuman(st)
}

// emitHuman ports the human summary: the report line, ranked lines, and
// the optional cost block, then the output/next line.
func emitHuman(st *state) {
	r := st.runner
	fmt.Fprintf(r.Human, "report: %s (%d shown) open=%d head=%s\n",
		st.dir, len(st.ranked), st.nOpen, readcommon.ShortSHA(st.headSHA))
	for _, item := range st.ranked {
		fmt.Fprintf(r.Human, "%d. [%s] %s (%s)\n",
			item.Rank, jq.Str(fieldOf(item.Entry, "status")),
			readcommon.Locator(item.Entry), sigHuman(item.Entry))
	}
	if st.flags.Cost {
		emitCostHuman(st)
	}
	if st.flags.Output != "" {
		fmt.Fprintf(r.Human, "wrote %s\n", st.flags.Output)
		return
	}
	if st.nOpen > 0 {
		fmt.Fprintln(r.Human, "next: address open threads")
	} else {
		fmt.Fprintln(r.Human, "next: none open — nothing to address")
	}
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

// sigHuman ports \(.sig[0:8]) for human lines: string sigs slice to 8
// codepoints, everything else renders jq-style ("null" for missing).
func sigHuman(entry any) string {
	if sig, ok := fieldOf(entry, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(fieldOf(entry, "sig"))
}

// emitCostHuman ports the human cost rollup block.
func emitCostHuman(st *state) {
	r := st.runner
	cost := st.cost
	if cost == nil {
		return
	}
	runs := jqStr(cost, "runs")
	if runs == "0" {
		fmt.Fprintf(r.Human, "cost: unrecorded (no usable %s/cost.json — the agent writes one per prompts/review.md)\n", st.dir)
		return
	}
	fmt.Fprintf(r.Human, "cost: %s run(s), %s with records — in=%s out=%s total=%s est_usd=%s cached=%s\n",
		runs, jqStr(cost, "runs_with_cost"), jqStr(cost, "input_tokens"),
		jqStr(cost, "output_tokens"), jqStr(cost, "total_tokens"),
		orNA(jqVal(cost, "est_usd")), jqStr(cost, "cached_input_tokens"))
	byTier, _ := cost.Get("by_tier")
	tierObj, ok := byTier.(*jq.Obj)
	if !ok {
		tierObj = nil
	}
	fmt.Fprintf(r.Human, "tiers: cheap=%s standard=%s deep=%s\n",
		tierRuns(tierObj, "cheap"), tierRuns(tierObj, "standard"), tierRuns(tierObj, "deep"))
	if st.receipts != nil {
		latest := st.receipts[len(st.receipts)-1]
		fmt.Fprintf(r.Human, "receipts: %d consented over-budget run(s) (latest projected $%s %s vs budget $%s @ %s)\n",
			len(st.receipts), receiptField(latest, "projected_usd"),
			receiptField(latest, "projected_tier"), receiptField(latest, "budget_usd"), receiptField(latest, "at"))
	}
}

// jqVal reads one envelope value.
func jqVal(obj *jq.Obj, key string) any {
	v, _ := obj.Get(key)
	return v
}

// jqStr renders one envelope value jq-style.
func jqStr(obj *jq.Obj, key string) string {
	return jq.Str(jqVal(obj, key))
}

// orNA renders a null estimate as n/a.
func orNA(v any) string {
	if v == nil {
		return "n/a"
	}
	return jq.Str(v)
}

// tierRuns reads one tier's run count.
func tierRuns(byTier *jq.Obj, tier string) string {
	if byTier == nil {
		return "0"
	}
	raw, _ := byTier.Get(tier)
	obj, ok := raw.(*jq.Obj)
	if !ok {
		obj = nil
	}
	if obj == nil {
		return "0"
	}
	return jqStr(obj, "runs")
}

// receiptField renders one receipt field jq-style ("?" when absent).
func receiptField(latest any, key string) string {
	if m, ok := latest.(map[string]any); ok {
		return receiptFieldFromMap(m, key)
	}
	return receiptFieldFromObj(latest, key)
}

// receiptFieldFromMap renders one field from a decoded receipt map.
func receiptFieldFromMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil || v == false {
		return "?"
	}
	return jq.Str(v)
}

// receiptFieldFromObj renders one field from a receipt object.
func receiptFieldFromObj(latest any, key string) string {
	om, ok := latest.(*jq.Obj)
	if !ok {
		return "?"
	}
	v, _ := om.Get(key)
	if v == nil || v == false {
		return "?"
	}
	return jq.Str(v)
}

// emitEnvelope ports the --json envelope: the ranked items, counts, panel,
// patch, cost, and completeness data with a null error.
func emitEnvelope(st *state) {
	r := st.runner
	if st.flags.Output != "" {
		fmt.Fprintf(r.Human, "wrote %s\n", st.flags.Output)
	}
	data := jq.NewObj(
		"generated_at", st.now,
		"dir", st.dir,
		"head", jq.NewObj(
			"fetched", st.headSHA,
			"base", st.baseSHA,
			"live", nil,
			"stale", false,
		),
		// The verdict tallies the displayed (filtered) ranking like the
		// shell's $items counts, not the whole store.
		"verdict", jq.NewObj(
			"open", countStatus(rankedEntries(st), "open"),
			"deferred", countStatus(rankedEntries(st), "deferred"),
			"addressed", countStatus(rankedEntries(st), "addressed"),
			"terminal", countTerminal(rankedEntries(st)),
		),
		"counts", st.counts,
		"panel", panelValue(st),
		"patch", st.patch,
		"output", outputVal(st.flags.Output),
		"filters", jq.NewObj(
			"status", st.flags.StatusF,
			"kind", st.flags.KindF,
		),
		"items", envelopeItems(st.ranked),
	)
	if st.cost != nil {
		data.Set("cost", st.cost)
	}
	if st.comp != nil {
		data.Set("completeness", st.comp)
	}
	next := "none open — nothing to address"
	if st.nOpen > 0 {
		next = "address open threads"
	}
	if st.flags.Output != "" {
		next = "wrote " + st.flags.Output
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "report",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", st.now,
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", data,
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// outputVal renders the output path (null when empty).
func outputVal(output string) any {
	if output == "" {
		return nil
	}
	return output
}

// panelValue ports the panel envelope value.
func panelValue(st *state) *jq.Obj {
	var src any
	if st.panelSrc != "" {
		src = st.panelSrc
	}
	var members []any
	if st.panel != nil {
		members = st.panel
	} else {
		members = []any{}
	}
	return jq.NewObj(
		"source", src,
		"members", members,
	)
}

// envelopeItems builds the machine items: ranked entries with locator and
// rank merged in, in shell key order.
func envelopeItems(ranked []readcommon.Ranked) []any {
	items := make([]any, 0, len(ranked))
	for _, item := range ranked {
		items = append(items, envelopeItem(item))
	}
	return items
}

// envelopeItem merges locator and rank into one ranked entry.
func envelopeItem(item readcommon.Ranked) any {
	if m, ok := item.Entry.(*jq.Obj); ok && m != nil {
		clone := m.Clone()
		clone.Set("locator", readcommon.Locator(item.Entry))
		clone.Set("rank", item.Rank)
		return clone
	}
	return jq.NewObj(
		"locator", readcommon.Locator(item.Entry),
		"rank", item.Rank,
	)
}

// rankedEntries flattens the displayed ranking for the verdict tallies.
func rankedEntries(st *state) []any {
	entries := make([]any, 0, len(st.ranked))
	for _, item := range st.ranked {
		entries = append(entries, item.Entry)
	}
	return entries
}

// countStatus counts entries with the given status.
func countStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if readcommon.StatusOf(e) == status {
			n++
		}
	}
	return n
}

// countTerminal counts terminal findings (wont-fix and false-positive,
// mirroring the verdict.terminal key).
func countTerminal(entries []any) int {
	n := 0
	for _, e := range entries {
		if s := readcommon.StatusOf(e); s == "wont-fix" || s == "false-positive" {
			n++
		}
	}
	return n
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// gateTail ports the gate tail: the tier, completeness, and live-thread
// dimensions print check: lines to stderr; any failing dimension exits 1.
func gateTail(st *state) int {
	if !st.gate {
		return 0
	}
	tierFail := gateTierCheck(st)
	compFail := gateCompletenessCheck(st)
	liveFail := gateLiveCheck(st)
	if tierFail || compFail || liveFail {
		return 1
	}
	return 0
}

// gateTierCheck prints the tier dimension check line, reporting whether
// the dimension fails the gate.
func gateTierCheck(st *state) bool {
	if st.flags.CheckSev == "" {
		return false
	}
	if n := tierHits(st.entries, st.flags.CheckSev); n > 0 {
		fmt.Fprintf(st.runner.Diag, "check: %d open finding(s) at-or-above %s — gate fails\n", n, st.flags.CheckSev)
		return true
	}
	fmt.Fprintf(st.runner.Diag, "check: no open findings at-or-above %s — gate passes\n", st.flags.CheckSev)
	return false
}

// gateCompletenessCheck prints the completeness dimension check line,
// reporting whether the dimension fails the gate.
func gateCompletenessCheck(st *state) bool {
	if !st.flags.Completeness {
		return false
	}
	if completenessPass(st) {
		short := readcommon.ShortSHA(st.headSHA)
		fmt.Fprintf(st.runner.Diag, "check: HEAD %s reviewed (post matches fetch) — completeness passes\n", short)
		return false
	}
	emitCompletenessFail(st)
	return true
}

// gateLiveCheck prints the live-thread verdict line, reporting whether
// the dimension fails the gate.
func gateLiveCheck(st *state) bool {
	return emitLiveVerdict(st)
}

// tierHits counts whole-store open findings at-or-above the tier.
func tierHits(entries []any, tier string) int {
	threshold := map[string]int{tierCritical: 0, tierImportant: 1, tierInformational: 2}[tier]
	n := 0
	for _, e := range entries {
		if readcommon.StatusOf(e) != statusOpen {
			continue
		}
		if sevRankOf(e) <= threshold {
			n++
		}
	}
	return n
}

// sevRankUntagged is the gate severity rank for untagged findings: it never
// meets any tier.
const sevRankUntagged = 3

// sevRankOf ports the gate severity rank (untagged reads 3, never meeting
// a tier).
func sevRankOf(entry any) int {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return sevRankUntagged
	}
	sev, _ := m.Get("severity")
	if s, ok := sev.(string); ok {
		switch s {
		case tierCritical:
			return 0
		case tierImportant:
			return 1
		case tierInformational:
			return 2
		}
	}
	return sevRankUntagged
}

// completenessPass reports whether the HEAD-reviewed dimension passes.
func completenessPass(st *state) bool {
	return st.compOK
}

// emitCompletenessFail prints the typed completeness failure line.
func emitCompletenessFail(st *state) {
	r := st.runner
	refetch := refetchCommand(st.dir, st.req, st.compPosted)
	short := readcommon.ShortSHA(st.headSHA)
	switch {
	case !isRegular(st.dir + "/post.json"):
		fmt.Fprintf(r.Diag, "check: incomplete — no completed post for HEAD %s "+
			"(missing-post: run %s, then review + post, then re-check)\n", short, refetch)
	case st.compPosted == "":
		fmt.Fprintf(r.Diag, "check: incomplete — post record carries no head SHA "+
			"(corrupt-post: re-run %s, then review + post, then re-check)\n", refetch)
	default:
		fmt.Fprintf(r.Diag, "check: incomplete — HEAD %s != posted %s "+
			"(stale-head: re-run %s, then review + post, then re-check)\n",
			short, readcommon.ShortSHA(st.compPosted), refetch)
	}
}

// emitLiveVerdict prints the live-thread verdict line, reporting whether
// the dimension fails the gate.
func emitLiveVerdict(st *state) bool {
	r := st.runner
	switch st.live.status {
	case liveFail:
		fmt.Fprintf(r.Diag, "check: %d live open thread(s) vs %d local open finding(s) — gate fails "+
			"(fetch + triage them, then re-check)\n", st.live.open, st.nAllOpen)
		return true
	case livePass:
		fmt.Fprintf(r.Diag, "check: %d live open thread(s) vs %d local open finding(s) — live recheck passes\n", st.live.open, st.nAllOpen)
	case liveUnknownStrict:
		detail := st.live.err
		if detail == "" {
			detail = "lookup failed"
		}
		fmt.Fprintf(r.Diag, "check: live thread recheck unreachable (%s — gate fails: offline posture is strict; "+
			"restore the network or re-run with --live skip, then re-check)\n", detail)
		return true
	case liveSkipExplicit:
		fmt.Fprintln(r.Diag, "check: live recheck skipped (--live skip) — gating on local dimensions only")
	}
	return false
}
