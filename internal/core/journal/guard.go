package journal

import (
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stainless-code/verdict/internal/argvshape"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	verbFetchLocal = "fetch-local"
	localPrefix    = "local-"
)

// Invocation describes one verb execution entering either front door (CLI or
// MCP). Zero-value callbacks are safe no-ops.
type Invocation struct {
	Now      func() time.Time
	Warn     func(msg string)
	Refusal  func() string
	Warnings func() int
	Next     func() string
	Env      map[string]string
	Verb     string
	Channel  string
	RepoRoot string
	Pr       string
	Version  string
	Argv     []string
	Environ  []string
}

// BundleTargeter is implemented by [*core.ReadRequest] and promoted by every
// request type that embeds it.
type BundleTargeter interface {
	BundleTarget() (repo, pr, repoRoot string)
}

// TargetFromRequest extracts (repoRoot, pr) from a decoded request that embeds
// [core.ReadRequest], returning empty strings otherwise.
func TargetFromRequest(request any) (repoRoot, pr string) {
	t, ok := request.(BundleTargeter)
	if !ok {
		return "", ""
	}
	_, pr, repoRoot = t.BundleTarget()
	return repoRoot, pr
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

func bundleForVerb(repoRoot, verb, pr string) string {
	if pr == "" {
		return ""
	}
	if verb == verbFetchLocal && !strings.HasPrefix(pr, localPrefix) {
		pr = localPrefix + pr
	}
	return BundleFor(repoRoot, pr)
}

func journalPathFor(repoRoot, bundleDir string, env map[string]string) (path, absRepo string, ok bool) {
	if bundleDir == "" {
		return "", "", false
	}
	return Path(repoRoot, readcommon.BundleName(bundleDir), env)
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
	journalPath  string
	start        StartLine
	preSnap      Snapshot
	startWritten bool
}

func beginGuard(inv *Invocation, verb string) *guardState {
	nowFn := resolveNow(inv.Now)
	startedAt := nowFn().UTC()
	step := startedAt.Format(time.RFC3339Nano)
	redactedArgv, _ := argvshape.Record(inv.Argv, "", inv.Environ)
	preDir := bundleForVerb(inv.RepoRoot, verb, inv.Pr)
	journalPath, absRepo, hasPath := journalPathFor(inv.RepoRoot, preDir, inv.Env)
	preSnap := sweepBestEffort(preDir, journalPath, inv.Warn)
	st := &guardState{
		now:         nowFn,
		journalPath: journalPath,
		start: StartLine{
			SchemaVersion: SchemaVersion,
			Phase:         PhaseStart,
			Repo:          absRepo,
			Step:          step,
			Verb:          verb,
			Channel:       inv.Channel,
			PID:           os.Getpid(),
			Argv:          redactedArgv,
			Version:       resolveVersion(inv.Version),
			StartedAt:     step,
		},
		preSnap: preSnap,
	}
	if hasPath {
		st.startWritten = true
		if err := AppendStart(journalPath, &st.start, startedAt); err != nil {
			emitWarn(inv.Warn, journalPath, err)
		}
	}
	return st
}

func finishGuard(inv *Invocation, st *guardState, code int) {
	postDir := bundleForVerb(inv.RepoRoot, st.start.Verb, inv.Pr)
	postPath, absRepo, hasPath := journalPathFor(inv.RepoRoot, postDir, inv.Env)
	if !hasPath {
		return
	}
	postSnap := sweepBestEffort(postDir, postPath, inv.Warn)
	preSnap := st.preSnap
	if !st.startWritten || postPath != st.journalPath {
		preSnap = Snapshot{}
		st.start.Repo = absRepo
		if err := AppendStart(postPath, &st.start, st.now().UTC()); err != nil {
			emitWarn(inv.Warn, postPath, err)
		}
	}
	endedAt := st.now().UTC()
	wrote, removed := Diff(preSnap, postSnap)
	endLine := EndLine{
		SchemaVersion: SchemaVersion,
		Phase:         PhaseEnd,
		Repo:          absRepo,
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
	if err := AppendEnd(postPath, &endLine, endedAt); err != nil {
		emitWarn(inv.Warn, postPath, err)
	}
}

func resolveNow(fn func() time.Time) func() time.Time {
	if fn != nil {
		return fn
	}
	return time.Now
}

func resolveVersion(ver string) string {
	if ver != "" {
		return ver
	}
	return core.VersionUnknown
}

func bundleDigestsOf(pre, post Snapshot) BundleDigests {
	var before *string
	if pre.Exists {
		d := pre.Digest
		before = &d
	}
	return BundleDigests{Before: before, After: post.Digest}
}

func emitWarn(warn func(string), path string, err error) {
	if warn == nil {
		return
	}
	warn(WarningMessage(path, err))
}

func sweepBestEffort(dir, warnPath string, warn func(string)) Snapshot {
	snap, err := Sweep(dir)
	if err != nil {
		emitWarn(warn, warnPath, err)
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
