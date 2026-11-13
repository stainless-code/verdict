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
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/exclude"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// writeKBRecall writes the scoped rules recalled onto a bundle as advisory
// context (kb.txt plus the kb-recall.json machine manifest): the store's
// top-3 ranked rules followed by every matching pack rule. No match, no
// store, or unreadable store: both files removed, no failure.
func writeKBRecall(ctx2 *fetchRunCtx, dir, extra string) {
	r := ctx2.runner
	mf, mfj := dir+suffixKBTxt, dir+suffixKBRecall
	files := recallFiles(dir, withoutExcluded(dir, extra))
	ranked, _ := recallRankedFor(r, files)
	packs := packRecallCands(ctx2.packs, files)
	if len(ranked) == 0 && len(packs) == 0 {
		clearKBRecall(mf, mfj)
		return
	}
	ids, note := renderKBNote(ranked, packs)
	// The atomic write is the one path: a reader never sees a torn note.
	if err := atomic.WriteFile(mf, []byte(note)); err != nil {
		r.Warn(fmt.Sprintf("could not write %s: %v", mf, err))
		return
	}
	if !writeKBManifest(r, mfj, mf, ranked, packs) {
		return
	}
	fmt.Fprintf(r.Human, "» kb: %d rule(s) -> %s (%s)\n", len(ranked)+len(packs), mf, strings.Join(ids, ", "))
}

// packRecallCands renders the matching pack rules as KB candidates: pack
// rules carry no judged counters, so they rank at the unjudged base score
// and are always included rather than competing for the store's top 3.
func packRecallCands(set *pack.Set, files []string) []recallCand {
	rules := set.RulesFor(files)
	out := make([]recallCand, 0, len(rules))
	for i := range rules {
		rule := &rules[i]
		out = append(out, recallCand{
			recallRule{
				id: rule.ID, scope: strings.Join(rule.Globs, ","),
				rule: strings.ReplaceAll(strings.TrimSpace(string(rule.Body)), newline, " "),
			},
			recallBaseScore,
			rule.Level,
			"pack:" + rule.Pack + "@" + rule.PackVersion,
		})
	}
	return out
}

// clearKBRecall removes both recall stores when the run has nothing to say.
func clearKBRecall(mf, mfj string) {
	_ = os.Remove(mf)
	_ = os.Remove(mfj)
}

// recallRankedFor owns the recall pipeline: store read, active rules, scope
// matching, and the top-N rank.
func recallRankedFor(r *session, files []string) ([]recallCand, bool) {
	raw, ok := r.readControl(kb.File)
	if !ok || len(files) == 0 {
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

// renderKBNote owns the kb.txt body, returning the cited ids in rank order:
// the store's ranked rules first, then every matching pack rule.
func renderKBNote(ranked, packs []recallCand) ([]string, string) {
	var note strings.Builder
	note.WriteString("# kb recall — context, never orders. Cited rules inform the review; evidence still decides.\n")
	var ids []string
	for _, c := range ranked {
		fmt.Fprintf(&note, "# %s — %s — precision %d/%d\n%s\n", c.id, c.scope, c.hits, c.judged, strings.ReplaceAll(c.rule, newline, " "))
		ids = append(ids, c.id)
	}
	for _, c := range packs {
		fmt.Fprintf(&note, "# %s — %s — severity %s — source %s\n%s\n",
			c.id, c.scope, c.severity, c.source, strings.ReplaceAll(c.rule, newline, " "))
		ids = append(ids, c.id)
	}
	return ids, note.String()
}

// writeKBManifest owns the kb-recall.json write, warning and dropping it on
// failure while keeping kb.txt. Pack rows add their severity and source;
// store rows keep their historical keys.
func writeKBManifest(r *session, mfj, mf string, ranked, packs []recallCand) bool {
	all := append(append([]recallCand{}, ranked...), packs...)
	parts := make([]string, len(all))
	for i, c := range all {
		obj := jq.NewObj(keyID, c.id, "score", c.score, "hits", c.hits, "judged", c.judged)
		if c.severity != "" {
			obj.Set("severity", c.severity)
		}
		if c.source != "" {
			obj.Set("source", c.source)
		}
		parts[i] = obj.MarshalCompact()
	}
	if err := atomic.WriteFile(mfj, []byte("["+strings.Join(parts, ",")+"]\n")); err != nil {
		_ = os.Remove(mfj)
		r.Warn(fmt.Sprintf("kb recall manifest skipped: could not write %s (%s kept)", mfj, mf))
		return false
	}
	return true
}

// recallFiles owns the recall file list: extra paths plus the diff b-side
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

// recallDiffFiles owns the diff scan for usable b-side paths: the shared
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

// recallRules owns the active-row slurp: unreadable store warns and reads
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

// atoOrZero parses a rendered JSON number, reading 0 on garbage so a
// malformed store value never blocks the recall.
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

// recallCand is one matched rule with its ranker inputs and, for a pack
// rule, the severity and source its KB row carries.
//
//nolint:govet // the embedded rule stays first so the row reads top-down
type recallCand struct {
	recallRule
	score    float64
	severity string
	source   string
}

// matchRules owns the scope-glob gate: a rule matches when any of its
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

func ruleHit(scope string, files []string) bool {
	for g := range strings.SplitSeq(scope, ",") {
		if globHitsFiles(strings.TrimSpace(g), files) {
			return true
		}
	}
	return false
}

// globHitsFiles handles one scope glob against the file set.
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

// rankCands owns the single ranker: score desc, judged desc, id asc
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

func scopeMatch(pattern, path string) bool { return exclude.Match(pattern, path) }
