package fetchcore

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// writeKBRecall ports write_kb_recall: scoped rules recalled onto a bundle
// as advisory context (kb.txt plus the kb-recall.json machine manifest,
// rank-ordered top 3). No match, no store, or unreadable store: both files
// removed, no failure.
func writeKBRecall(r *session, dir, extra string) {
	mf, mfj := dir+suffixKBTxt, dir+suffixKBRecall
	ranked, ok := recallRanked(r, dir, extra)
	if !ok || len(ranked) == 0 {
		clearKBRecall(mf, mfj)
		return
	}
	ids, note := renderKBNote(ranked)
	// The shell stages through .tmp files; the atomic path writes the same
	// bytes (only the bytes are graded).
	if err := atomic.WriteFile(mf, []byte(note)); err != nil {
		r.Warn(fmt.Sprintf("could not write %s: %v", mf, err))
		return
	}
	if !writeKBManifest(r, mfj, mf, ranked) {
		return
	}
	fmt.Fprintf(r.Human, "» kb: %d rule(s) -> %s (%s)\n", len(ranked), mf, strings.Join(ids, ", "))
}

// clearKBRecall removes both recall stores when the run has nothing to say.
func clearKBRecall(mf, mfj string) {
	_ = os.Remove(mf)
	_ = os.Remove(mfj)
}

// recallRanked ports the recall pipeline: store read, file set, active
// rules, scope matching, and the top-N rank.
func recallRanked(r *session, dir, extra string) ([]recallCand, bool) {
	raw, ok := r.readControl(kb.File)
	if !ok {
		return nil, false
	}
	files := recallFiles(dir, extra)
	if len(files) == 0 {
		return nil, false
	}
	rules, ok := recallRules(r, raw)
	if !ok {
		return nil, false
	}
	ranked := rankCands(matchRules(rules, files))
	if len(ranked) > numTopRules {
		ranked = ranked[:numTopRules]
	}
	return ranked, true
}

// renderKBNote ports the kb.txt body, returning the cited ids in rank order.
func renderKBNote(ranked []recallCand) ([]string, string) {
	var note strings.Builder
	note.WriteString("# kb recall — context, never orders. Cited rules inform the review; evidence still decides.\n")
	var ids []string
	for _, c := range ranked {
		fmt.Fprintf(&note, "# %s — %s — precision %d/%d\n%s\n", c.id, c.scope, c.hits, c.judged, strings.ReplaceAll(c.rule, newline, " "))
		ids = append(ids, c.id)
	}
	return ids, note.String()
}

// writeKBManifest ports the kb-recall.json write, warning and dropping it on
// failure while keeping kb.txt.
func writeKBManifest(r *session, mfj, mf string, ranked []recallCand) bool {
	parts := make([]string, len(ranked))
	for i, c := range ranked {
		parts[i] = jq.NewObj(keyID, c.id, "score", c.score, "hits", c.hits, "judged", c.judged).MarshalCompact()
	}
	if err := atomic.WriteFile(mfj, []byte("["+strings.Join(parts, ",")+"]\n")); err != nil {
		_ = os.Remove(mfj)
		r.Warn(fmt.Sprintf("kb recall manifest skipped: could not write %s (%s kept)", mfj, mf))
		return false
	}
	return true
}

// recallFiles ports the recall file list: extra paths plus the diff b-side
// paths, trimmed, deduped, sorted.
func recallFiles(dir, extra string) []string {
	var files []string
	if extra != "" {
		files = append(files, strings.Split(extra, newline)...)
	}
	files = append(files, recallDiffFiles(dir)...)
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || f == devNullPath {
			continue
		}
		out = append(out, f)
	}
	slices.Sort(out)
	return dedupeSorted(out)
}

// recallDiffFiles ports the diff scan for usable b-side paths: the shared
// section index, empty and /dev/null headers dropped.
func recallDiffFiles(dir string) []string {
	raw, err := os.ReadFile(dir + suffixFullDiff)
	if err != nil {
		return nil
	}
	sections := hunks.Files(string(raw))
	var files []string
	for i := range sections {
		if p := sections[i].Path; p != "" && p != devNullPath {
			files = append(files, p)
		}
	}
	return files
}

// recallRule is one active KB row.
type recallRule struct {
	id     string
	scope  string
	rule   string
	hits   int
	judged int
}

// recallRules ports the active-row slurp: unreadable store warns and reads
// absent (callers remove both files).
func recallRules(r *session, raw []byte) ([]recallRule, bool) {
	docs, err := kb.ParseRows(raw)
	if err != nil {
		r.Warn(fmt.Sprintf("kb recall skipped: %s unreadable", kb.File))
		return nil, false
	}
	var out []recallRule
	for _, doc := range docs {
		status, _ := doc.Get(keyStatus)
		if jq.StrOf(status) != stateActive {
			continue
		}
		id, _ := doc.Get(keyID)
		scope, _ := doc.Get("scope")
		rule, _ := doc.Get("rule")
		hits, _ := doc.Get("hits")
		judged, _ := doc.Get("judged")
		out = append(out, recallRule{
			id:     jq.R(id),
			scope:  jq.R(scope),
			rule:   jq.R(rule),
			hits:   jsonInt(hits),
			judged: jsonInt(judged),
		})
	}
	return out, true
}

// atoOrZero parses a rendered JSON number, reading 0 on garbage (the
// shell's `tonumber? // 0` posture for malformed store values).
func atoOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// jsonInt reads a JSON number as an int (missing reads 0).
func jsonInt(v any) int {
	switch t := v.(type) {
	case jq.Number:
		return atoOrZero(t.String())
	case json.Number:
		return atoOrZero(t.String())
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	default:
		return 0
	}
}

