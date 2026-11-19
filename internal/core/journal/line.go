package journal

import (
	"fmt"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// StartLine is the opening record appended before a bundle-writing verb runs.
type StartLine struct {
	SchemaVersion string   `const:"1"          json:"schema_version"`
	Phase         string   `const:"start"      json:"phase"`
	Repo          string   `json:"repo"`
	Step          string   `json:"step"`
	Verb          string   `json:"verb"`
	Channel       string   `enum:"cli|mcp"     json:"channel"`
	Version       string   `json:"version"`
	StartedAt     string   `format:"date-time" json:"started_at"`
	Argv          []string `json:"argv"`
	PID           int      `json:"pid"         min:"0"`
}

// BundleDigests records the pre- and post-invocation canonical
// content-manifest sha256 digests of the bundle directory. Before is nil when
// the directory did not exist before the call. Unobserved marks a call whose
// content sweep failed: the digests and the file diff are unknown rather than
// empty.
type BundleDigests struct {
	Before     *string `json:"before"`
	After      string  `json:"after"`
	Unobserved bool    `json:"unobserved,omitempty"`
}

// FileRecord records one file written or changed during an invocation.
type FileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"  min:"0"`
}

// EndLine is the closing record appended after a bundle-writing verb returns.
type EndLine struct {
	SchemaVersion string        `const:"1"          json:"schema_version"`
	Phase         string        `const:"end"        json:"phase"`
	Repo          string        `json:"repo"`
	Step          string        `json:"step"`
	Verb          string        `json:"verb"`
	Channel       string        `enum:"cli|mcp"     json:"channel"`
	StartedAt     string        `format:"date-time" json:"started_at"`
	EndedAt       string        `format:"date-time" json:"ended_at"`
	Refusal       *string       `json:"refusal"`
	Next          string        `json:"next"`
	Bundle        BundleDigests `json:"bundle"`
	Wrote         []FileRecord  `json:"wrote"`
	Removed       []string      `json:"removed"`
	PID           int           `json:"pid"         min:"0"`
	Exit          int           `json:"exit"        min:"0"`
	Warnings      int           `json:"warnings"    min:"0"`
}

// TruncatedLine records a whole-line truncation rewrite when the journal file
// exceeded [MaxBytes].
type TruncatedLine struct {
	SchemaVersion string `const:"1"          json:"schema_version"`
	Phase         string `const:"truncated"  json:"phase"`
	Repo          string `json:"repo"`
	At            string `format:"date-time" json:"at"`
	KeptFrom      string `format:"date-time" json:"kept_from"`
	Dropped       int    `json:"dropped"     min:"0"`
}

// Step is one completed invocation folded from its start and end lines.
type Step struct {
	Step       string        `json:"step"`
	Verb       string        `json:"verb"`
	Channel    string        `enum:"cli|mcp"     json:"channel"`
	Version    string        `json:"version"`
	StartedAt  string        `format:"date-time" json:"started_at"`
	EndedAt    string        `format:"date-time" json:"ended_at"`
	Refusal    *string       `json:"refusal"`
	Next       string        `json:"next"`
	Bundle     BundleDigests `json:"bundle"`
	Argv       []string      `json:"argv"`
	Wrote      []FileRecord  `json:"wrote"`
	Removed    []string      `json:"removed"`
	PID        int           `json:"pid"         min:"0"`
	Exit       int           `json:"exit"        min:"0"`
	Warnings   int           `json:"warnings"    min:"0"`
	DurationMs int64         `json:"duration_ms" min:"0"`
}

// UnfinishedStep is a start line with no matching end line (a crash or
// in-flight invocation).
type UnfinishedStep struct {
	Step      string   `json:"step"`
	Verb      string   `json:"verb"`
	Channel   string   `enum:"cli|mcp"     json:"channel"`
	Version   string   `json:"version"`
	StartedAt string   `format:"date-time" json:"started_at"`
	Argv      []string `json:"argv"`
	PID       int      `json:"pid"         min:"0"`
}

