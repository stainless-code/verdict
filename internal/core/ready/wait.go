package readycore

import (
	"context"
	"errors"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// WaitRequest describes a bounded, read-only foreground PR wait.
type WaitRequest struct {
	PollRuntime PollRuntime         `json:"-"`
	Observe     func(*WaitSnapshot) `json:"-"`
	core.ReadRequest
	Until    string `enum:"merged|closed|checks-green|checks-done|review-changed"`
	Timeout  string
	Interval string
	Provider string `enum:"github|gitlab|forgejo"`
	Once     bool
}

// WaitResponse carries the final observation and typed wait outcome.
type WaitResponse struct {
	Data     *WaitData
	Repo     string
	Pr       string
	At       string
	Next     string
	Warnings []string
}

// WaitData is the bounded wait outcome, including its last observation.
type WaitData struct {
	Snapshot     *WaitSnapshot `anyOf:"null"                                                 json:"snapshot"`
	Outcome      string        `enum:"condition-met|condition-unmet|wait-timeout"            json:"outcome"`
	Until        string        `enum:"merged|closed|checks-green|checks-done|review-changed" json:"until"`
	Observations int           `json:"observations"`
}

// Wire renders the published wait data profile.
func (d *WaitData) Wire() *jq.Obj {
	var snapshot any
	if d.Snapshot != nil {
		snapshot = d.Snapshot
	}
	return jq.NewObj("outcome", d.Outcome, "until", d.Until, "snapshot", snapshot, "observations", d.Observations)
}

// Wait runs a foreground poll, returning timeout as data rather than a refusal.
func Wait(ctx context.Context, req *WaitRequest) (*WaitResponse, *core.RefusalError) {
	resp, _, ref := waitSessionRun(ctx, req)
	return resp, ref
}

func waitSessionRun(ctx context.Context, req *WaitRequest) (*WaitResponse, *WaitData, *core.RefusalError) {
	timeout, interval, ref := WaitDurations(req.Timeout, req.Interval)
	if ref != nil {
		return nil, nil, ref
	}
	if !validUntil(req.Until) {
		return nil, nil, &core.RefusalError{Code: waitBadFlags, Message: "--until must be merged, closed, checks-green, " +
			"checks-done, or review-changed"}
	}
	if req.Runtime == nil {
		req.Runtime = &core.Runtime{}
	}
	provider, ref := waitProvider(req)
	if ref != nil {
		return nil, nil, ref
	}
	data := &WaitData{Until: req.Until}
	session := &waitSession{req: req, data: data, provider: provider}

	runtime := req.PollRuntime
	if runtime.Now == nil {
		runtime.Now = req.Now
	}
	completed, err := Poll(ctx, timeout, interval, runtime, session.observe)
	if err != nil {
		return nil, data, waitRefusal(ctx, err)
	}
	data.Outcome = session.outcome(completed)
	return &WaitResponse{
		Repo: req.Repo, Pr: req.Pr, At: req.TimeUTC(),
		Next: "inspect the wait outcome", Warnings: []string{}, Data: data,
	}, data, nil
}

// WaitDurations validates timeout and interval for all wait transports.
func WaitDurations(timeoutText, intervalText string) (time.Duration, time.Duration, *core.RefusalError) {
	if intervalText == "" {
		intervalText = "1s"
	}
	timeout, err := time.ParseDuration(timeoutText)
	if err != nil || timeout < time.Second || timeout > pollMaxTimeout {
		return 0, 0, badWaitBounds()
	}
	interval, err := time.ParseDuration(intervalText)
	if err != nil || interval < time.Second || interval > timeout {
		return 0, 0, badWaitBounds()
	}
	return timeout, interval, nil
}

func badWaitBounds() *core.RefusalError {
	return &core.RefusalError{Code: waitBadFlags, Message: "wait timeout must be 1s–30m; interval must be 1s–timeout"}
}

func waitProvider(req *WaitRequest) (string, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return "", ref
	}
	provider, present := readcommon.OpenBundle(dir).ProviderMarker()
	if !present {
		provider = providerGitHub
	}
	if req.Provider != "" {
		provider = req.Provider
	}
	return provider, nil
}

const (
	untilMerged  = "merged"
	untilClosed  = "closed"
	stateClosed  = "CLOSED"
	codeCanceled = "canceled"
)

func validUntil(until string) bool {
	switch until {
	case untilMerged, untilClosed, "checks-green", untilChecksDone, "review-changed":
		return true
	}
	return false
}

func waitMatches(until string, baseline, snapshot *WaitSnapshot) bool {
	switch until {
	case untilMerged:
		return snapshot.State == waitMerged
	case untilClosed:
		return snapshot.State == stateClosed || snapshot.State == waitMerged
	case "checks-green":
		return snapshot.ChecksKnown && snapshot.ChecksGreen
	case untilChecksDone:
		return snapshot.ChecksKnown && snapshot.ChecksDone
	case "review-changed":
		return baseline != nil && (snapshot.Reviews != baseline.Reviews || snapshot.Threads != baseline.Threads)
	}
	return false
}

func waitOutcome(matched, once bool) string {
	if matched {
		return "condition-met"
	}
	if once {
		return "condition-unmet"
	}
	return outcomeTimeout
}

func waitRefusal(ctx context.Context, err error) *core.RefusalError {
	if ctx.Err() != nil {
		return &core.RefusalError{Code: codeCanceled, Message: "PR wait canceled"}
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: waitReadCode, Message: "PR wait failed: " + err.Error()}
}

type waitSession struct {
	req      *WaitRequest
	data     *WaitData
	baseline *WaitSnapshot
	probe    WaitProbe
	provider string
	last     string
	matched  bool
}

func (s *waitSession) observe(ctx context.Context) (bool, error) {
	snapshot, ref := s.probe.Read(ctx, &s.req.ReadRequest, s.provider)
	if ref != nil {
		return false, ref
	}
	s.matched = waitMatches(s.req.Until, s.baseline, snapshot)
	if s.baseline == nil {
		s.baseline = snapshot
	}
	s.data.Snapshot = snapshot
	encoded := jq.Compact(snapshot)
	if encoded != s.last {
		s.changed(snapshot, encoded)
	}
	return s.matched || s.req.Once, nil
}

func (s *waitSession) changed(snapshot *WaitSnapshot, encoded string) {
	s.last = encoded
	s.data.Observations++
	if s.req.Observe != nil {
		s.req.Observe(snapshot)
	}
}

const (
	waitBadFlags    = "bad-flags"
	untilChecksDone = "checks-done"
	outcomeTimeout  = "wait-timeout"
)

func (s *waitSession) outcome(completed bool) string {
	return waitOutcome(completed && s.matched, completed && s.req.Once)
}
