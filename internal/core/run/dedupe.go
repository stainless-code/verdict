package run

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	crossSameAs   = "same_as"
	crossOrigin   = "origin"
	unionFindings = "findings"
	findingPath   = "path"
	findingLine   = "line"
	// shortSigLen is how much of a signature a warning prints: enough to find
	// it in review.json, short enough to keep the line readable.
	shortSigLen = 12
)

// The union merges exact signatures only, and two finders never word one
// defect identically, so a real duplicate reaches the cross phase as two
// findings. A checker that confirms the other finder's candidate may name one
// of its own findings as the same issue; the coordinator folds that pair into
// one finding after the cross phase. The checker's word alone never merges: the
// pair must sit on one file and side at overlapping or adjacent lines, so a bad
// same_as is ignored and named rather than collapsing two distinct bugs.

// unionIndex is the frozen union keyed by signature: each finding row in union
// order and the finders the plan records as its origins.
type unionIndex struct {
	rows       map[string]*jq.Obj
	origins    map[string][]string
	identities []any
	order      []string
}

func indexUnion(review, plan *jq.Obj) *unionIndex {
	u := &unionIndex{rows: map[string]*jq.Obj{}, origins: map[string][]string{}, identities: panelcore.Findings(review)}
	findings, _ := review.Get(unionFindings)
	for i, value := range jq.Elements(findings) {
		row, ok := value.(*jq.Obj)
		if !ok || i >= len(u.identities) {
			continue
		}
		signature := jq.StrOf(readcommon.FieldOf(u.identities[i], crossSig))
		u.rows[signature] = row
		u.order = append(u.order, signature)
	}
	checks, _ := plan.Get("checks")
	for _, check := range jq.Elements(checks) {
		signature := jq.StrOf(readcommon.FieldOf(check, crossSig))
		u.origins[signature] = append(u.origins[signature], jq.StrOf(readcommon.FieldOf(check, crossOrigin)))
	}
	return u
}

// ownFindings lists the findings only checker reported. The checker runs in a
// fresh process and cannot recall its own review, so the request carries the
// signatures a same_as may name.
func ownFindings(plan, review *jq.Obj, checker string) []any {
	u := indexUnion(review, plan)
	out := []any{}
	for _, signature := range u.order {
		if !soleOrigin(u.origins[signature], checker) {
			continue
		}
		row := u.rows[signature]
		out = append(out, jq.NewObj(crossSig, signature, findingPath, readcommon.FieldOf(row, findingPath),
			findingLine, readcommon.FieldOf(row, findingLine), "claim", readcommon.FieldOf(row, "claim")))
	}
	return out
}

func soleOrigin(origins []string, finder string) bool {
	return len(origins) == 1 && origins[0] == finder
}

// sameAsPair is one accepted duplicate: the candidate a checker confirmed and
// the checker's own finding it named as the same issue.
type sameAsPair struct {
	checked, own, checker string
}

// sameAsPairs reads every same_as the cross rows carry, in receipt order, and
// keeps the ones the anchors support. Each ignored one is named with why.
func (u *unionIndex) sameAsPairs(results []any) ([]sameAsPair, []string) {
	var pairs []sameAsPair
	var notes []string
	taken := map[string]bool{}
	for _, row := range results {
		named := jq.StrOf(readcommon.FieldOf(row, crossSameAs))
		if named == "" {
			continue
		}
		pair := sameAsPair{
			checked: jq.StrOf(readcommon.FieldOf(row, crossSig)), checker: jq.StrOf(readcommon.FieldOf(row, crossChecker)),
			own: readcommon.ResolveSig(u.identities, named),
		}
		if problem := u.sameAsProblem(row, pair, taken); problem != "" {
			notes = append(notes, core.Msgf("%s's cross check named finding %s same_as %s; ignored: %s",
				pair.checker, shortSig(pair.checked), shortSig(named), problem))
			continue
		}
		taken[pair.checked], taken[pair.own] = true, true
		pairs = append(pairs, pair)
	}
	return pairs, notes
}

