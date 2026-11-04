package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/stainless-code/verdict/internal/jq"
)

// ApplyCredit stages one credit op per cited rule
// not already credited for this finding. Staging is memory-only; the caller
// stamps Credited into the flipping history event, and CommitStaged applies
// everything after the findings file persists. It never fails the flip.
func (c *Ctx) ApplyCredit(entries []any, sig, outcome, actor, verb, now string, kbExists bool) {
	c.Credited = []any{}
	if c.Broken || !kbExists {
		return
	}
	switch outcome {
	case outcomeAddressed, "false-positive":
	default:
		return
	}
	joins, ok := readJoins()
	if !ok {
		c.Warn(fmt.Sprintf("kb joins %s unreadable — credit skipped for this flip (fix or remove the file)", Joins))
		return
	}
	entry := findEntry(entries, sig)
	if entry == nil {
		return
	}
	rawKb, _ := entry.Get("kb")
	kbList, ok := rawKb.([]any)
	if !ok {
		return
	}
	c.stageCites(entry, kbList, sig, outcome, actor, verb, now, joins)
}

func (c *Ctx) stageCites(entry *jq.Obj, kbList []any, sig, outcome, actor, verb, now string, joins []*jq.Obj) {
	for _, item := range kbList {
		rid := jq.Str(item)
		if rid == "" {
			continue
		}
		if joinHas(joins, sig, rid, outcome) || historyHas(entry, rid, outcome) {
			continue
		}
		c.stageCite(sig, rid, outcome, actor, verb, now)
	}
}

// stageCite stages one rule's credit op when the rule is active.
func (c *Ctx) stageCite(sig, rid, outcome, actor, verb, now string) {
	if !c.ruleActive(rid) {
		c.Warn(fmt.Sprintf("kb credit skipped: rule '%s' not active in %s", rid, File))
		return
	}
	c.Staged = append(c.Staged, map[string]any{
		fieldSig: sig, fieldRule: rid, fieldOutcome: outcome,
		"actor": actor, "verb": verb, "at": now,
	})
	c.Credited = append(c.Credited, map[string]any{fieldRule: rid, "to": outcome})
}

func findEntry(entries []any, sig string) *jq.Obj {
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok {
			if s, _ := m.Get(fieldSig); s == sig {
				return m
			}
		}
	}
	return nil
}

func joinHas(joins []*jq.Obj, sig, rid, outcome string) bool {
	for _, j := range joins {
		s, _ := j.Get(fieldSig)
		r, _ := j.Get(fieldRule)
		o, _ := j.Get(fieldOutcome)
		if s == sig && r == rid && o == outcome {
			return true
		}
	}
	return false
}

func historyHas(entry *jq.Obj, rid, outcome string) bool {
	histRaw, _ := entry.Get("history")
	hist, ok := histRaw.([]any)
	if !ok {
		return false
	}
	for _, h := range hist {
		if creditsMatch(historyCredits(h), rid, outcome) {
			return true
		}
	}
	return false
}

func historyCredits(h any) []any {
	switch hm := h.(type) {
	case *jq.Obj:
		cr, _ := hm.Get("kb_credit")
		if arr, ok := cr.([]any); ok {
			return arr
		}
	case map[string]any:
		if arr, ok := hm["kb_credit"].([]any); ok {
			return arr
		}
	}
	return nil
}

func creditsMatch(credits []any, rid, outcome string) bool {
	for _, cr := range credits {
		if rule, to, ok := creditRef(cr); ok && rule == rid && to == outcome {
			return true
		}
	}
	return false
}

func creditRef(cr any) (string, string, bool) {
	switch cm := cr.(type) {
	case *jq.Obj:
		rule, _ := cm.Get(fieldRule)
		to, _ := cm.Get("to")
		return jq.StrOf(rule), jq.StrOf(to), true
	case map[string]any:
		return jq.StrOf(cm[fieldRule]), jq.StrOf(cm["to"]), true
	default:
		return "", "", false
	}
}

