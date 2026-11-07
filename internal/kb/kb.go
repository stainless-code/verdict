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

	"github.com/stainless-code/verdict/internal/jq"
)

// File names relative to the reviewed repository root.
const (
	// File is the tracked shared knowledge corpus.
	File = ".verdict/kb.jsonl"
	// Joins is the tracked append-only credit audit log beside it.
	Joins = ".verdict/kb-joins.jsonl"
)

// Policy carries the resolved retirement thresholds.
type Policy struct {
	MaxFPRate string
	MinJudged int
}

// Ctx carries the KB runtime state for one record run.
type Ctx struct {
	Warn func(string)
	// Root is the reviewed repository root every store write anchors to
	// ("" reads the working directory).
	Root string
	// ReadFile reads a decision store: the worktree by default, the controls
	// snapshot for record/post (write paths always use the worktree).
	ReadFile func(string) ([]byte, bool)
	Policy   Policy
	Staged   []map[string]any
	Credited []any
	Broken   bool
	// QuietShape suppresses malformed shape/id warnings after the review
	// checker already reported them as structured problems.
	QuietShape bool
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

// path anchors one store path under the reviewed repository root.
func (c *Ctx) path(rel string) string {
	if c.Root == "" {
		return rel
	}
	return filepath.Join(c.Root, rel)
}

// display renders one store path for operator-facing output: slash-form on
// every OS, the same portable form readcommon.DisplayPath gives bundle paths,
// so warnings and goldens never carry a native separator. Filesystem calls
// keep using [Ctx.path].
func (c *Ctx) display(rel string) string { return filepath.ToSlash(c.path(rel)) }

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,64}$`)

// PolicyFrom builds the retirement policy from the resolved typed
// `kb_policy` defaults: the resolver already folded flag, environment,
// repository, personal, and built-in layers into one value, so the store
// enforces exactly what the run resolved. Non-positive quorums and
// non-rate ceilings keep the built-in defaults instead of weakening
// retirement.
func PolicyFrom(minJudged int, maxFPRate string) Policy {
	pol := Policy{MinJudged: defaultMinJudged, MaxFPRate: defaultMaxFPRate}
	if minJudged > 0 {
		pol.MinJudged = minJudged
	}
	if isRate(maxFPRate) {
		pol.MaxFPRate = maxFPRate
	}
	return pol
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
		if !c.QuietShape {
			c.Warn("ignoring malformed kb (want an array of rule ids)")
		}
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
		n.warnShape("ignoring malformed kb id (want a rule id string)")
		return
	}
	if !idRe.MatchString(s) {
		n.warnShape(fmt.Sprintf("ignoring malformed kb id '%s' (want ^[a-z0-9][a-z0-9-]{0,64}$)", s))
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
func (n *normalizer) warnShape(msg string) {
	if !n.c.QuietShape {
		n.c.Warn(msg)
	}
}

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

func (c *Ctx) readJoins() ([]*jq.Obj, bool) {
	raw, err := jq.CappedFile(c.path(Joins), jq.MaxKBBytes)
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
)
