// Package kb owns the structured knowledge-base helpers the record path
// shares with triage: citation policy resolution, per-comment cite
// normalization (absent/null keeps the prior cite, explicit [] clears),
// verification/triage credit staging, the atomic commit, and threshold
// retirement.
package kb

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/statedir"
)

// File names relative to the working directory.
const (
	File       = ".review/kb.jsonl"
	Joins      = ".review/kb-joins.jsonl"
	PolicyFile = ".review/kb-policy"
)

// StorePath is the writable worktree KB: verdict/kb.jsonl in committed
// controls mode, <state>/controls/kb.jsonl in local mode. File stays the
// bundle snapshot key every record/verify gate uses.
func StorePath() string {
	if statedir.ControlsMode() == "committed" {
		return filepath.Join(statedir.Resolve().Base, "verdict", "kb.jsonl")
	}
	return filepath.Join(statedir.Resolve().ControlsDir(), "kb.jsonl")
}

// Policy carries the resolved retirement thresholds.
type Policy struct {
	MaxFPRate string
	MinJudged int
}

// Ctx carries the KB runtime state for one record run.
type Ctx struct {
	Warn func(string)
	// ReadFile reads a decision store: the worktree by default, the controls
	// snapshot for record/post (write paths always use the worktree).
	ReadFile func(string) ([]byte, bool)
	Policy   Policy
	Staged   []map[string]any
	Credited []any
	Broken   bool
}