// recallCand is one matched rule with its ranker inputs.
type recallCand struct {
	recallRule
	score float64
}

// matchRules ports the scope-glob gate: a rule matches when any of its
// comma-separated scope globs matches any file.
func matchRules(rules []recallRule, files []string) []recallCand {
	var out []recallCand
	for _, rule := range rules {
		if ruleHit(rule.scope, files) {
			score := recallBaseScore
			if rule.judged != 0 {
				score = float64(rule.hits) / float64(rule.judged)
			}
			out = append(out, recallCand{recallRule: rule, score: score})
		}
	}
	return out
}

// ruleHit ports the nested scope/file loop with shell case-pattern matching.
func ruleHit(scope string, files []string) bool {
	for g := range strings.SplitSeq(scope, ",") {
		if globHitsFiles(strings.TrimSpace(g), files) {
			return true
		}
	}
	return false
}

// globHitsFiles ports one scope glob against the file set.
func globHitsFiles(g string, files []string) bool {
	if g == "" {
		return false
	}
	for _, f := range files {
		if f != "" && scopeMatch(g, f) {
			return true
		}
	}
	return false
}

// rankCands ports the single ranker: score desc, judged desc, id asc
// (jq sort_by is stable; ties keep candidate order).
func rankCands(cands []recallCand) []recallCand {
	slices.SortStableFunc(cands, func(a, b recallCand) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		if a.judged != b.judged {
			return cmp.Compare(b.judged, a.judged)
		}
		return strings.Compare(a.id, b.id)
	})
	return cands
}

// scopeMatch ports shell `case "$f" in $g)` pattern matching: `*` crosses
// separators, `?` matches one char, `[...]` matches a class, and `\`
// quotes the next char.
// globState carries one scopeMatch run: the classic wildcard matcher
// with a single pending star (backtracking over one more char per miss).
type globState struct {
	pattern string
	s       string
	px      int
	sx      int
	star    int
	mark    int
}

func scopeMatch(pattern, s string) bool {
	g := &globState{pattern: pattern, s: s, star: -1}
	for g.sx < len(g.s) {
		if !g.step() {
			return false
		}
	}
	for g.px < len(g.pattern) && g.pattern[g.px] == '*' {
		g.px++
	}
	return g.px == len(g.pattern)
}

// step advances one input byte through escape, star, and literal
// matching, backtracking on a miss. False fails the match outright.
func (g *globState) step() bool {
	if g.matchEscape() {
		return true
	}
	if g.matchStar() {
		return true
	}
	if g.matchConsume() {
		return true
	}
	return g.matchBacktrack()
}

// escape ports the backslash escape: a backslash matching the input
// byte consumes both.
func (g *globState) matchEscape() bool {
	if g.px < len(g.pattern) && g.pattern[g.px] == '\\' && g.px+1 < len(g.pattern) && g.pattern[g.px+1] == g.s[g.sx] {
		g.px += 2
		g.sx++
		return true
	}
	return false
}

// star ports the star record: the star position and input mark park
// for later backtracking.
func (g *globState) matchStar() bool {
	if g.px < len(g.pattern) && g.pattern[g.px] == '*' {
		g.star, g.mark, g.px = g.px, g.sx, g.px+1
		return true
	}
	return false
}

// consume ports the literal match: `?`, a class, or the equal byte.
func (g *globState) matchConsume() bool {
	if g.px >= len(g.pattern) {
		return false
	}
	switch g.pattern[g.px] {
	case '?':
		g.px++
		g.sx++
		return true
	case '[':
		if n, ok := matchClass(g.pattern[g.px:], g.s[g.sx]); ok {
			g.px += n
			g.sx++
			return true
		}
		return false
	default:
		if g.pattern[g.px] == g.s[g.sx] {
			g.px++
			g.sx++
			return true
		}
		return false
	}
}

// backtrack ports the mismatch fork: a pending `*` advances over one
// more input char, else the match fails.
func (g *globState) matchBacktrack() bool {
	if g.star == -1 || g.mark >= len(g.s) {
		return false
	}
	g.mark++
	g.px, g.sx = g.star+1, g.mark
	return true
}

// matchClass matches one [...] class against one byte, returning the
// pattern bytes consumed. A missing close reads literal (no match here).
func matchClass(pat string, c byte) (int, bool) {
	i, neg := classNeg(pat)
	if i < len(pat) && pat[i] == ']' {
		i++
	}
	matched, closed, end := classItems(pat, i, c)
	if !closed {
		return 0, false
	}
	if neg {
		matched = !matched
	}
	if !matched {
		return 0, false
	}
	return end, true
}

// classNeg parses the class negation prefix, returning the body start.
func classNeg(pat string) (int, bool) {
	if len(pat) > 1 && (pat[1] == '!' || pat[1] == '^') {
		return 2, true
	}
	return 1, false
}

// classItems scans the class body to its close, reporting whether c
// matched, whether the class closed, and the end index.
func classItems(pat string, i int, c byte) (matched, closed bool, end int) {
	for i < len(pat) {
		if pat[i] == ']' {
			return matched, true, i + 1
		}
		var hit bool
		i, hit = classItem(pat, i, c)
		matched = matched || hit
	}
	return matched, false, i
}

// classItem parses one class item — a char, an escape, or a lo-hi
// range — reporting the new index and whether c matches.
func classItem(pat string, i int, c byte) (int, bool) {
	lo := pat[i]
	i++
	if lo == '\\' && i < len(pat) {
		lo = pat[i]
		i++
	}
	hi := lo
	if i+1 < len(pat) && pat[i] == '-' && pat[i+1] != ']' {
		hi = pat[i+1]
		i += 2
	}
	return i, lo <= c && c <= hi
}