func (u *unionIndex) sameAsProblem(row any, pair sameAsPair, taken map[string]bool) string {
	switch {
	case readcommon.FieldOf(row, "verdict") != "confirmed":
		return "only a confirmed check can name a duplicate"
	case pair.own == "NONE" || pair.own == "MANY":
		return "it names no single finding in the union"
	case !soleOrigin(u.origins[pair.own], pair.checker):
		return "it names a finding the checker did not report alone"
	case len(u.origins[pair.checked]) != 1:
		return "the checked finding already carries both finders"
	case taken[pair.checked] || taken[pair.own]:
		return "a finding merges at most once"
	}
	return anchorProblem(u.rows[pair.checked], u.rows[pair.own])
}

// anchorProblem is the deterministic guard on a checker's same_as: one file,
// one side, and line spans that overlap or touch.
func anchorProblem(checked, own *jq.Obj) string {
	checkedPath, ownPath := jq.StrOf(readcommon.FieldOf(checked, findingPath)), jq.StrOf(readcommon.FieldOf(own, findingPath))
	if checkedPath != ownPath {
		return fmt.Sprintf("it names a finding in %s, not %s", ownPath, checkedPath)
	}
	if quoteSide(checked) != quoteSide(own) {
		return "it names a finding on the other side of the diff"
	}
	checkedStart, checkedEnd, checkedOK := lineSpan(checked)
	ownStart, ownEnd, ownOK := lineSpan(own)
	if !checkedOK || !ownOK {
		return "a finding without a line cannot merge"
	}
	if checkedStart > ownEnd+1 || ownStart > checkedEnd+1 {
		return fmt.Sprintf("it names a finding at %s, not overlapping or adjacent to %s",
			spanText(ownStart, ownEnd), spanText(checkedStart, checkedEnd))
	}
	return ""
}

func lineSpan(row *jq.Obj) (int, int, bool) {
	end, ok := jq.IntOf(readcommon.FieldOf(row, findingLine))
	if !ok {
		return 0, 0, false
	}
	start, ok := jq.IntOf(readcommon.FieldOf(row, "start_line"))
	if !ok || start > end {
		start = end
	}
	return start, end, true
}

func spanText(start, end int) string {
	if start == end {
		return fmt.Sprintf("line %d", start)
	}
	return fmt.Sprintf("lines %d-%d", start, end)
}

// shortSig cuts by rune: same_as is agent text, and a byte cut could split a
// character in the warning.
func shortSig(signature string) string {
	if runes := []rune(signature); len(runes) > shortSigLen {
		return string(runes[:shortSigLen])
	}
	return signature
}

// dedupe folds every accepted same_as pair into one finding and republishes the
// aggregate review, plan and receipt bindings. Without a pair the frozen union
// stands byte for byte. A resumed run re-derives the union and reuses the
// verified rows, so it reaches the same merge.
func (c *coordinator) dedupe() *core.RefusalError {
	results := jq.Elements(readcommon.FieldOf(c.binding, crossResults))
	u := indexUnion(c.review, c.plan)
	pairs, notes := u.sameAsPairs(results)
	for _, row := range results {
		if obj, ok := row.(*jq.Obj); ok {
			obj.Delete(crossSameAs)
		}
	}
	for _, note := range notes {
		c.st.warn(note)
	}
	if len(pairs) == 0 {
		return nil
	}
	diff, ref := snapshotBytes(c.dir, "full.diff")
	if ref != nil {
		return ref
	}
	idx := evidence.IndexDiff(diff)
	merged := u.mergePairs(pairs, func(row *jq.Obj) bool { return findingQuoteVerified(idx, row) })
	for _, m := range merged {
		c.st.human(core.Msgf("» merged duplicate finding %s into %s (%s named it same_as)",
			shortSig(m.dropped), shortSig(m.kept), m.checker))
	}
	c.review = mergedReview(c.review, u, merged)
	c.plan = mergedPlan(c.plan, u, merged)
	c.plan.Set("review_sha256", shaHex(coordinatorBytes(c.review)))
	rekeyResults(results, merged)
	return c.republishUnion()
}