// New returns a Ctx with default thresholds.
func New(warn func(string)) *Ctx {
	return &Ctx{
		Policy: Policy{MinJudged: defaultMinJudged, MaxFPRate: defaultMaxFPRate},
		Warn:   warn,
		ReadFile: func(path string) ([]byte, bool) {
			raw, err := jq.CappedFile(path, jq.MaxKBBytes)
			return raw, err == nil
		},
	}
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,64}$`)

// ResolvePolicy reads the policy: env wins, then the policy file,
// then defaults. Invalid values warn and fall back. When no store and no
// env knobs exist the defaults stand untouched.
func (c *Ctx) ResolvePolicy(env map[string]string, kbExists bool) {
	if !kbExists && env[envMinJudged] == "" && env[envMaxFPRate] == "" {
		return
	}
	c.resolveMinJudged(env)
	c.resolveMaxFPRate(env)
}

func (c *Ctx) resolveMinJudged(env map[string]string) {
	if raw, ok := env[envMinJudged]; ok && raw != "" {
		v := strings.TrimSpace(raw)
		if n, ok := positiveInt(v); ok {
			c.Policy.MinJudged = n
		} else {
			c.Warn(fmt.Sprintf("ignoring invalid REVIEW_KB_MIN_JUDGED '%s' (want a positive integer) — trying file, then default", raw))
		}
	}
	// The file is read only when the env knob was absent or invalid; the
	// `min_from_env` gate is folded into the outcome: an accepted env
	// value wins outright.
	if _, ok := env[envMinJudged]; !ok || !isPositiveInt(strings.TrimSpace(env[envMinJudged])) {
		c.resolveMinJudgedFile()
	}
}

func (c *Ctx) resolveMinJudgedFile() {
	raw := c.policyLine(PolicyFile, envMinJudged, "MIN_JUDGED")
	if raw == "" {
		return
	}
	if n, ok := positiveInt(raw); ok {
		c.Policy.MinJudged = n
	} else {
		c.Warn(fmt.Sprintf("ignoring invalid MIN_JUDGED '%s' in %s (want a positive integer) — default 5", raw, PolicyFile))
	}
}

func (c *Ctx) resolveMaxFPRate(env map[string]string) {
	if raw, ok := env[envMaxFPRate]; ok && raw != "" {
		v := strings.TrimSpace(raw)
		if isRate(v) {
			c.Policy.MaxFPRate = v
		} else {
			c.Warn(fmt.Sprintf("ignoring invalid REVIEW_KB_MAX_FP_RATE '%s' (want 0..1) — trying file, then default", raw))
		}
	}
	if _, ok := env[envMaxFPRate]; !ok || !isRate(strings.TrimSpace(env[envMaxFPRate])) {
		c.resolveMaxFPRateFile()
	}
}

func (c *Ctx) resolveMaxFPRateFile() {
	raw := c.policyLine(PolicyFile, envMaxFPRate, "MAX_FP_RATE")
	if raw == "" {
		return
	}
	if isRate(raw) {
		c.Policy.MaxFPRate = raw
	} else {
		c.Warn(fmt.Sprintf("ignoring invalid MAX_FP_RATE '%s' in %s (want 0..1) — default 0.5", raw, PolicyFile))
	}
}

func (c *Ctx) policyLine(path, k1, k2 string) string {
	raw, ok := c.ReadFile(path)
	if !ok {
		return ""
	}
	found := ""
	for ln := range strings.SplitSeq(string(raw), "\n") {
		key, val, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key == k1 || key == k2 {
			found = strings.TrimSpace(val)
		}
	}
	return found
}

// isPositiveInt mirrors `[[ "$raw" =~ ^[0-9]+$ ]] && (( 10#$raw > 0 ))`.
func isPositiveInt(s string) bool {
	_, ok := positiveInt(s)
	return ok
}

func positiveInt(s string) (int, bool) {
	if !jq.Digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// isRate accepts `^[0-9]*\.?[0-9]+$` within the 0..1 range.
func isRate(s string) bool {
	digits, ok := scanRate(s)
	if !ok || digits == 0 {
		return false
	}
	f, err := strconv.ParseFloat(s, floatBits)
	return err == nil && f >= 0 && f <= 1
}

func scanRate(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	dot := false
	digits := 0
	for i := range len(s) {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digits++
		case s[i] == '.' && !dot:
			dot = true
		default:
			return 0, false
		}
	}
	return digits, true
}

// NormalizeEntry returns the normalized kb id
// list for one comment entry. Absent or null kb reads nil (no re-cite — the
// merge keeps the prior cite); explicit [] reads as an empty list (uncite).
// Present arrays normalize per element with warn-and-drop. It is
// same-process by design: warns must land in the envelope.
func (c *Ctx) NormalizeEntry(entry *jq.Obj, kbExists bool) any {
	raw, has := entry.Get("kb")
	if !has || raw == nil {
		return nil
	}
	out := []any{}
	if c.Broken {
		return out
	}
	arr, ok := raw.([]any)
	if !ok {
		c.Warn("ignoring malformed kb (want an array of rule ids)")
		return out
	}
	norm := &normalizer{c: c, kbExists: kbExists, seen: map[string]bool{}}
	for _, item := range arr {
		norm.add(item)
	}
	return norm.out
}

type normalizer struct {
	c        *Ctx
	seen     map[string]bool
	out      []any
	capped   bool
	kbExists bool
}

func (n *normalizer) add(item any) {
	s, isStr := item.(string)
	if !isStr {
		n.c.Warn("ignoring malformed kb id (want a rule id string)")
		return
	}
	if !idRe.MatchString(s) {
		n.c.Warn(fmt.Sprintf("ignoring malformed kb id '%s' (want ^[a-z0-9][a-z0-9-]{0,64}$)", s))
		return
	}
	if !n.live(s) {
		return
	}
	if n.seen[s] {
		return
	}
	if len(n.out) >= kbListCap {
		if !n.capped {
			n.c.Warn(fmt.Sprintf("kb list truncated to %d ids (extras dropped)", kbListCap))
			n.capped = true
		}
		return
	}
	n.seen[s] = true
	n.out = append(n.out, s)
}

// live checks the cited rule against the store, warning on dead cites.
func (n *normalizer) live(s string) bool {
	if !n.kbExists {
		n.c.Warn(fmt.Sprintf("ignoring unknown kb rule '%s' (no %s yet)", s, File))
		return false
	}
	st := "UNREADABLE"
	if rows, ok := n.c.readRows(); ok {
		if row := FindRow(rows, s); row != nil {
			v, _ := row.Get("status")
			st = jq.Str(v)
		} else {
			st = "MISSING"
		}
	}
	switch st {
	case statusActive:
		return true
	case "retired":
		n.c.Warn(fmt.Sprintf("ignoring retired kb rule '%s' (restore it before citing)", s))
	case "MISSING":
		n.c.Warn(fmt.Sprintf("ignoring unknown kb rule '%s'", s))
	default:
		n.c.Warn(fmt.Sprintf("kb store unreadable — ignoring cited rule '%s'", s))
	}
	return false
}

func (c *Ctx) readRows() ([]*jq.Obj, bool) {
	raw, ok := c.ReadFile(File)
	if !ok {
		return nil, false
	}
	rows, err := ParseRows(raw)
	if err != nil {
		return nil, false
	}
	return rows, true
}

func readJoins() ([]*jq.Obj, bool) {
	raw, err := jq.CappedFile(Joins, jq.MaxKBBytes)
	if err != nil {
		return nil, true
	}
	rows, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		return nil, false
	}
	return rows, true
}

const (
	defaultMinJudged = 5
	defaultMaxFPRate = "0.5"
	floatBits        = 64
	kbListCap        = 5
	envMinJudged     = "REVIEW_KB_MIN_JUDGED"
	envMaxFPRate     = "REVIEW_KB_MAX_FP_RATE"
)
