package journal

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/stainless-code/verdict/internal/argvshape"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Invocation describes one verb execution entering either front door (CLI or
// MCP). Zero-value callbacks are safe no-ops.
type Invocation struct {
	Now      func() time.Time
	Warn     func(msg string)
	Refusal  func() string
	Warnings func() int
	Next     func() string
	Verb     string
	Channel  string
	RepoRoot string
	Pr       string
	Version  string
	Argv     []string
	Environ  []string
}

// BundleFor resolves the bundle directory for (repoRoot, pr) using
// [readcommon.ResolveBundleDir], returning "" when pr is empty or unresolvable.
func BundleFor(repoRoot, pr string) string {
	if pr == "" {
		return ""
	}
	dir, ref := readcommon.ResolveBundleDir(repoRoot, pr)
	if ref != nil {
		return ""
	}
	return dir
}

// Guard wraps one verb invocation with start/end bundle-journal appends and
// pre/post content-digest sweeps. Non-journaling verbs invoke fn directly with
// no filesystem I/O. Any journal append error emits a diagnostic warning and
// never alters fn's return code.
func Guard(inv *Invocation, fn func() int) int {
	verb := CanonicalVerb(inv.Verb)
	if !ShouldJournal(verb) {
		return fn()
	}
	st := beginGuard(inv, verb)
	code := fn()
	finishGuard(inv, st, code)
	return code
}

type guardState struct {
	now          func() time.Time
	preDir       string
	start        StartLine
	preSnap      Snapshot
	startWritten bool
}

func beginGuard(inv *Invocation, verb string) *guardState {
	nowFn := inv.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	startedAt := nowFn().UTC()
	step := startedAt.Format(time.RFC3339Nano)
	ver := inv.Version
	if ver == "" {
		ver = core.VersionUnknown
	}
	redactedArgv, _ := argvshape.Record(inv.Argv, "", inv.Environ)
	preDir := BundleFor(inv.RepoRoot, inv.Pr)
	preSnap := sweepBestEffort(preDir, inv.Warn)
	st := &guardState{
		now:    nowFn,
		preDir: preDir,
		start: StartLine{
			SchemaVersion: SchemaVersion,
			Phase:         PhaseStart,
			Step:          step,
			Verb:          verb,
			Channel:       inv.Channel,
			PID:           os.Getpid(),
			Argv:          redactedArgv,
			Version:       ver,
			StartedAt:     step,
		},
		preSnap: preSnap,
	}
	if preSnap.Exists {
		st.startWritten = true
		if err := AppendStart(preDir, &st.start, startedAt); err != nil {
			emitWarn(inv.Warn, preDir, err)
		}
	}
	return st
}

func finishGuard(inv *Invocation, st *guardState, code int) {
	postDir := BundleFor(inv.RepoRoot, inv.Pr)
	postSnap := sweepBestEffort(postDir, inv.Warn)
	if !postSnap.Exists {
		return
	}
	preSnap := st.preSnap
	if !st.startWritten || postDir != st.preDir {
		preSnap = Snapshot{}
		if err := AppendStart(postDir, &st.start, st.now().UTC()); err != nil {
			emitWarn(inv.Warn, postDir, err)
		}
	}
	endedAt := st.now().UTC()
	wrote, removed := Diff(preSnap, postSnap)
	endLine := EndLine{
		SchemaVersion: SchemaVersion,
		Phase:         PhaseEnd,
		Step:          st.start.Step,
		Verb:          st.start.Verb,
		Channel:       st.start.Channel,
		PID:           st.start.PID,
		StartedAt:     st.start.StartedAt,
		EndedAt:       endedAt.Format(time.RFC3339Nano),
		Exit:          code,
		Refusal:       optionalString(inv.Refusal),
		Warnings:      callInt(inv.Warnings),
		Bundle:        bundleDigestsOf(preSnap, postSnap),
		Wrote:         wrote,
		Removed:       removed,
		Next:          callString(inv.Next),
	}
	if err := AppendEnd(postDir, &endLine, endedAt); err != nil {
		emitWarn(inv.Warn, postDir, err)
	}
}

func bundleDigestsOf(pre, post Snapshot) BundleDigests {
	var before *string
	if pre.Exists {
		d := pre.Digest
		before = &d
	}
	return BundleDigests{Before: before, After: post.Digest}
}

func emitWarn(warn func(string), dir string, err error) {
	if warn == nil {
		return
	}
	warn(WarningMessage(filepath.Join(dir, JournalFile), err))
}

func sweepBestEffort(dir string, warn func(string)) Snapshot {
	snap, err := Sweep(dir)
	if err != nil {
		emitWarn(warn, dir, err)
		return Snapshot{}
	}
	return snap
}

func optionalString(fn func() string) *string {
	if fn == nil {
		return nil
	}
	s := fn()
	if s == "" {
		return nil
	}
	return &s
}

func callString(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}

func callInt(fn func() int) int {
	if fn == nil {
		return 0
	}
	return fn()
}

// CountingProgress wraps a [core.Progress] observer and counts Warn calls in a
// race-safe counter so MCP invocations record accurate warning counts without
// scraping stderr.
type CountingProgress struct {
	Inner core.Progress
	count atomic.Int64
}

// Human forwards a rendered human output line to Inner.
func (p *CountingProgress) Human(line string) {
	if p.Inner != nil {
		p.Inner.Human(line)
	}
}

// Warn increments the warning counter and forwards msg to Inner.
func (p *CountingProgress) Warn(msg string) {
	p.count.Add(1)
	if p.Inner != nil {
		p.Inner.Warn(msg)
	}
}

// Diag forwards a raw diagnostic line to Inner.
func (p *CountingProgress) Diag(line string) {
	if p.Inner != nil {
		p.Inner.Diag(line)
	}
}

// Count returns the number of warnings observed through this progress wrapper.
func (p *CountingProgress) Count() int {
	return int(p.count.Load())
}
