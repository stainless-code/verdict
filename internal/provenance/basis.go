package provenance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func collectCited(m *jq.Obj, cited map[string]bool) {
	kbl, ok := m.Get("kb")
	if !ok {
		return
	}
	arr, ok := kbl.([]any)
	if !ok {
		return
	}
	for _, id := range arr {
		cited[jq.Compact(id)] = true
	}
}

// ReviewBasisCounts derives the expected Basis numbers from
// the review's own comments (rigor normalized, distinct raw kb ids) plus the
// verified count scoped to this run's addressed/triaged flips at now.
func ReviewBasisCounts(review *jq.Obj, entries []any, now string) string {
	counts := map[string]int{}
	cited := map[string]bool{}
	for _, c := range reviewComments(review) {
		m, ok := c.(*jq.Obj)
		if !ok {
			continue
		}
		counts[commentRigor(m)]++
		collectCited(m, cited)
	}
	return renderBasis(counts, cited, runVerified(entries, now))
}

func reviewComments(review *jq.Obj) []any {
	if review == nil {
		return nil
	}
	if c, ok := review.Get("comments"); ok {
		return jq.Elements(c)
	}
	return nil
}

// commentRigor normalizes one comment's rigor rung: null/false/missing read
// said, the rung the checker fills; unknown strings read pointed.
func commentRigor(m *jq.Obj) string {
	rigorRaw, _ := m.Get("rigor")
	if rigorRaw == nil || rigorRaw == false {
		return rigorSaid
	}
	if s, isStr := rigorRaw.(string); isStr {
		switch s {
		case rigorReproduced, rigorRan, rigorWalked, rigorPointed, rigorSaid:
			return s
		}
	}
	return rigorPointed
}

func runVerified(entries []any, now string) int {
	verified := 0
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok {
			continue
		}
		if flippedAtNow(m, now) {
			verified++
		}
	}
	return verified
}

func flippedAtNow(m *jq.Obj, now string) bool {
	if st, _ := m.Get("status"); st != outcomeAddressed {
		return false
	}
	histRaw, _ := m.Get("history")
	hist, ok := histRaw.([]any)
	if !ok {
		return false
	}
	for _, h := range hist {
		if flipAtNow(h, now) {
			return true
		}
	}
	return false
}

func flipAtNow(h any, now string) bool {
	hm, ok := h.(*jq.Obj)
	if !ok {
		return false
	}
	if at, _ := hm.Get("at"); at != now {
		return false
	}
	ev, _ := hm.Get("event")
	return ev == "addressed" || ev == "triaged"
}

func renderBasis(counts map[string]int, cited map[string]bool, verified int) string {
	parts := []string{
		strconv.Itoa(counts["reproduced"]),
		strconv.Itoa(counts["ran"]),
		strconv.Itoa(counts["walked"]),
		strconv.Itoa(counts[rigorPointed]),
		strconv.Itoa(counts["said"]),
		strconv.Itoa(len(cited)),
		strconv.Itoa(verified),
	}
	return strings.Join(parts, " ")
}

var (
	basisLineRe = regexp.MustCompile(`(?m)^[ \t]*> Basis:`)
	digitRunRe  = regexp.MustCompile(`[0-9]+`)
)

// ApplyBasisLine replaces the body's first "> Basis:" line with the computed
// counts, inserting one under the confidence lead when the body carries none.
// It is the only prose byte the CLI writes; a matching line is left untouched.
func ApplyBasisLine(expected, body string) (string, *core.Problem) {
	lines := strings.Split(body, newline)
	computed := "> Basis: " + expected
	if i, found := basisLineIndex(lines); found {
		return replaceBasisLine(body, lines, i, computed, expected)
	}
	return insertBasisLine(lines, computed)
}

// basisLineIndex finds the first "> Basis:" line.
func basisLineIndex(lines []string) (int, bool) {
	for i, line := range lines {
		if basisLineRe.MatchString(line) {
			return i, true
		}
	}
	return 0, false
}

