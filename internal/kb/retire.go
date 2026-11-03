package kb

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// unitSep mirrors the \u001f separator the shell uses for staged op keys.
const unitSep = "\u001f"

// StripStamps ports kb_strip_stamps: it removes staged kb_credit stamps from
// the persisted findings file so a later re-flip can retry dropped credit.
// Best effort; it never fails. Like the shell it warns loudly when the
// rewrite cannot proceed (a missing file stays silent, matching the shell's
// early return).
func StripStamps(findingsFile string, staged []map[string]any, warn func(string)) {
	if len(staged) == 0 {
		return
	}
	if _, err := os.Stat(findingsFile); err != nil {
		return
	}
	entries, ok := loadFindingsEntries(findingsFile)
	if !ok {
		recoverySkipped(findingsFile, warn)
		return
	}
	sigSet, drop := stagedKeys(staged)
	if hasMalformedCredit(entries, sigSet, findingsFile, warn) {
		return
	}
	if !stripMatchedStamps(entries, sigSet, drop) {
		return
	}
	if err := atomic.WriteFile(findingsFile, []byte(jq.PrettyOrdered(entries))); err != nil {
		// Staged stamps kept — credit for these flips is spent; the next
		// re-flip finds either the join row or the stamp and skips.
		recoverySkipped(findingsFile, warn)
	}
}

// recoverySkipped warns that staged stamps are kept, mirroring the shell's
// kb_strip_stamps failure path byte-for-byte.
func recoverySkipped(findingsFile string, warn func(string)) {
	warn(fmt.Sprintf("kb recovery skipped: could not rewrite %s "+
		"(staged stamps kept — credit for these flips is spent)", findingsFile))
}

// loadFindingsEntries reads the findings file as an entry list. An oversized
// store reads unreadable, exactly like a corrupt one.
func loadFindingsEntries(findingsFile string) ([]any, bool) {
	raw, err := jq.CappedFile(findingsFile, jq.MaxFindingsBytes)
	if err != nil {
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	entries, ok := doc.([]any)
	if !ok {
		return nil, false
	}
	return entries, true
}

// stripMatchedStamps strips retired stamps from the staged entries,
// reporting whether anything changed.
func stripMatchedStamps(entries []any, sigSet, drop map[string]bool) bool {
	changed := false
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || !sigSet[entrySig(m)] {
			continue
		}
		if stripEntryStamps(m, drop) {
			changed = true
		}
	}
	return changed
}

// stagedKeys indexes the staged credit ops by sig and rule+outcome.
func stagedKeys(staged []map[string]any) (sigSet, drop map[string]bool) {
	sigSet = map[string]bool{}
	drop = map[string]bool{}
	for _, op := range staged {
		sigSet[jq.StrOf(op["sig"])] = true
		drop[jq.StrOf(op["rule"])+unitSep+jq.StrOf(op["outcome"])] = true
	}
	return sigSet, drop
}

// entrySig reads one entry's string sig.
func entrySig(m *jq.Obj) string {
	sigVal, _ := m.Get("sig")
	return jq.StrOf(sigVal)
}

// hasMalformedCredit reports whether any matched entry carries a non-array
// kb_credit. Mirrors the shell: the jq filter errors on a non-array
// kb_credit, so the whole recovery rewrite is skipped.
func hasMalformedCredit(entries []any, sigSet map[string]bool, findingsFile string, warn func(string)) bool {
	for _, e := range entries {
		if malformedEntryCredit(e, sigSet) {
			recoverySkipped(findingsFile, warn)
			return true
		}
	}
	return false
}

// malformedEntryCredit reports whether one entry carries a non-array
// kb_credit in its history.
func malformedEntryCredit(e any, sigSet map[string]bool) bool {
	m, ok := e.(*jq.Obj)
	if !ok || !sigSet[entrySig(m)] {
		return false
	}
	histRaw, _ := m.Get(fieldHistory)
	hist, ok := histRaw.([]any)
	if !ok {
		return false
	}
	return slices.ContainsFunc(hist, malformedHistoryCredit)
}

// malformedHistoryCredit reports whether one history event carries a
// non-array kb_credit.
func malformedHistoryCredit(h any) bool {
	hm, ok := h.(*jq.Obj)
	if !ok {
		return false
	}
	rawCredits, has := hm.Get(fieldCredit)
	if !has || rawCredits == nil {
		return false
	}
	_, ok = rawCredits.([]any)
	return !ok
}

// stripEntryStamps removes retired stamps from one entry's history, reporting
// whether anything changed.
func stripEntryStamps(m *jq.Obj, drop map[string]bool) bool {
	changed := false
	histRaw, _ := m.Get(fieldHistory)
	hist, ok := histRaw.([]any)
	if !ok {
		return false
	}
	for i, h := range hist {
		hm, ok := h.(*jq.Obj)
		if !ok {
			continue
		}
		if stripHistoryStamps(hm, drop) {
			changed = true
		}
		hist[i] = hm
	}
	return changed
}

