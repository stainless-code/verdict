package core

import "time"

// Progress observes human-facing lines emitted by a service. It is not an
// [io.Writer]: the adapter decides which stream receives which kind, and a
// nil Progress is a no-op (services stay callable without a transport).
type Progress interface {
	// Human receives rendered human output lines.
	Human(line string)
	// Warn receives one warning message (the adapter adds the `warn: ` prefix).
	Warn(msg string)
	// Diag receives a raw diagnostic line that is neither human output nor a
	// warning.
	Diag(line string)
}

// Runtime is the injected per-invocation state every service receives: the
// environment snapshot, the clock, and the progress observer. Core never
// reads the process environment or stdio directly.
type Runtime struct {
	Env      map[string]string
	Now      func() time.Time
	Progress Progress
}

// TimeUTC formats the injected clock like the shell's
// date -u +%Y-%m-%dT%H:%M:%SZ.
func (r Runtime) TimeUTC() string {
	return NowUTC(r.now())
}

// now returns the injected clock, defaulting to the wall clock.
func (r Runtime) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// ReviewModel reads the REVIEW_MODEL knob for attribution substitution.
func (r Runtime) ReviewModel() string { return r.Env["REVIEW_MODEL"] }

// Human forwards a human line to the observer when one is installed.
func (r Runtime) Human(line string) {
	if r.Progress != nil {
		r.Progress.Human(line)
	}
}

// Warn forwards a warning to the observer when one is installed.
func (r Runtime) Warn(msg string) {
	if r.Progress != nil {
		r.Progress.Warn(msg)
	}
}

// Diag forwards a raw diagnostic line to the observer when one is installed.
func (r Runtime) Diag(line string) {
	if r.Progress != nil {
		r.Progress.Diag(line)
	}
}

// ReadRequest is the transport-neutral invocation shared by the read verbs:
// the injected runtime plus the resolved positionals and global flag forces.
type ReadRequest struct { //nolint:govet // the transport contract keeps its field order; 56 bytes is cheap to copy
	*Runtime
	Repo   string
	Pr     string
	Forces *GlobalForces
	Root   *string
}

// ForcesOrEmpty returns the global forces, defaulting to an empty set so
// direct callers and tests need no CLI wiring.
func (q ReadRequest) ForcesOrEmpty() *GlobalForces {
	if q.Forces == nil {
		return &GlobalForces{}
	}
	return q.Forces
}

// ReviewModel reads the REVIEW_MODEL knob for attribution substitution.
func (q ReadRequest) ReviewModel() string {
	if q.Runtime == nil {
		return ""
	}
	return q.Runtime.ReviewModel()
}

// RootPath returns the checkout root ("" when unset).
func (q ReadRequest) RootPath() string {
	if q.Root == nil {
		return ""
	}
	return *q.Root
}

// TimeUTC formats the injected clock (zero time when no runtime is set).
func (q ReadRequest) TimeUTC() string {
	if q.Runtime == nil {
		return NowUTC(time.Time{})
	}
	return q.Runtime.TimeUTC()
}

// Warn forwards a warning when a runtime is installed.
func (q ReadRequest) Warn(msg string) {
	if q.Runtime != nil {
		q.Runtime.Warn(msg)
	}
}

// Diag forwards a diagnostic line when a runtime is installed.
func (q ReadRequest) Diag(line string) {
	if q.Runtime != nil {
		q.Runtime.Diag(line)
	}
}

// NowUTC formats t like the shell's date -u +%Y-%m-%dT%H:%M:%SZ.
func NowUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