// replaceBasisLine swaps a disagreeing Basis line for the computed counts;
// a matching line leaves the body untouched.
func replaceBasisLine(body string, lines []string, i int, computed, expected string) (string, *core.Problem) {
	nums := strings.Join(digitRunRe.FindAllString(lines[i], -1), " ")
	if nums == expected {
		return body, nil
	}
	lines[i] = computed
	return strings.Join(lines, newline), &core.Problem{
		Path: "$.body", Code: core.ProblemCodeBasisReplaced, Level: core.ProblemLevelWarning,
		Message: fmt.Sprintf(
			"basis line counts (%s) differ from the computed counts (%s) — replaced it (basis-replaced)",
			nums, expected),
		Fix:     "write the computed Basis line, or leave the line to the CLI",
		Example: computed,
	}
}

// insertBasisLine adds the computed Basis line under the confidence lead.
func insertBasisLine(lines []string, computed string) (string, *core.Problem) {
	at := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		at = i + 1
	}
	lines = append(lines[:at], append([]string{computed}, lines[at:]...)...)
	return strings.Join(lines, newline), &core.Problem{
		Path: "$.body", Code: core.ProblemCodeBasisReplaced, Level: core.ProblemLevelWarning,
		Message: "no basis line — inserted the computed counts (basis-replaced)",
		Fix:     "write the computed Basis line, or leave the line to the CLI",
		Example: computed,
	}
}

// EvidenceBaseURL returns the blob base for GitHub bundles,
// "" otherwise.
func EvidenceBaseURL(dir, repo string) string {
	m := providerRecord(dir)
	if m == nil {
		return ""
	}
	if prov := jq.StrOf(m["provider"]); prov != "" && prov != providerGitHub {
		return ""
	}
	if url := jq.StrOf(m["url"]); url != "" {
		if head, _, ok := strings.Cut(url, "/pull/"); ok {
			return head
		}
		return url
	}
	if repo != "" {
		return "https://github.com/" + repo
	}
	return ""
}

func providerRecord(dir string) map[string]any {
	raw, err := os.ReadFile(filepath.Join(dir, prFile))
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
	return m
}

// EvidenceLinkable reports "true" when this bundle posts to
// GitHub with a known reviewed SHA.
func EvidenceLinkable(dir, sha string) string {
	if sha == "" {
		return validationFalse
	}
	raw, err := os.ReadFile(filepath.Join(dir, prFile))
	if err != nil {
		return validationFalse
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return validationFalse
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return validationFalse
	}
	if prov := jq.StrOf(m["provider"]); prov != "" && prov != providerGitHub {
		return validationFalse
	}
	return core.FlagOn
}

var blobSHARe = regexp.MustCompile(`blob/[0-9a-fA-F]{7,40}/`)

// ValidateEvidenceLinks checks the evidence links: blob SHAs must name
// the reviewed commit, and long quotes stay inside collapsed details.
func ValidateEvidenceLinks(body, sha, linkable string) []string {
	var warns []string
	shas := bodySHAs(body)
	if w := checkBlobSHAs(shas, sha, linkable); w != "" {
		warns = append(warns, w)
	}
	for _, s := range mismatchedSHAs(shas, sha, linkable) {
		warns = append(warns, fmt.Sprintf("evidence link SHA '%s' differs from the reviewed SHA '%s' — keeping "+
			"body as-is (links stay pinned to the reviewed commit, never live HEAD)", s, readcommon.ShortSHA(sha)))
	}
	if longQuoteOutsideDetails(body) {
		warns = append(warns, "code quote longer than 3 lines outside collapsed details — keeping body as-is "+
			"(link-first: quote inside <details> or cut it, the link already points at the lines)")
	}
	return warns
}

// mismatchedSHAs lists blob SHAs that do not prefix-match the reviewed SHA.
func mismatchedSHAs(shas []string, sha, linkable string) []string {
	if len(shas) == 0 || linkable != core.FlagOn || sha == "" {
		return nil
	}
	var out []string
	for _, s := range shas {
		if !strings.HasPrefix(sha, s) {
			out = append(out, s)
		}
	}
	return out
}