// ruleActive owns the active-status gate: the rule row must exist with
// status active. An unreadable store reads inactive (the caller warns).
func (c *Ctx) ruleActive(rid string) bool {
	raw, ok := c.ReadFile(File)
	if !ok {
		return false
	}
	rows, err := ParseRows(raw)
	if err != nil {
		return false
	}
	row := FindRow(rows, rid)
	if row == nil {
		return false
	}
	st, _ := row.Get("status")
	return st == statusActive
}

// CommitStaged applies staged credit after the
// findings file carrying the flips has persisted. One kb.jsonl rewrite
// (counters), then join rows, then retirement checks. Failures warn loudly
// and strip the staged stamps so a later re-flip can retry; never fails.
func (c *Ctx) CommitStaged(findingsFile, now string) {
	if len(c.Staged) == 0 {
		return
	}
	if c.Broken {
		c.Staged = nil
		return
	}
	rows, ok := c.loadStore(findingsFile)
	if !ok {
		return
	}
	joins, _ := readJoins()
	live := filterLive(c.Staged, joins)
	if len(live) == 0 {
		c.Staged = nil
		return
	}
	if !c.rewriteCounters(rows, live, findingsFile) {
		return
	}
	c.appendJoins(findingsFile, live)
	for _, rid := range unionRules(live) {
		c.maybeRetire(rid, now)
	}
	c.Staged = nil
}

func (c *Ctx) loadStore(findingsFile string) ([]*jq.Obj, bool) {
	raw, err := jq.CappedFile(File, jq.MaxKBBytes)
	rows, derr := ParseRows(raw)
	if err != nil || derr != nil {
		c.Warn(fmt.Sprintf("kb store %s unreadable at commit — staged credit dropped (flips stand); re-run the flip to retry", File))
		StripStamps(findingsFile, c.Staged, c.Warn)
		c.Staged = nil
		return nil, false
	}
	return rows, true
}

func filterLive(staged []map[string]any, joins []*jq.Obj) []map[string]any {
	live := make([]map[string]any, 0, len(staged))
	for _, op := range staged {
		sig := jq.StrOf(op[fieldSig])
		rid := jq.StrOf(op[fieldRule])
		outcome := jq.StrOf(op[fieldOutcome])
		if joinHas(joins, sig, rid, outcome) {
			continue
		}
		live = append(live, op)
	}
	return live
}

// rewriteCounters bumps the hits/false_hits/judged counters, returning false
// (staged credit dropped) when the rewrite fails. The rewrite is one
// program, one bad counter fails the whole rewrite: jq's `+` errors on
// strings, booleans, and containers, so those refuse here instead of
// coercing (only null, false, missing, and numbers add).
func (c *Ctx) rewriteCounters(rows []*jq.Obj, live []map[string]any, findingsFile string) bool {
	hits, fps := tallyOutcomes(live)
	if !bumpCounters(rows, hits, fps) {
		c.Warn(fmt.Sprintf("kb commit failed: could not rewrite %s (flips stand, staged credit dropped) — re-run the flip to retry", File))
		StripStamps(findingsFile, c.Staged, c.Warn)
		c.Staged = nil
		return false
	}
	if err := WriteRows(rows); err != nil {
		c.Warn(fmt.Sprintf("kb commit failed: could not rewrite %s (flips stand, staged credit dropped) — re-run the flip to retry", File))
		StripStamps(findingsFile, c.Staged, c.Warn)
		c.Staged = nil
		return false
	}
	return true
}

func bumpCounters(rows []*jq.Obj, hits, fps map[string]int) bool {
	for _, row := range rows {
		if !bumpRow(row, hits, fps) {
			return false
		}
	}
	return true
}

