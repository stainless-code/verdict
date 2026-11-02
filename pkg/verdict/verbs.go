package verdict

import (
	"context"
	"encoding/json"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// ReportRequest carries the report options. Output/Sarif name files whose
// bytes return in the response instead of being written.
type ReportRequest struct {
	Invocation
	Status, Kind, Output, Sarif, Check, Live string
	Cost, Completeness                       bool
}

// ReportResponse is the offline review report with render inputs and the
// requested file bytes.
type ReportResponse struct { //nolint:govet // field order is the public API surface
	Result
	Human      []string
	CheckLines []string
	GateFailed bool
	Output     *Artifact
	Sarif      *Artifact
}

// Report renders the offline review report for the bundle.
func (c *Client) Report(ctx context.Context, req *ReportRequest) (*ReportResponse, error) {
	resp, ref := reportcore.Run(ctx, &core.ReportRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Flags: core.ReportFlags{
			Status: req.Status, Kind: req.Kind, Output: req.Output, Sarif: req.Sarif,
			Check: req.Check, Live: req.Live, Cost: req.Cost, Completeness: req.Completeness,
		},
		Effective: core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &ReportResponse{
		Result:     resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
		Human:      resp.Human,
		CheckLines: resp.CheckLines,
		GateFailed: resp.GateFailed,
		Output:     artifactOf(resp.Output),
		Sarif:      artifactOf(resp.Sarif),
	}, nil
}

// NextRequest asks for the top actionable finding, optionally filtered.
type NextRequest struct {
	Invocation
	Status string
}

// NextResponse carries the top actionable finding for the bundle.
type NextResponse struct { //nolint:govet // field order is the public API surface
	Repo, PR, At, Status, Human string
	Warnings                    []string
	Item                        *Finding
	StoreUnreadable             bool
}

// Next returns the top actionable finding for the bundle.
func (c *Client) Next(ctx context.Context, req *NextRequest) (*NextResponse, error) {
	resp, ref := reportcore.RunNext(ctx, &core.NextRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Status:      req.Status,
		Effective:   core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	if resp.ReportRefusal != nil {
		return nil, refusalErr(resp.ReportRefusal)
	}
	return &NextResponse{
		Repo: resp.Repo, PR: resp.Pr, At: resp.At, Status: resp.Status,
		Warnings: resp.Warnings, Human: resp.Human,
		Item: findingOf(resp.Item), StoreUnreadable: resp.StoreUnreadable,
	}, nil
}

// ShowRequest asks for one full finding record by signature or prefix.
type ShowRequest struct {
	Invocation
	Sig string
}

// ShowResponse carries one full finding record in store key order.
type ShowResponse struct { //nolint:govet // field order is the public API surface
	Repo, PR, At string
	Warnings     []string
	Item         *Finding
}

// Show returns one full finding record by signature or unique prefix.
func (c *Client) Show(ctx context.Context, req *ShowRequest) (*ShowResponse, error) {
	resp, ref := reportcore.RunShow(ctx, &core.ShowRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Sig:         req.Sig,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &ShowResponse{
		Repo: resp.Repo, PR: resp.Pr, At: resp.At, Warnings: resp.Warnings, Item: findingOf(resp.Item),
	}, nil
}

// RecordRequest merges a local review.json into the findings store.
type RecordRequest struct {
	Invocation
}

// Record validates and merges a local review.json into the stores.
func (c *Client) Record(ctx context.Context, req *RecordRequest) (*Result, error) {
	resp, ref := recordcore.Run(ctx, &core.RecordRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Effective:   core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// PostRequest carries the post options: approval, review file, and guards.
type PostRequest struct {
	Invocation
	Approve, ReviewFile                 string
	RequestChanges, Bind, Force, DryRun bool
}

// Post submits the review payload; DryRun renders without submitting.
func (c *Client) Post(ctx context.Context, req *PostRequest) (*Result, error) {
	resp, ref := postcore.Run(ctx, &core.PostRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Flags: core.PostFlags{
			Approve: req.Approve, ReviewFile: req.ReviewFile, RequestChanges: req.RequestChanges,
			Bind: req.Bind, Force: req.Force, DryRun: req.DryRun,
		},
		Effective: core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// VerifyRequest runs the recorded verifyCmd under the watchdog.
type VerifyRequest struct {
	Invocation
	Timeout string   // raw --timeout value (seconds); bad values refuse
	Env     []string // ordered child environment snapshot
}

// VerifyResponse carries the verify outcome and the recorded proof.
type VerifyResponse struct { //nolint:govet // field order is the public API surface
	Result
	Exit     int
	PatchDir string
	TimedOut bool
	Tail     string
}

// Verify runs the recorded verifyCmd with the watchdog and records proof.
func (c *Client) Verify(ctx context.Context, req *VerifyRequest) (*VerifyResponse, error) {
	resp, ref := verifycore.Run(ctx, &core.VerifyRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		TimeoutRaw:  req.Timeout,
		Environ:     req.Env,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &VerifyResponse{
		Result:   resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
		Exit:     resp.Exit,
		PatchDir: resp.PatchDir,
		TimedOut: resp.TimedOut,
		Tail:     resp.Tail,
	}, nil
}

// FetchRequest carries the fetch options: base/provider overrides and flags.
type FetchRequest struct {
	Invocation
	BaseSHA, Provider                 string
	BudgetConsent, ShowPrompts, Chunk bool
}

// Fetch fetches a pull request into .review/<pr>.
func (c *Client) Fetch(ctx context.Context, req *FetchRequest) (*Result, error) {
	resp, err := fetchcore.Run(ctx, &core.FetchRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Flags: core.FetchFlags{
			BaseSHA: req.BaseSHA, Provider: req.Provider, BudgetConsent: req.BudgetConsent,
			ShowPrompts: req.ShowPrompts, ChunkFlag: req.Chunk,
			HasBase: req.BaseSHA != "", HasProvider: req.Provider != "",
		},
		Effective: core.Defaults(),
		Human:     c.streamWriter(true),
		Diag:      c.streamWriter(false),
	})
	if err != nil {
		return nil, convertErr(err)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// FetchLocalRequest carries the local-diff fetch options.
type FetchLocalRequest struct {
	Invocation
	Range, BaseRef, Stash                               string
	Paths                                               []string
	Staged, HasStash, BudgetConsent, ShowPrompts, Chunk bool
}

// FetchLocal fetches a local git diff into .review/local-<name>.
func (c *Client) FetchLocal(ctx context.Context, req *FetchLocalRequest) (*Result, error) {
	resp, err := fetchcore.RunLocal(ctx, &core.FetchLocalRequest{
		ReadRequest: c.read(req.Repo, req.PR),
		Flags: core.FetchLocalFlags{
			RangeSpec: req.Range, BaseRef: req.BaseRef, StashN: req.Stash, Paths: req.Paths,
			Staged: req.Staged, HasStash: req.HasStash, BudgetConsent: req.BudgetConsent,
			ShowPrompts: req.ShowPrompts, ChunkFlag: req.Chunk,
		},
		Effective: core.Defaults(),
		Human:     c.streamWriter(true),
		Diag:      c.streamWriter(false),
	})
	if err != nil {
		return nil, convertErr(err)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// ReadyRequest gates on merge readiness.
type ReadyRequest struct {
	Invocation
	Check bool
}

// ReadyResponse carries the merge-readiness advisory and gate outcome.
type ReadyResponse struct {
	Result
	Ready, GateFailed bool
}

// Ready reports merge readiness (checks, threads, draft/conflict).
func (c *Client) Ready(ctx context.Context, req *ReadyRequest) (*ReadyResponse, error) {
	resp, ref := readycore.Run(ctx, &readycore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		Check:       req.Check,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &ReadyResponse{
		Result:     resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
		Ready:      resp.Ready,
		GateFailed: resp.GateFailed,
	}, nil
}

// TrendRequest rolls up review outcomes across fetched bundles.
// PR is unused; trend scopes by repo.
type TrendRequest struct {
	Repo    string
	Overlap bool
}

// TrendResponse carries the outcome rollup and the human summary.
type TrendResponse struct {
	Repo, At, Next string
	Warnings       []string
	Human          []string
	Data           json.RawMessage
}

// Trend rolls up review outcomes across fetched bundles.
func (c *Client) Trend(ctx context.Context, req *TrendRequest) (*TrendResponse, error) {
	resp, ref := trendcore.Run(ctx, &trendcore.Request{
		ReadRequest: c.read(req.Repo, ""),
		Overlap:     req.Overlap,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &TrendResponse{
		Repo: resp.Repo, At: resp.At, Next: resp.Next,
		Warnings: resp.Warnings, Human: resp.Human, Data: wireJSON(resp.Data),
	}, nil
}

// CalibrateRequest carries the calibration options: offline probe or live
// ground-truth join.
type CalibrateRequest struct { //nolint:govet // field order is the public API surface
	Invocation
	Probe     bool
	Specs     []string
	JudgeFile string
}

// Calibrate compares stored findings against ground truth.
func (c *Client) Calibrate(ctx context.Context, req *CalibrateRequest) (*Result, error) {
	resp, ref := calibratecore.Run(ctx, &calibratecore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		Probe:       req.Probe,
		Specs:       req.Specs,
		JudgeFile:   req.JudgeFile,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// DoctorRequest carries no positionals: doctor preflights the harness.
type DoctorRequest struct{}

// DoctorResponse carries the preflight result.
type DoctorResponse struct {
	Outcome
	Healthy bool
}

// Doctor preflights the harness (auth, git, storage) and reports health.
func (c *Client) Doctor(ctx context.Context, _ *DoctorRequest) (*DoctorResponse, error) {
	resp, ref := doctorcore.Run(ctx, &doctorcore.Request{ReadRequest: c.readBare()})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &DoctorResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		Healthy: resp.Healthy,
	}, nil
}

// CheckTreeRequest gates on a clean git worktree.
type CheckTreeRequest struct {
	Invocation
	Expect string
}

// CheckTreeResponse carries the gate outcome and process exit code.
type CheckTreeResponse struct {
	Outcome
	Exit int
}

// CheckTree gates on a clean git worktree. A not-a-repo checkout returns a
// typed refusal, not a response.
func (c *Client) CheckTree(ctx context.Context, req *CheckTreeRequest) (*CheckTreeResponse, error) {
	resp, ref := checktreecore.Run(ctx, &checktreecore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		Expect:      req.Expect,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	if resp.Error != nil {
		return nil, &RefusalError{
			Code: resp.Error.Code, Message: resp.Error.Message,
			Hint: core.HintFor(resp.Error.Code), Warnings: resp.Warnings,
		}
	}
	return &CheckTreeResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		Exit:    resp.Exit,
	}, nil
}

// TriageRequest flips one finding's status (or previews the flip).
type TriageRequest struct {
	Invocation
	Sig, Status, Rationale string
	Force, DryRun          bool
}

// TriageResponse carries the flip summary and the human line.
type TriageResponse struct { //nolint:govet // field order is the public API surface
	Outcome
	Human string
}

// Triage flips one finding's status, or previews the flip with DryRun.
func (c *Client) Triage(ctx context.Context, req *TriageRequest) (*TriageResponse, error) {
	resp, ref := triagecore.Run(ctx, &triagecore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		Sig:         req.Sig,
		Status:      req.Status,
		Rationale:   req.Rationale,
		Force:       req.Force,
		DryRun:      req.DryRun,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &TriageResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		Human:   resp.Human,
	}, nil
}

// AdoptRequest copies findings from a local bundle into the target bundle.
type AdoptRequest struct {
	Invocation
	From string
}

// AdoptResponse carries the handoff summary and the human line.
type AdoptResponse struct { //nolint:govet // field order is the public API surface
	Outcome
	Human string
}

// Adopt copies findings from a local bundle into the target PR bundle.
func (c *Client) Adopt(ctx context.Context, req *AdoptRequest) (*AdoptResponse, error) {
	resp, ref := adoptcore.Run(ctx, &adoptcore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		From:        req.From,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &AdoptResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		Human:   resp.Human,
	}, nil
}

// FixRequest runs one bounded fix pass over an inline finding.
type FixRequest struct {
	Invocation
	Sig string
}

// Fix runs one bounded fix pass over an inline finding (or reports
// completion when nothing is actionable).
func (c *Client) Fix(ctx context.Context, req *FixRequest) (*Outcome, error) {
	resp, ref := fixcore.Run(ctx, &fixcore.Request{
		ReadRequest: c.read(req.Repo, req.PR),
		Sig:         req.Sig,
		Human:       c.streamWriter(true),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return outcomePtr(resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// LearnRequest reads or updates the local knowledge base. It takes no
// repo/PR positionals; the base lives under .review/.
type LearnRequest struct {
	Rule, Scope, ID, Provenance, Rigor, Reason, Retire, Restore string
	Status                                                      bool
}

// Learn reads or updates the local structured knowledge base.
func (c *Client) Learn(ctx context.Context, req *LearnRequest) (*Outcome, error) {
	resp, ref := learncore.Run(ctx, &learncore.Request{
		ReadRequest: c.readBare(),
		Rule:        req.Rule,
		Scope:       req.Scope,
		ID:          req.ID,
		Provenance:  req.Provenance,
		Rigor:       req.Rigor,
		Reason:      req.Reason,
		Retire:      req.Retire,
		Restore:     req.Restore,
		Status:      req.Status,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return outcomePtr(resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// InitRequest scaffolds .review control files from a style preset.
// Non-interactive only: pass Yes to confirm.
type InitRequest struct {
	Preset string
	Yes    bool
}

// InitResponse carries the scaffold plan and exit code.
type InitResponse struct {
	Outcome
	ExitCode int
}

// Init scaffolds .review control files from a style preset.
func (c *Client) Init(ctx context.Context, req *InitRequest) (*InitResponse, error) {
	resp, ref := initcore.Run(ctx, &initcore.Request{
		ReadRequest: c.readBare(),
		Preset:      req.Preset,
		Yes:         req.Yes,
		Human:       c.streamWriter(true),
		Diag:        c.streamWriter(false),
		ReadAnswer:  func() string { return "" },
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &InitResponse{
		Outcome:  outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		ExitCode: resp.ExitCode,
	}, nil
}