// checkBlobSHAs warns when blob links cannot resolve to the reviewed commit.
func checkBlobSHAs(shas []string, sha, linkable string) string {
	if len(shas) == 0 {
		return ""
	}
	switch {
	case linkable != core.FlagOn:
		return "body carries GitHub blob link(s) on a non-GitHub bundle — keeping " +
			"body as-is (plain path:line degrades gracefully, blob URLs would 404)"
	case sha == "":
		return "body carries blob link(s) but the reviewed SHA is unknown — keeping body as-is"
	default:
		return ""
	}
}

// bodySHAs lists the distinct blob SHAs in the body, byte-sorted.
func bodySHAs(body string) []string {
	seen := map[string]bool{}
	var shas []string
	for _, m := range blobSHARe.FindAllString(body, -1) {
		s := strings.TrimSuffix(strings.TrimPrefix(m, "blob/"), "/")
		if !seen[s] {
			seen[s] = true
			shas = append(shas, s)
		}
	}
	// LC_ALL=C sort -u: byte order. shas are hex; insertion order is close
	// but not guaranteed — sort for fidelity.
	sortHex(shas)
	return shas
}

// sortHex sorts hex strings in byte order (LC_ALL=C sort -u).
func sortHex(s []string) {
	slices.Sort(s)
}

// longQuoteOutsideDetails owns the evidence-link awk verbatim: <details>
// depth tracks by substring anywhere in the line (even inside fences), ```
// fences toggle only on exact ``` or ^```[^`] lines, and content lines count
// only outside details.
func longQuoteOutsideDetails(body string) bool {
	st := &quoteScan{}
	for line := range strings.SplitSeq(body, "\n") {
		st.feed(line)
	}
	st.finish()
	return st.bad
}

// quoteScan tracks details depth and fence state for the quote check.
type quoteScan struct {
	detail  int
	n       int
	inFence bool
	bad     bool
}

// feed consumes one body line.
func (st *quoteScan) feed(line string) {
	switch {
	case strings.Contains(line, "<details"):
		st.detail++
	case strings.Contains(line, "</details>"):
		if st.detail > 0 {
			st.detail--
		}
	case line == fencePrefix || fenceOpenLine(line):
		st.toggleFence()
	default:
		if st.inFence && st.detail == 0 {
			st.n++
		}
	}
}

// toggleFence opens or closes a fence, flagging long outside quotes.
func (st *quoteScan) toggleFence() {
	if st.inFence {
		if st.detail == 0 && st.n > minQuoteLines {
			st.bad = true
		}
		st.inFence = false
		st.n = 0
		return
	}
	st.inFence = true
	st.n = 0
}

// finish flags a trailing unterminated long quote. Called after the last
// line: the awk counts only closed fences, and an unterminated fence reads
// as still-open (swallow-to-EOF).
func (st *quoteScan) finish() {
	if st.inFence && st.detail == 0 && st.n > minQuoteLines {
		st.bad = true
	}
}

// fencePrefix is the markdown fence marker.
const fencePrefix = "```"

// fenceOpenLine is the opening-fence test ($0 == "```" || $0 ~ /^```[^`]/).
func fenceOpenLine(line string) bool {
	if line == fencePrefix {
		return true
	}
	return len(line) > len(fencePrefix) && line[:len(fencePrefix)] == fencePrefix && line[len(fencePrefix)] != '`'
}

// Basis-line parsing and rendering constants.
const (
	// validationFalse is the provider-validation failure rendering.
	validationFalse = "false"
	// Rigor bucket names for the seven-count basis line.
	rigorReproduced = "reproduced"
	rigorRan        = "ran"
	rigorWalked     = "walked"
	rigorSaid       = "said"
	// outcomeAddressed is the verified status.
	outcomeAddressed = "addressed"
	// rigorPointed names the pointed rigor bucket.
	rigorPointed = "pointed"
	// providerGitHub is the hosted provider that skips validation.
	providerGitHub = "github"
	// minQuoteLines is the outside-details quote length that warns.
	minQuoteLines = 3
)