// stripHistoryStamps removes retired stamps from one history event.
func stripHistoryStamps(hm *jq.Obj, drop map[string]bool) bool {
	rawCredits, has := hm.Get(fieldCredit)
	if !has || rawCredits == nil {
		return false
	}
	credits, ok := rawCredits.([]any)
	if !ok {
		return false
	}
	kept := filterCredits(credits, drop)
	if len(kept) == 0 {
		hm.Delete(fieldCredit)
		return true
	}
	if len(kept) != len(credits) {
		hm.Set(fieldCredit, kept)
		return true
	}
	return false
}

// filterCredits drops the retired credit stamps.
func filterCredits(credits []any, drop map[string]bool) []any {
	kept := credits[:0:0]
	for _, cr := range credits {
		if key := credKey(cr); key != "" && drop[key] {
			continue
		}
		kept = append(kept, cr)
	}
	return kept
}

// credKey renders the (rule, outcome) idempotency key for one kb_credit
// element, or "" for non-objects (which the shell's select keeps).
func credKey(cr any) string {
	switch cm := cr.(type) {
	case *jq.Obj:
		rule, _ := cm.Get("rule")
		to, _ := cm.Get("to")
		rs := jq.StrOf(rule)
		ts := jq.StrOf(to)
		return rs + unitSep + ts
	case map[string]any:
		rule := jq.StrOf(cm["rule"])
		to := jq.StrOf(cm["to"])
		return rule + unitSep + to
	default:
		return ""
	}
}

// maybeRetire ports kb_maybe_retire: it tombstones an active rule whose
// false-hit rate exceeds the explicit threshold. It warns, never fails.
func (c *Ctx) maybeRetire(rid, now string) {
	raw, err := jq.CappedFile(File, jq.MaxKBBytes)
	if err != nil {
		return
	}
	rows, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		return
	}
	row := findRow(rows, rid)
	if row == nil {
		return
	}
	if st, _ := row.Get("status"); st != statusActive {
		return
	}
	if !c.shouldRetire(row) {
		return
	}
	c.retireRow(rows, row, rid, now)
}

// shouldRetire reports whether the row's false-hit rate trips the policy.
func (c *Ctx) shouldRetire(row *jq.Obj) bool {
	newFP, okFP := numField(row, "false_hits")
	newJudged, okJ := numField(row, "judged")
	if !okFP || !okJ {
		return false
	}
	if newJudged < c.Policy.MinJudged {
		return false
	}
	if newJudged <= 0 || float64(newFP)/float64(newJudged) <= rateVal(c.Policy.MaxFPRate) {
		return false
	}
	return true
}

// retireReason renders the auto-retire reason for the row.
func (c *Ctx) retireReason(row *jq.Obj) string {
	newFP, _ := numField(row, "false_hits")
	newJudged, _ := numField(row, "judged")
	rate := fmt.Sprintf("%.2f", float64(newFP)/float64(newJudged))
	return fmt.Sprintf("auto-retired: false-hit rate %d/%d (%s) exceeds %s at judged >= %d",
		newFP, newJudged, rate, c.Policy.MaxFPRate, c.Policy.MinJudged)
}

// retireRow marks the row retired and persists the store. The history event
// keeps shell field order (at, event, actor, note) via *jq.Obj — plain maps
// would serialize sorted (I4).
func (c *Ctx) retireRow(rows []*jq.Obj, row *jq.Obj, rid, now string) {
	reason := c.retireReason(row)
	row.Set("status", "retired")
	row.Set("retiredAt", now)
	row.Set("retireReason", reason)
	appendHistory(row, jq.NewObj(
		"at", now, fieldEvent, "auto-retired", fieldActor, "script", fieldNote, reason,
	))
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString(r.MarshalCompact())
		sb.WriteByte('\n')
	}
	if err := atomic.WriteFile(File, []byte(sb.String())); err != nil {
		c.Warn(fmt.Sprintf("kb auto-retire skipped: could not rewrite %s for '%s'", File, rid))
		return
	}
	c.Warn(fmt.Sprintf("kb rule '%s' auto-retired (%s)", rid, reason))
}

// numField reads an integer counter field, reporting false unless the raw
// value matches ^[0-9]+$ after jq -r rendering.
func numField(row *jq.Obj, key string) (int, bool) {
	v, ok := row.Get(key)
	if !ok {
		return 0, false
	}
	s := jq.Str(v)
	if !isPositiveInt(s) && s != "0" {
		return 0, false
	}
	return parseNum(s), true
}

// rateVal parses the max-FP-rate threshold.
// fallbackRate is the retire rate used when the threshold knob is unreadable.
const fallbackRate = 0.5

func rateVal(s string) float64 {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	if err != nil {
		return fallbackRate
	}
	return f
}

// appendHistory appends an event to the row's history array.
func appendHistory(row *jq.Obj, ev *jq.Obj) {
	h, _ := row.Get(fieldHistory)
	hist, ok := h.([]any)
	if !ok {
		hist = []any{}
	}
	row.Set(fieldHistory, append(hist, ev))
}

// History field names.
const (
	fieldCredit  = "kb_credit"
	fieldHistory = "history"
	fieldEvent   = "event"
	fieldActor   = "actor"
	fieldNote    = "note"
)
