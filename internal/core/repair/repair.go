// Package repair renders the run verb's repair-loop prompts: the follow-up
// directive a round-k agent receives after round k-1 left an invalid review,
// and the shape check that decides whether another round runs. Round 1 keeps
// the launcher's one-shot directive byte-identical; later rounds append the
// prior review bytes and a feedback block carrying the checker's problems,
// both fenced with the run's nonce.
package repair

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// feedbackCap bounds the problems JSON a follow-up prompt carries.
const feedbackCap = 8192

// Nonce makes the run's untrusted-fence nonce: 16 lowercase hex chars from 8
// crypto-random bytes, the same shape fetch fences its dumps with. One nonce
// covers every round of the run.
func Nonce() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RoundPrompt appends round k's repair context to the base directive: the
// prior round's review bytes and the feedback block, each in its own
// nonce-fenced section.
func RoundPrompt(base, nonce string, prior []byte, round, total int, problems []core.Problem) string {
	var sb strings.Builder
	sb.WriteString(base)
	if !strings.HasSuffix(base, "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(fenceOpen(nonce, fmt.Sprintf("review.json:round-%d", round-1)))
	sb.Write(prior)
	if len(prior) == 0 || prior[len(prior)-1] != '\n' {
		sb.WriteByte('\n')
	}
	sb.WriteString(fenceClose(nonce))
	sb.WriteString(fenceOpen(nonce, "repair-feedback"))
	sb.WriteString(Feedback(round, total, problems))
	sb.WriteString(fenceClose(nonce))
	return sb.String()
}

// Feedback renders the fix list: the round counter, the checker's problems
// with errors first, and the fixed tail naming the only accepted reply.
func Feedback(round, total int, problems []core.Problem) string {
	errors, warnings := countLevels(problems)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Round %d/%d: the review.json above has %d error(s) and %d warning(s).\n", round, total, errors, warnings)
	sb.WriteString("The JSON below is the shape checker's problem list for that file.\n")
	rendered, omitted := renderProblems(problems)
	sb.WriteString(rendered)
	sb.WriteByte('\n')
	if omitted > 0 {
		fmt.Fprintf(&sb, "(+%d more problem(s) omitted for size)\n", omitted)
	}
	sb.WriteString("Fix every error. Reply with one JSON document only.\n")
	return sb.String()
}

// Check runs the published shape checker over one round's review bytes: the
// same normalize-then-check pair record's dry run uses, in lenient mode, so a
// warnings-only review reads as valid and never triggers another round. A
// missing full.diff checks against an empty diff.
func Check(dir string, review []byte) recordcore.CheckResult {
	normalized, repairs := recordcore.NormalizeReview(review)
	diff, readErr := os.ReadFile(dir + "/full.diff")
	diffText := ""
	if readErr == nil {
		diffText = string(diff)
	}
	return recordcore.MergeRepairs(recordcore.CheckReview(normalized, diffText), repairs, normalized)
}

// Valid reports whether the check result lets the loop stop: no errors.
// Warnings are reported in the attempt but never by themselves run another
// round.
func Valid(res recordcore.CheckResult) bool {
	return res.Errors == 0
}

// Problems returns the checker's problems with errors first, the order the
// feedback block and the recorded attempt carry.
func Problems(res recordcore.CheckResult) []core.Problem {
	return errorsFirst(res.Problems)
}

func errorsFirst(problems []core.Problem) []core.Problem {
	ordered := slices.Clone(problems)
	slices.SortStableFunc(ordered, func(a, b core.Problem) int {
		return levelRank(a.Level) - levelRank(b.Level)
	})
	return ordered
}

func levelRank(level string) int {
	if level == core.ProblemLevelError {
		return 0
	}
	return 1
}

func countLevels(problems []core.Problem) (errors, warnings int) {
	for _, p := range problems {
		if p.Level == core.ProblemLevelError {
			errors++
		} else {
			warnings++
		}
	}
	return errors, warnings
}

// renderProblems marshals the problems with errors first, keeping as many
// leading entries as fit under the cap (always the first, so a single huge
// problem still reaches the agent) and counting the rest as omitted.
func renderProblems(problems []core.Problem) (string, int) {
	ordered := errorsFirst(problems)
	raws := marshalProblems(ordered)
	kept := ordered[:0:0]
	size := 2
	for i := range ordered {
		if raws[i] == nil {
			break
		}
		if !keptFits(size, i, len(raws[i])) {
			break
		}
		kept = append(kept, ordered[i])
		size += len(raws[i])
		if i > 0 {
			size++
		}
	}
	return marshalKept(kept), len(ordered) - len(kept)
}

// marshalProblems renders every problem once, so the fit loop below stays a
// straight scan. A nil entry is an unmarshable problem, which never happens
// for the checker's plain shapes.
func marshalProblems(ordered []core.Problem) [][]byte {
	raws := make([][]byte, len(ordered))
	for i, p := range ordered {
		if raw, err := json.Marshal(p); err == nil {
			raws[i] = raw
		}
	}
	return raws
}

// keptFits reports whether one more marshaled problem fits under the cap.
// The first entry always fits, so a single huge problem still reaches the
// agent instead of an empty list.
func keptFits(size, i, raw int) bool {
	if i == 0 {
		return true
	}
	return size+1+raw <= feedbackCap
}

func marshalKept(kept []core.Problem) string {
	if kept == nil {
		return "[]"
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func fenceOpen(nonce, source string) string {
	return fmt.Sprintf("<<<UNTRUSTED nonce=%s source=%s>>>\n", nonce, source)
}

func fenceClose(nonce string) string {
	return fmt.Sprintf("<<<END UNTRUSTED nonce=%s>>>\n", nonce)
}
