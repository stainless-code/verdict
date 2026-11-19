package run

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// salvageWarning names what the binary repaired, not what the agent claims.
// It rides the run's warning list and the refusal or success envelope alike.
const salvageWarning = "review.json was missing or still the empty placeholder; salvaged the last fenced json block from the agent's stdout"

// salvageAllowed gates a repair on a clean process: a crash, a watchdog kill,
// a non-zero exit, or a canceled context leaves stdout as evidence only. The
// run still refuses on the missing artifact, so a failed agent cannot bury a
// review behind its failure.
func salvageAllowed(ctx context.Context, res *core.Result, spawnErr error) bool {
	return ctx.Err() == nil && spawnErr == nil && res != nil && !res.TimedOut && res.ExitCode == 0
}

// salvageReview writes a findings document the agent printed but never saved.
// An absent artifact qualifies, as does the exact empty placeholder the
// directive told the agent to replace. Any other file stays the agent's own
// artifact: a stale or malformed file is never overwritten. The artifact
// assertion and the shared checker run after this repair.
func (st *state) salvageReview(args *launchArgs, stdout string) bool {
	if st.drainDigest != "" || !canReplaceReview(args.abs) {
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

// canReplaceReview accepts an absent artifact or the exact empty placeholder.
// Any other file, including a stale or malformed one, belongs to the agent.
func canReplaceReview(path string) bool {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true
	case err != nil:
		return false
	case !info.Mode().IsRegular() || info.Size() > maxRoundReviewBytes:
		return false
	}
	raw, err := os.ReadFile(path)
	return err == nil && isPlaceholderReview(raw)
}

func isJSONFence(line string) bool {
	info, fenced := strings.CutPrefix(strings.TrimSpace(line), "```")
	return fenced && strings.TrimSpace(info) == "json"
}

// fencedJSONBlock returns one complete ```json body and its closing line.
// A new opening fence before a close abandons the unclosed draft and hands
// the new opening back to the caller, so the draft's bytes never join the
// block that follows it. A malformed block never hides a later valid one.
func fencedJSONBlock(lines []string, opening int) ([]byte, int) {
	for j := opening + 1; j < len(lines); j++ {
		if isJSONFence(lines[j]) {
			return nil, j - 1
		}
		if strings.TrimSpace(lines[j]) == "```" {
			return []byte(strings.Join(lines[opening+1:j], "")), j
		}
	}
	return nil, len(lines)
}

func fencedJSONBlocks(stdout string) [][]byte {
	lines := strings.SplitAfter(stdout, "\n")
	var blocks [][]byte
	for i := 0; i < len(lines); {
		if !isJSONFence(lines[i]) {
			i++
			continue
		}
		block, end := fencedJSONBlock(lines, i)
		if block != nil {
			blocks = append(blocks, block)
		}
		i = end + 1
	}
	return blocks
}

// lastFindingsBlock returns the last fenced json block whose root is an object
// with a findings array. Missing schema_version is the only byte this scanner
// adds; every other field, including an unknown one, goes to the shared
// checker unchanged so run and record agree on the verdict.
func lastFindingsBlock(stdout string) ([]byte, bool) {
	for _, block := range slices.Backward(fencedJSONBlocks(stdout)) {
		if doc, ok := findingsDocument(block); ok {
			return doc, true
		}
	}
	return nil, false
}

func findingsDocument(block []byte) ([]byte, bool) {
	doc, err := jq.DecodeOrdered(bytes.TrimSpace(block))
	if err != nil {
		return nil, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return nil, false
	}
	findings, ok := obj.Get("findings")
	if !ok {
		return nil, false
	}
	if _, ok := findings.([]any); !ok {
		return nil, false
	}
	if _, ok := obj.Get("schema_version"); !ok {
		obj.Set("schema_version", "1")
		return []byte(obj.MarshalCompact()), true
	}
	return bytes.TrimSpace(block), true
}

// isPlaceholderReview recognizes only the exact first-write document: two
// keys, schema_version "1", and an empty findings array. A clean review with
// a summary, or any extra field, is finished work and is never overwritten.
func isPlaceholderReview(raw []byte) bool {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || len(obj.Keys) != 2 {
		return false
	}
	version, hasVersion := obj.Get("schema_version")
	findings, hasFindings := obj.Get("findings")
	if !hasVersion || !hasFindings || jq.StrOf(version) != "1" {
		return false
	}
	items, ok := findings.([]any)
	return ok && len(items) == 0
}