// ChainBreak reports a bundle content-manifest mismatch between one completed
// step's post-state digest and the next step's pre-state digest.
type ChainBreak struct {
	Step          string `json:"step"`
	Before        string `json:"before"`
	PreviousAfter string `json:"previous_after"`
}

// TruncatedInfo summarizes the most recent truncation notice in the journal.
type TruncatedInfo struct {
	At       *string `format:"date-time" json:"at"`
	KeptFrom *string `format:"date-time" json:"kept_from"`
	Dropped  int     `json:"dropped"     min:"0"`
}

// Summary is the folded read view of a bundle's journal.
type Summary struct {
	Bundle          string           `json:"bundle"`
	Truncation      TruncatedInfo    `json:"truncation"`
	Steps           []Step           `json:"steps"`
	Unfinished      []UnfinishedStep `json:"unfinished"`
	ChainBreaks     []ChainBreak     `json:"chain_breaks"`
	UnreadableLines int              `json:"unreadable_lines" min:"0"`
}

// Wire renders the folded journal summary in published wire key order.
func (s *Summary) Wire() *jq.Obj {
	steps := make([]any, 0, len(s.Steps))
	for i := range s.Steps {
		steps = append(steps, stepObj(&s.Steps[i]))
	}
	unfinished := make([]any, 0, len(s.Unfinished))
	for i := range s.Unfinished {
		unfinished = append(unfinished, unfinishedObj(&s.Unfinished[i]))
	}
	breaks := make([]any, 0, len(s.ChainBreaks))
	for i := range s.ChainBreaks {
		breaks = append(breaks, chainBreakObj(&s.ChainBreaks[i]))
	}
	truncation := jq.NewObj(
		"dropped", s.Truncation.Dropped,
		"at", ptrStringVal(s.Truncation.At),
		"kept_from", ptrStringVal(s.Truncation.KeptFrom),
	)
	return jq.NewObj(
		"bundle", s.Bundle,
		"steps", steps,
		"unfinished", unfinished,
		"chain_breaks", breaks,
		"truncation", truncation,
		"unreadable_lines", s.UnreadableLines,
	)
}

func stepObj(st *Step) *jq.Obj {
	wrote := make([]any, 0, len(st.Wrote))
	for i := range st.Wrote {
		wrote = append(wrote, jq.NewObj(
			"path", st.Wrote[i].Path,
			"sha256", st.Wrote[i].SHA256,
			"bytes", st.Wrote[i].Bytes,
		))
	}
	bundleObj := bundleDigestsObj(st.Bundle)
	return jq.NewObj(
		"step", st.Step,
		"verb", st.Verb,
		"channel", st.Channel,
		"pid", st.PID,
		"argv", stringSliceVal(st.Argv),
		"version", st.Version,
		"started_at", st.StartedAt,
		"ended_at", st.EndedAt,
		"exit", st.Exit,
		"refusal", ptrStringVal(st.Refusal),
		"warnings", st.Warnings,
		"bundle", bundleObj,
		"wrote", wrote,
		"removed", stringSliceVal(st.Removed),
		"next", st.Next,
		"duration_ms", st.DurationMs,
	)
}

func unfinishedObj(u *UnfinishedStep) *jq.Obj {
	return jq.NewObj(
		"step", u.Step,
		"verb", u.Verb,
		"channel", u.Channel,
		"pid", u.PID,
		"started_at", u.StartedAt,
		"argv", stringSliceVal(u.Argv),
		"version", u.Version,
	)
}

func chainBreakObj(cb *ChainBreak) *jq.Obj {
	return jq.NewObj(
		"step", cb.Step,
		"before", cb.Before,
		"previous_after", cb.PreviousAfter,
	)
}

func bundleDigestsObj(b BundleDigests) *jq.Obj {
	pairs := []any{"before", ptrStringVal(b.Before), "after", b.After}
	if b.Unobserved {
		pairs = append(pairs, "unobserved", true)
	}
	return jq.NewObj(pairs...)
}