// republishUnion writes the merged review and plan over the frozen union and
// points the receipt binding at them.
func (c *coordinator) republishUnion() *core.RefusalError {
	if ref := writeCoordinator(c.dir, reviewFile, c.review); ref != nil {
		return ref
	}
	planPath := filepath.Join(verificationDir, "plan.json")
	if ref := writeCoordinator(c.dir, planPath, c.plan); ref != nil {
		return ref
	}
	c.binding.Set("review_sha256", shaHex(coordinatorBytes(c.review)))
	c.binding.Set("plan_sha256", shaHex(coordinatorBytes(c.plan)))
	return c.bind(reviewFile, planPath)
}

func findingQuoteVerified(idx *evidence.DiffIndex, row *jq.Obj) bool {
	line, _ := jq.IntOf(readcommon.FieldOf(row, findingLine))
	return evidence.MatchQuote(idx, evidence.QuoteRequest{
		Path: jq.StrOf(readcommon.FieldOf(row, findingPath)), Side: quoteSide(row),
		Line: line, Quote: jq.StrOf(readcommon.FieldOf(row, "quote")),
	}).Verified()
}

// mergedFinding is one folded pair: the signature that stays, the one that
// folds into it, the row the merged finding carries, and the checker that
// named the pair.
type mergedFinding struct {
	row                    *jq.Obj
	kept, dropped, checker string
}

// mergePairs picks each pair's surviving row: the better-evidenced quote wins,
// the confirmed candidate breaks a tie, and the merged finding carries the
// higher severity of the two.
func (u *unionIndex) mergePairs(pairs []sameAsPair, verified func(*jq.Obj) bool) []mergedFinding {
	out := make([]mergedFinding, 0, len(pairs))
	for _, pair := range pairs {
		kept, dropped := pair.checked, pair.own
		if verified(u.rows[pair.own]) && !verified(u.rows[pair.checked]) {
			kept, dropped = pair.own, pair.checked
		}
		row := cloneUnionRow(u.rows[kept])
		severity := readcommon.FieldOf(u.rows[dropped], unionSeverity)
		if core.TierRank(severity) < core.TierRank(readcommon.FieldOf(row, unionSeverity)) {
			row.Set(unionSeverity, severity)
		}
		out = append(out, mergedFinding{row: row, kept: kept, dropped: dropped, checker: pair.checker})
	}
	return out
}

// survivors maps each dropped signature to the one it folded into.
func survivors(merged []mergedFinding) map[string]string {
	out := make(map[string]string, len(merged))
	for _, m := range merged {
		out[m.dropped] = m.kept
	}
	return out
}

// mergedOrder is the union's finding order after the fold: severity first,
// then signature, exactly as the union sorts.
func mergedOrder(u *unionIndex, merged []mergedFinding) ([]string, map[string]*jq.Obj) {
	rows := make(map[string]*jq.Obj, len(u.rows))
	maps.Copy(rows, u.rows)
	for _, m := range merged {
		rows[m.kept] = m.row
		delete(rows, m.dropped)
	}
	order := make([]string, 0, len(rows))
	for _, signature := range u.order {
		if rows[signature] != nil {
			order = append(order, signature)
		}
	}
	slices.SortStableFunc(order, func(a, b string) int {
		rank := core.TierRank(readcommon.FieldOf(rows[a], unionSeverity)) - core.TierRank(readcommon.FieldOf(rows[b], unionSeverity))
		if rank != 0 {
			return rank
		}
		return strings.Compare(a, b)
	})
	return order, rows
}

func mergedReview(review *jq.Obj, u *unionIndex, merged []mergedFinding) *jq.Obj {
	order, rows := mergedOrder(u, merged)
	findings := make([]any, 0, len(order))
	for _, signature := range order {
		findings = append(findings, rows[signature])
	}
	out := cloneUnionRow(review)
	out.Set(unionFindings, findings)
	out.Set("summary", fmt.Sprintf("Two-finder union: %d findings.", len(findings)))
	if specs, ok := review.Get(unionSpecChecks); ok && specs != nil {
		out.Set(unionSpecChecks, rekeySpecChecks(jq.Elements(specs), u, survivors(merged)))
	}
	return out
}