// bumpRow applies staged tallies to one rule row, reporting false when any
// touched counter is non-numeric (jq's `+` would error there too).
func bumpRow(row *jq.Obj, hits, fps map[string]int) bool {
	id, _ := row.Get("id")
	rid := jq.StrOf(id)
	h, f := hits[rid], fps[rid]
	if h+f == 0 {
		return true
	}
	nh, ok := numAdd(row, "hits", h)
	if !ok {
		return false
	}
	nf, ok := numAdd(row, "false_hits", f)
	if !ok {
		return false
	}
	nj, ok := numAdd(row, "judged", h+f)
	if !ok {
		return false
	}
	row.Set("hits", nh)
	row.Set("false_hits", nf)
	row.Set("judged", nj)
	return true
}

func tallyOutcomes(live []map[string]any) (hits, fps map[string]int) {
	hits = map[string]int{}
	fps = map[string]int{}
	for _, op := range live {
		rid := jq.StrOf(op[fieldRule])
		if op[fieldOutcome] == outcomeAddressed {
			hits[rid]++
		} else {
			fps[rid]++
		}
	}
	return hits, fps
}

// appendJoins writes one audit row per live op. Bundle scoping uses
// [filepath.Dir]/[filepath.Base].
func (c *Ctx) appendJoins(findingsFile string, live []map[string]any) {
	bundleDir := filepath.Dir(findingsFile)
	bundle := filepath.Base(bundleDir)
	reviewID := reviewIDOf(filepath.Join(bundleDir, "post.json"))
	for _, op := range live {
		rid := jq.StrOf(op[fieldRule])
		jrow := jq.NewObj(
			"at", op["at"], fieldSig, op[fieldSig], fieldRule, rid,
			fieldOutcome, op[fieldOutcome], "actor", op["actor"], fieldVerb, op[fieldVerb],
			"bundle", bundle, "review_id", reviewID,
		)
		if err := appendJoin(jrow.MarshalCompact() + "\n"); err != nil {
			c.Warn(fmt.Sprintf("kb audit row for '%s' not written (%s)", rid, Joins))
		}
	}
}

func unionRules(live []map[string]any) []string {
	seen := map[string]bool{}
	var rules []string
	for _, op := range live {
		rid := jq.StrOf(op[fieldRule])
		if !seen[rid] {
			seen[rid] = true
			rules = append(rules, rid)
		}
	}
	slices.Sort(rules)
	return rules
}

func reviewIDOf(postPath string) any {
	raw, err := os.ReadFile(postPath)
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
	rev, ok := m["review"].(map[string]any)
	if !ok {
		return nil
	}
	id, ok := rev["id"]
	if !ok || id == nil {
		return nil
	}
	return id
}

// appendJoin appends one row to the joins log (creating it when absent).
// A deferred-Close error after a failed write is already reported; after a
// successful write the close is checked like any other flush.
func appendJoin(line string) error {
	f, err := os.OpenFile(Joins, os.O_WRONLY|os.O_APPEND|os.O_CREATE, storeFileMode)
	if err != nil {
		return jq.Sealed("append joins", err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close() // write error already returned; close adds nothing
		return jq.Sealed("append joins", err)
	}
	if err := f.Close(); err != nil {
		return jq.Sealed("append joins", err)
	}
	return nil
}

// numAdd adds n to the row's numeric field: null, false, and missing read
// as 0 (matching jq //); numbers add; anything else reports false (jq's `+`
// would error, failing the whole rewrite).
func numAdd(row *jq.Obj, key string, n int) (any, bool) {
	v, ok := row.Get(key)
	if !ok || v == nil || v == false {
		return jsonNumber(n), true
	}
	switch t := v.(type) {
	case jq.Number:
		return addCounter(t.String(), n)
	case json.Number:
		return addCounter(t.String(), n)
	case float64:
		return jsonNumber(int(t) + n), true
	case int:
		return t + n, true
	default:
		return nil, false
	}
}

// addCounter adds n to one stored counter literal, refusing a numeric value
// that overflows an int instead of wrapping it negative.
func addCounter(lit string, n int) (any, bool) {
	f, ok := jq.SignedPrefixInt(lit)
	if !ok {
		return nil, false
	}
	return jsonNumber(f + n), true
}

func jsonNumber(n int) jq.Number {
	return jq.Number(strconv.Itoa(n))
}

const storeFileMode = 0o644
