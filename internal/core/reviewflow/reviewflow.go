// Package reviewflow is the fetch → run → record pipeline the composed verbs
// share: `verdict demo` (one review in a disposable directory) and
// `verdict loop` (the self-review rounds) both call the shipped fetch, run,
// and record services through this one wiring, so a change to how a composed
// review is fetched, launched, or recorded lands in both at once.
//
// The package composes and maps only: it chooses no target, no agent, no
// directory, and no stop condition. The callers own those decisions and hand
// in the bundle each call reads.
package reviewflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/spend"
)

// codeFetchFailed is the fetch boundary's own code for an untyped failure.
const codeFetchFailed = "fetch-failed"

// Flow carries everything the composed services share across one invocation:
// the runtime (clock, environment, progress), the tool root, the render
// configuration, and the seams no service may invent for itself.
type Flow struct { //nolint:govet // field grouping documents the request surface
	// ReadRequest supplies the runtime (Env, Now, Progress), Forces, and the
	// tool Root; its Repo, Pr, RepoRoot, and Commander are set per call.
	core.ReadRequest
	SettingsPath string
	Effective    core.Effective
	// Commander runs the host and git reads (fetch, record); AgentCommander
	// runs the launcher's watchdog spawn. The two postures differ, so the
	// caller owns both.
	Commander      fetchcore.Commander
	AgentCommander core.Commander
	LookPath       func(string) (string, error)
	Environ        []string
	Human          io.Writer
	Diag           io.Writer
	JSONMode       bool
	// Agents are the operator-defined rows from the prefs file; nil launches
	// from the shipped table only.
	Agents []config.PersonalAgent
	// SpendPolicy is the operator's personal spend policy, as `run` reads it.
	SpendPolicy *spend.Policy
}

// Bundle names one review target: the directory every composed service
// anchors to, the positionals the store reads it by, and the checkout the
// agent works in.
type Bundle struct {
	// Dir is the repository root the bundle lives under.
	Dir  string
	Repo string
	Pr   string
	// Checkout is the agent's working directory and the tree a local bundle's
	// freshness check reads, when that is not Dir ("" reads Dir).
	Checkout string
}

// Launch is the one agent launch the run phase performs.
type Launch struct {
	Agent      string
	FindersRaw string
	TimeoutRaw string
	// RoundsRaw is run's --rounds; empty keeps run's default.
	RoundsRaw string
}

// RecordOptions selects the record posture.
type RecordOptions struct {
	DryRun          bool
	RequireCoverage bool
}

// Read builds the transport-neutral request the composed read services take:
// the flow's runtime with the host commander, the positionals, the tool root,
// and the bundle directory as the reviewed root.
func (f *Flow) Read(b *Bundle) core.ReadRequest {
	return f.read(b, f.Commander)
}

func (f *Flow) read(b *Bundle, cmdr core.Commander) core.ReadRequest {
	root := f.RootPath()
	rt := &core.Runtime{Env: f.Env, Now: f.Now, Progress: f.Progress, Commander: cmdr}
	return core.ReadRequest{
		Runtime: rt,
		Repo:    b.Repo, Pr: b.Pr, Forces: f.Forces,
		Root: &root, RepoRoot: b.Dir,
	}
}

// FetchPR fetches one pull request bundle.
func (f *Flow) FetchPR(
	ctx context.Context, b *Bundle, flags *fetchcore.Flags, warnings []string,
) (*fetchcore.Response, *core.RefusalError) {
	resp, err := fetchcore.Run(ctx, &fetchcore.Request{
		ReadRequest:  f.Read(b),
		Flags:        *flags,
		SettingsPath: f.SettingsPath,
		Effective:    f.Effective,
		JSONMode:     f.JSONMode,
		Warnings:     warnings,
		Human:        f.Human,
		Diag:         f.Diag,
	})
	if err != nil {
		return nil, FetchRefusal(err)
	}
	return resp, nil
}

// FetchLocal fetches one local bundle from a checkout.
func (f *Flow) FetchLocal(
	ctx context.Context, b *Bundle, flags *fetchcore.LocalFlags, warnings []string,
) (*fetchcore.Response, *core.RefusalError) {
	resp, err := fetchcore.RunLocal(ctx, &fetchcore.LocalRequest{
		ReadRequest:  f.Read(b),
		Flags:        *flags,
		SettingsPath: f.SettingsPath,
		Effective:    f.Effective,
		JSONMode:     f.JSONMode,
		Warnings:     warnings,
		Human:        f.Human,
		Diag:         f.Diag,
	})
	if err != nil {
		return nil, FetchRefusal(err)
	}
	return resp, nil
}

// Run launches the operator's agent against the bundle: the review phase.
func (f *Flow) Run(
	ctx context.Context, b *Bundle, l Launch, warnings []string,
) (*core.RunResponse, *core.RefusalError) {
	return runcore.WithAgents(ctx, f.RunRequest(b, l, warnings), f.Agents)
}

// RunRequest is the run service request one launch against the bundle takes,
// exposed so a caller can spawn a different directive under the same agent
// resolution, environment, and watchdog.
func (f *Flow) RunRequest(b *Bundle, l Launch, warnings []string) *core.RunRequest {
	return &core.RunRequest{
		ReadRequest: f.read(b, f.AgentCommander),
		Agent:       l.Agent,
		FindersRaw:  l.FindersRaw,
		TimeoutRaw:  l.TimeoutRaw,
		RoundsRaw:   l.RoundsRaw,
		Checkout:    b.Checkout,
		LookPath:    f.LookPath,
		Environ:     f.Environ,
		JSONMode:    f.JSONMode,
		Warnings:    warnings,
		SpendPolicy: f.SpendPolicy,
	}
}

// Record records the launched review and hands back the rendered body.
func (f *Flow) Record(
	ctx context.Context, b *Bundle, opts RecordOptions, warnings []string,
) (*recordcore.Response, *core.RefusalError) {
	return recordcore.Run(ctx, &recordcore.Request{
		ReadRequest:   f.Read(b),
		Effective:     f.Effective,
		Warnings:      warnings,
		RepoLayerRoot: b.Checkout,
		DryRun:        opts.DryRun,
		Flags:         recordcore.Flags{RequireCoverage: opts.RequireCoverage},
	})
}

// FetchRefusal maps a fetch failure: a typed refusal passes through unchanged,
// and anything else becomes fetch-failed.
func FetchRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("fetch failed: %v", err)}
}