// rekeySpecChecks points a spec check that cited a folded finding at the
// survivor, so a violated spec item keeps its cross-checked finding.
func rekeySpecChecks(specs []any, u *unionIndex, folded map[string]string) []any {
	out := make([]any, 0, len(specs))
	for _, spec := range specs {
		row, ok := spec.(*jq.Obj)
		if !ok {
			out = append(out, spec)
			continue
		}
		cited := readcommon.ResolveSig(u.identities, jq.StrOf(readcommon.FieldOf(row, "finding_sig")))
		if kept := folded[cited]; kept != "" {
			row = cloneUnionRow(row)
			row.Set("finding_sig", kept)
		}
		out = append(out, row)
	}
	return out
}

// mergedPlan moves the dropped finding's directed check and origin onto the
// survivor, which then carries both finders like an exact-signature duplicate,
// and records which pair folded and who named it.
func mergedPlan(plan *jq.Obj, u *unionIndex, merged []mergedFinding) *jq.Obj {
	order, _ := mergedOrder(u, merged)
	folded := survivors(merged)
	checks, _ := plan.Get("checks")
	sources, _ := plan.Get("sources")
	bySig := groupChecks(jq.Elements(checks), folded)
	origins := groupOrigins(jq.Elements(sources), folded)
	notes := map[string]*jq.Obj{}
	for _, m := range merged {
		notes[m.kept] = jq.NewObj(crossSig, m.dropped, crossChecker, m.checker)
	}
	newChecks, newSources := []any{}, []any{}
	for _, signature := range order {
		slices.SortStableFunc(bySig[signature], byFinder(crossOrigin))
		newChecks = append(newChecks, bySig[signature]...)
		slices.SortStableFunc(origins[signature], byFinder(unionFinder))
		source := jq.NewObj(crossSig, signature, "origins", origins[signature])
		if note := notes[signature]; note != nil {
			source.Set("merged", note)
		}
		newSources = append(newSources, source)
	}
	out := cloneUnionRow(plan)
	out.Set("checks", newChecks)
	out.Set("sources", newSources)
	return out
}

// groupChecks keys the plan's directed checks by the finding they land on
// after the fold.
func groupChecks(checks []any, folded map[string]string) map[string][]any {
	out := map[string][]any{}
	for _, check := range checks {
		obj, ok := check.(*jq.Obj)
		if !ok {
			continue
		}
		row := cloneUnionRow(obj)
		signature := jq.StrOf(readcommon.FieldOf(row, crossSig))
		if kept := folded[signature]; kept != "" {
			signature = kept
			row.Set(crossSig, kept)
		}
		out[signature] = append(out[signature], row)
	}
	return out
}

// groupOrigins keys the plan's finder origins by the finding they land on
// after the fold.
func groupOrigins(sources []any, folded map[string]string) map[string][]any {
	out := map[string][]any{}
	for _, source := range sources {
		signature := jq.StrOf(readcommon.FieldOf(source, crossSig))
		if kept := folded[signature]; kept != "" {
			signature = kept
		}
		out[signature] = append(out[signature], jq.Elements(readcommon.FieldOf(source, "origins"))...)
	}
	return out
}

func byFinder(key string) func(a, b any) int {
	return func(a, b any) int {
		return strings.Compare(jq.StrOf(readcommon.FieldOf(a, key)), jq.StrOf(readcommon.FieldOf(b, key)))
	}
}

// rekeyResults points the dropped finding's check at the survivor, so the
// receipt holds both directed checks for the one merged finding.
func rekeyResults(results []any, merged []mergedFinding) {
	folded := survivors(merged)
	for _, row := range results {
		obj, ok := row.(*jq.Obj)
		if !ok {
			continue
		}
		if kept := folded[jq.StrOf(readcommon.FieldOf(obj, crossSig))]; kept != "" {
			obj.Set(crossSig, kept)
		}
	}
}
