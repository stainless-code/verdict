package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// adapterToolCallKind labels a sample whose adapter answered with tool-call
// markup: the model was asked for a review and produced a tool invocation the
// tool-free harness cannot run.
const adapterToolCallKind = "adapter_tool_call"

// toolCallMarks are the case-insensitive substrings that name tool-call
// markup. They only ever label an already-failed sample, never a pass.
var toolCallMarks = []string{
	"tool_call",
	"tool-call",
	"toolcall",
	"function_calls",
	"<invoke",
	"<function",
	"antml:tool",
	"dsml",
	"invoke name=",
}

// looksLikeToolCall reports tool-call markup in an adapter log.
func looksLikeToolCall(log string) bool {
	lower := strings.ToLower(log)
	for _, mark := range toolCallMarks {
		if strings.Contains(lower, mark) {
			return true
		}
	}
	return false
}

// validJSONFile reports a review file the adapter actually produced.
func validJSONFile(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Valid(raw)
}

// failureKind classifies a failed sample. A sample that produced a review
// file is never reclassified, even when its log mentions tool markup.
func (r *runner) failureKind(cr *caseRun) string {
	review, log := filepath.Join(cr.dir, reviewJSONName), filepath.Join(cr.dir, agentLogName)
	if r.cfg.Loop > 1 {
		if dir := latestRoundDir(cr.dir); dir != "" {
			review, log = filepath.Join(dir, reviewJSONName), filepath.Join(dir, agentLogName)
		}
	}
	if validJSONFile(review) {
		return ""
	}
	raw, err := os.ReadFile(log)
	if err != nil || !looksLikeToolCall(string(raw)) {
		return ""
	}
	return adapterToolCallKind
}

// latestRoundDir names the highest round dir under the sample dir.
func latestRoundDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, "round-") && name > best {
			best = name
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best)
}
