package run

import (
	"bytes"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// salvageWarning names what the binary repaired, not what the agent claims.
// It rides the run's warning list and the refusal or success envelope alike.
const salvageWarning = "review.json was missing; salvaged the last fenced json block from the agent's stdout"

// salvageReview writes a findings document the agent printed but never saved.
// Only an absent artifact qualifies: a stale or malformed file stays the
// agent's own artifact, and drain's replies contract is never salvaged as a
// review. A malformed or absent block changes nothing, so the artifact
// assertion still reports no-review-artifact.
func (st *state) salvageReview(args *launchArgs, stdout string) bool {
	if st.drainDigest != "" {
		return false
	}
	if _, err := os.Stat(args.abs); err == nil {
		return false
	}
	block, ok := lastFindingsBlock(stdout)
	if !ok {
		return false
	}
	if err := atomic.WriteFile(args.abs, block); err != nil {
		st.warn("could not salvage review.json from the agent's stdout: " + err.Error())
		return false
	}
	st.warn(salvageWarning)
	return true
}

func isJSONFence(line string) bool {
	info, fenced := strings.CutPrefix(strings.TrimSpace(line), "```")
	return fenced && strings.TrimSpace(info) == "json"
}

// fencedJSONBlocks collects the bodies of complete ```json fences. A later
// malformed block never hides an earlier valid one.
func fencedJSONBlock(lines []string, opening int) ([]byte, int) {
	for j := opening + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) != "```" {
			continue
		}
		return []byte(strings.Join(lines[opening+1:j], "")), j
	}
	return nil, opening
}

func fencedJSONBlocks(stdout string) [][]byte {
	lines := strings.SplitAfter(stdout, "\n")
	var blocks [][]byte
	for i := 0; i < len(lines); i++ {
		if !isJSONFence(lines[i]) {
			continue
		}
		block, end := fencedJSONBlock(lines, i)
		if block != nil {
			blocks = append(blocks, block)
		}
		i = end
	}
	return blocks
}

// lastFindingsBlock returns the last fenced json block that is a findings
// document.
func lastFindingsBlock(stdout string) ([]byte, bool) {
	for _, block := range slices.Backward(fencedJSONBlocks(stdout)) {
		got := bytes.TrimSpace(block)
		if isFindingsDocument(got) {
			return got, true
		}
	}
	return nil, false
}

var (
	findingSeverities  = []string{"critical", "important", "informational"}
	findingConfidences = []string{"high", "medium", "low"}
)

// isFindingsDocument checks the published findings shape without a diff: a
// finding outside the reviewed hunks is still a document, while a malformed
// or incomplete one is not salvageable evidence. The required fields and
// their enums are spelled here, not read from the JSON under test.
func isFindingsDocument(block []byte) bool {
	doc, err := jq.Decode(block)
	if err != nil {
		return false
	}
	obj, ok := jq.AsMap(doc)
	if !ok || jq.StrOf(obj["schema_version"]) != "1" {
		return false
	}
	findings, ok := obj["findings"].([]any)
	if !ok {
		return false
	}
	for _, item := range findings {
		if !isFinding(item) {
			return false
		}
	}
	return true
}

func isFinding(item any) bool {
	f, ok := jq.AsMap(item)
	return ok && findingFieldsValid(f) && findingEnumsValid(f)
}

func findingFieldsValid(f map[string]any) bool {
	if jq.StrOf(f["path"]) == "" {
		return false
	}
	if _, ok := jq.IntOf(f["line"]); !ok {
		return false
	}
	return jq.StrOf(f["claim"]) != "" && jq.StrOf(f["quote"]) != ""
}

func findingEnumsValid(f map[string]any) bool {
	if !slices.Contains(findingSeverities, jq.StrOf(f["severity"])) {
		return false
	}
	return slices.Contains(findingConfidences, jq.StrOf(f["confidence"]))
}