func ptrStringVal(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func stringSliceVal(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// FormatHuman renders the frozen human view of a folded journal summary.
func FormatHuman(s *Summary) []string {
	if len(s.Steps) == 0 && len(s.Unfinished) == 0 && s.Truncation.Dropped == 0 && s.UnreadableLines == 0 {
		return []string{fmt.Sprintf("journal: %s (no calls recorded)", s.Bundle)}
	}
	lines := []string{fmt.Sprintf(
		"journal: %s (%d steps, %d unfinished, %d chain breaks)",
		s.Bundle, len(s.Steps), len(s.Unfinished), len(s.ChainBreaks),
	)}
	for i := range s.Steps {
		lines = append(lines, formatStepLines(i+1, &s.Steps[i])...)
	}
	lines = append(lines, formatUnfinishedLines(s.Unfinished)...)
	lines = append(lines, formatChainBreakLines(s.ChainBreaks)...)
	lines = append(lines, formatMetaLines(s)...)
	return lines
}

func formatStepLines(index int, st *Step) []string {
	detail := stepDetail(st)
	row := fmt.Sprintf("  %d  %s  %-11s  exit %-4d  %s",
		index, clockOf(st.StartedAt), st.Verb, st.Exit, detail)
	lines := []string{strings.TrimRight(row, " ")}
	if st.Next != "" {
		lines = append(lines, "      next: "+st.Next)
	}
	return lines
}

func stepDetail(st *Step) string {
	var parts []string
	if st.Refusal != nil && *st.Refusal != "" {
		parts = append(parts, "refusal "+*st.Refusal)
	}
	parts = append(parts, stepFileDetail(st)...)
	if st.Warnings > 0 {
		parts = append(parts, fmt.Sprintf("(%d %s)", st.Warnings, plural(st.Warnings, "warning", "warnings")))
	}
	if st.Bundle.Unobserved {
		parts = append(parts, "file diff unknown (content sweep failed)")
	}
	return strings.Join(parts, " ")
}

// stepFileDetail renders the wrote and removed clauses of one folded step.
func stepFileDetail(st *Step) []string {
	var parts []string
	if len(st.Wrote) > 0 {
		paths := make([]string, 0, len(st.Wrote))
		for i := range st.Wrote {
			paths = append(paths, st.Wrote[i].Path)
		}
		parts = append(parts, "wrote "+strings.Join(paths, ", "))
	}
	if len(st.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(st.Removed, ", "))
	}
	return parts
}

func formatUnfinishedLines(unfinished []UnfinishedStep) []string {
	if len(unfinished) == 0 {
		return nil
	}
	lines := make([]string, 0, len(unfinished)+1)
	lines = append(lines, "unfinished:")
	for i := range unfinished {
		u := &unfinished[i]
		lines = append(lines, fmt.Sprintf("  %s  %s  pid %d  (no end recorded)",
			clockOf(u.StartedAt), u.Verb, u.PID))
	}
	return lines
}

func formatChainBreakLines(breaks []ChainBreak) []string {
	if len(breaks) == 0 {
		return nil
	}
	lines := make([]string, 0, len(breaks)+1)
	lines = append(lines, "chain breaks:")
	for i := range breaks {
		cb := &breaks[i]
		lines = append(lines, fmt.Sprintf("  %s  before %s != previous after %s",
			clockOf(cb.Step), shortDigest(cb.Before), shortDigest(cb.PreviousAfter)))
	}
	return lines
}

func formatMetaLines(s *Summary) []string {
	var lines []string
	if s.Truncation.Dropped > 0 {
		lines = append(lines, fmt.Sprintf("truncated: %d earlier %s dropped",
			s.Truncation.Dropped, plural(s.Truncation.Dropped, "line", "lines")))
	}
	if s.UnreadableLines > 0 {
		lines = append(lines, fmt.Sprintf("unreadable: %d %s skipped",
			s.UnreadableLines, plural(s.UnreadableLines, "line", "lines")))
	}
	return lines
}

func clockOf(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("15:04:05Z")
}

func shortDigest(d string) string {
	const shortLen = 12
	if len(d) > shortLen {
		return d[:shortLen]
	}
	return d
}

func plural(n int, singular, multi string) string {
	if n == 1 {
		return singular
	}
	return multi
}
