package recordcore

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	suppressedKey        = "suppressed"
	withheldKey          = "withheld"
	sourceKey            = "source"
	reasonKey            = "reason"
	exclusionKey         = "exclusion_id"
	minSuppressionReason = 30
)

func suppressionError(message string) error {
	return &core.RefusalError{Code: "bad-suppression", Message: message}
}

func (st *state) parseSuppressions() ([]*jq.Obj, error) {
	raw, has := st.review.Get(suppressedKey)
	if !has {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, suppressionError("suppressed must be an array")
	}
	var out []*jq.Obj
	for _, item := range items {
		m, err := st.parseSuppression(item)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func suppressionString(m *jq.Obj, key string) string {
	v, _ := m.Get(key)
	return jq.StrOf(v)
}

func (st *state) validateSuppression(m *jq.Obj) error {
	if suppressionString(m, fieldPath) == "" || strings.TrimSpace(suppressionString(m, reasonKey)) == "" {
		return suppressionError("suppression requires path and reason")
	}
	switch suppressionString(m, sourceKey) {
	case "agent":
		if utf8.RuneCountInString(strings.TrimSpace(suppressionString(m, reasonKey))) < minSuppressionReason {
			return suppressionError("agent reason requires at least 30 characters")
		}
	case "exclusion":
		if !st.suppressionReceipt(m) {
			return suppressionError("exclusion suppression requires a matching excluded.json path and rule id")
		}
	default:
		return suppressionError("suppression source must be exclusion or agent")
	}
	return suppressionFields(st, m)
}

func suppressionFields(st *state, m *jq.Obj) error {
	for _, key := range append([]string{}, m.Keys...) {
		if err := suppressionField(st, m, key); err != nil {
			return err
		}
	}
	return nil
}

func suppressionField(st *state, m *jq.Obj, key string) error {
	switch key {
	case fieldPath, reasonKey, sourceKey, exclusionKey:
	case fieldCategory:
		st.suppressionCategory(m)
	case fieldSig:
		if suppressionString(m, key) == "" {
			return suppressionError("suppression sig must be a non-empty prefix")
		}
	case fieldLine:
		raw, _ := m.Get(key)
		line, err := strconv.Atoi(jq.R(raw))
		if err != nil || line < 1 {
			return suppressionError("suppression line must be a positive integer")
		}
	default:
		st.warn("unknown suppression field dropped: " + key)
		m.Delete(key)
	}
	return nil
}

func (st *state) suppressionReceipt(m *jq.Obj) bool {
	id := suppressionString(m, exclusionKey)
	if id == "" {
		return false
	}
	receipt := st.exclusions()
	if receipt == nil {
		return false
	}
	file := suppressionString(m, fieldPath)
	for i := range receipt.Excluded {
		row := &receipt.Excluded[i]
		if row.RuleID == id && exclusionRowMatches(row, file) {
			return true
		}
	}
	return false
}

func (st *state) exclusions() *core.ExcludedFile {
	if !st.receiptLoaded {
		st.receipt = loadExclusions(st.dir)
		st.receiptLoaded = true
	}
	return st.receipt
}

func suppressionMatches(s, e *jq.Obj) bool {
	if prefix := suppressionString(s, fieldSig); prefix != "" {
		return suppressionString(s, fieldPath) == suppressionString(e, fieldPath) &&
			strings.HasPrefix(suppressionString(e, fieldSig), prefix)
	}
	if suppressionString(s, fieldPath) != suppressionString(e, fieldPath) {
		return false
	}
	line, has := s.Get(fieldLine)
	actual, _ := e.Get(fieldLine)
	return !has || jq.R(line) == jq.R(actual)
}

func (st *state) applySuppressions() error {
	suppressions, err := st.parseSuppressions()
	if err != nil {
		return err
	}
	matched := make([]bool, len(suppressions))
	for _, entry := range st.entries {
		e, ok := entry.(*jq.Obj)
		if !ok {
			continue
		}
		chosen := chooseSuppression(e, suppressions, matched)
		if chosen >= 0 {
			st.stampSuppression(e, suppressions[chosen])
		} else if !st.autoExclusion(e) {
			st.clearSuppression(e)
		}
	}
	st.warnSuppressionMatches(suppressions, matched)
	return nil
}

func (st *state) stampSuppression(e, s *jq.Obj) {
	prior, _ := e.Get(withheldKey)
	at := st.now
	if p, ok := prior.(*jq.Obj); ok && suppressionString(p, sourceKey) == suppressionString(s, sourceKey) {
		at = suppressionString(p, fieldAt)
	}
	w := jq.NewObj(sourceKey, suppressionString(s, sourceKey), reasonKey, suppressionString(s, reasonKey))
	if id := suppressionString(s, exclusionKey); id != "" {
		w.Set(exclusionKey, id)
	}
	w.Set(fieldAt, at)
	if jq.Compact(prior) != jq.Compact(w) {
		e.Set(fieldHistory, appendHistory(e, jq.NewObj(
			fieldAt, st.now, "event", withheldKey, "actor", st.cmd, "note", suppressionString(s, reasonKey))))
	}
	e.Set(withheldKey, w)
}

func (st *state) clearSuppression(e *jq.Obj) {
	source := readcommon.WithheldSource(e)
	if source == sourceBelowSeverity || source == "validation" {
		return
	}
	if source == "" {
		return
	}
	e.Delete(withheldKey)
	e.Set(fieldHistory, appendHistory(e, jq.NewObj(fieldAt, st.now, "event", "reopened", "actor", st.cmd, "note", "withheld cleared")))
}

// PublicComments returns comments without a withholding receipt.
func PublicComments(entries, comments []any, sigs []string) ([]any, []string) {
	hidden := withheldIndex(entries, func(e *jq.Obj) string { return suppressionString(e, fieldSig) })
	var kept []any
	var keptSigs []string
	for i, c := range comments {
		sig := ""
		if i < len(sigs) {
			sig = sigs[i]
		}
		if !hidden[sig] {
			kept = append(kept, c)
			keptSigs = append(keptSigs, sig)
		}
	}
	return kept, keptSigs
}

func (st *state) hideWithheldMoved(body string) string {
	var visible []any
	hidden := withheldIndex(st.entries, movedKey)
	for _, entry := range st.moved {
		if e, ok := entry.(*jq.Obj); ok && !hidden[movedKey(e)] {
			visible = append(visible, e)
		}
	}
	if len(visible) == len(st.moved) {
		return body
	}
	if len(visible) == 0 {
		st.dropDerivedMoveHeading()
	}
	return strings.Replace(body, appendMoveSection("", st.moved), appendMoveSection("", visible), 1)
}

func movedKey(e *jq.Obj) string {
	line, _ := e.Get(fieldLine)
	return suppressionString(e, fieldPath) + "\x00" + jq.R(line) + "\x00" + NeutralizeMentions(suppressionString(e, "body"))
}

func (st *state) parseSuppression(item any) (*jq.Obj, error) {
	m, ok := item.(*jq.Obj)
	if !ok || m == nil {
		return nil, suppressionError("suppression must be an object")
	}
	if err := st.validateSuppression(m); err != nil {
		return nil, err
	}
	if suppressionAmbiguous(m, st.entries) {
		return nil, suppressionError("suppression sig prefix is ambiguous")
	}
	return m, nil
}

func chooseSuppression(e *jq.Obj, suppressions []*jq.Obj, matched []bool) int {
	chosen := -1
	for i, s := range suppressions {
		if suppressionMatches(s, e) {
			matched[i] = true
			if chosen < 0 {
				chosen = i
			}
		}
	}
	return chosen
}

func withheldIndex(entries []any, key func(*jq.Obj) string) map[string]bool {
	hidden := map[string]bool{}
	for _, entry := range entries {
		if e, ok := entry.(*jq.Obj); ok && readcommon.WithheldSource(e) != "" {
			hidden[key(e)] = true
		}
	}
	return hidden
}

func (st *state) warnSuppressionMatches(suppressions []*jq.Obj, matched []bool) {
	for i, yes := range matched {
		if !yes {
			st.warn("suppression matched no finding: " + suppressionString(suppressions[i], fieldPath))
		} else {
			st.warn("suppression takes precedence over finding: " + suppressionString(suppressions[i], fieldPath))
		}
	}
}

func suppressionAmbiguous(s *jq.Obj, entries []any) bool {
	if suppressionString(s, fieldSig) == "" {
		return false
	}
	n := 0
	for _, entry := range entries {
		if e, ok := entry.(*jq.Obj); ok && suppressionMatches(s, e) {
			n++
		}
	}
	return n > 1
}

func (st *state) suppressionCategory(m *jq.Obj) {
	raw, _ := m.Get(fieldCategory)
	if raw != nil && !core.ValidCategory(jq.StrOf(raw)) {
		st.warn("unknown suppression category — using null")
		m.Set(fieldCategory, nil)
	}
}

func loadExclusions(dir string) *core.ExcludedFile {
	if dir == "" {
		return nil
	}
	raw, err := readcommon.ReadConfined(dir+"/excluded.json", maxAgentBytes)
	if err != nil {
		return nil
	}
	var receipt core.ExcludedFile
	if json.Unmarshal(raw, &receipt) != nil || receipt.SchemaVersion != "1" || !validExclusionRows(receipt.Excluded) {
		return nil
	}
	return &receipt
}

func (st *state) excludedPaths() map[string]bool {
	receipt := st.exclusions()
	out := map[string]bool{}
	if receipt == nil {
		return out
	}
	for _, e := range receipt.Excluded {
		out[e.Path] = true
		out[e.OldPath] = true
	}
	return out
}

// ExclusionForPath returns the rule id and withheld reason for the first row
// in dir/excluded.json matching path, or ok == false when path is not excluded.
func ExclusionForPath(dir, path string) (ruleID, reason string, ok bool) {
	return matchExclusion(loadExclusions(dir), path)
}

func matchExclusion(receipt *core.ExcludedFile, path string) (ruleID, reason string, ok bool) {
	if receipt == nil || path == "" {
		return "", "", false
	}
	for i := range receipt.Excluded {
		row := &receipt.Excluded[i]
		if exclusionRowMatches(row, path) {
			return row.RuleID, "Path excluded from the reviewed diff by " + row.RuleID, true
		}
	}
	return "", "", false
}

func (st *state) autoExclusion(e *jq.Obj) bool {
	if ruleID, reason, ok := matchExclusion(st.exclusions(), suppressionString(e, fieldPath)); ok {
		st.stampSuppression(e, jq.NewObj(
			sourceKey, "exclusion", reasonKey, reason, exclusionKey, ruleID))
		return true
	}
	return false
}

func (st *state) validateSuppressionBytes(raw []byte) *core.RefusalError {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return refusalOrFailed(err)
	}
	review, ok := doc.(*jq.Obj)
	if !ok {
		return nil
	}
	st.review = review
	if _, err := st.parseSuppressions(); err != nil {
		return refusalOrFailed(err)
	}
	return nil
}

func validExclusionRows(rows []core.ExcludedPath) bool {
	for _, row := range rows {
		if row.Path == "" || row.RuleID == "" || row.Bytes <= 0 {
			return false
		}
	}
	return true
}

func exclusionRowMatches(row *core.ExcludedPath, file string) bool {
	return file != "" && (row.Path == file || row.OldPath == file)
}

func (st *state) dropDerivedMoveHeading() {
	var entries []any
	for _, entry := range st.entries {
		if readcommon.FieldOf(entry, fieldHeading) != core.MovedSectionHeading {
			entries = append(entries, entry)
		}
	}
	st.entries = entries
}
