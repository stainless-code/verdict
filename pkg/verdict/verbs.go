package verdict

import (
	"context"
	"encoding/json"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	handoffcore "github.com/stainless-code/verdict/internal/core/handoff"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	ingestcore "github.com/stainless-code/verdict/internal/core/ingest"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	riskcore "github.com/stainless-code/verdict/internal/core/risk"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// The two coverage postures Gate accepts, spelled here so the exported field
// documents its own published values. Unset means require.
const (
	coverageRequire = "require"
	coverageWarn    = "warn"
)

// ReportRequest carries the report options. Output/Sarif name files whose
// bytes return in the response instead of being written.
type ReportRequest struct {
	Invocation
	Status, Kind, Output, Sarif, Check, Live, MaxRisk string
	// Compare names a baseline bundle (a directory, or a PR/local slug in the
	// same state directory) to compare signature-matched findings against.
	Compare                                   string
	Cost, Completeness, IncludeWithheld, Risk bool
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := reportcore.Run(ctx, &reportcore.Request{
		ReadRequest: rr,
		Flags: reportcore.Flags{
			Status: req.Status, Kind: req.Kind, Output: req.Output, Sarif: req.Sarif,
			Check: req.Check, Live: req.Live, MaxRisk: req.MaxRisk,
			Compare: req.Compare,
			Cost:    req.Cost, Completeness: req.Completeness,
			IncludeWithheld: req.IncludeWithheld, Risk: req.Risk,
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

// ConfigRequest explains the stored effective policy of a fetched run or
// previews a proposed configuration file. Exactly one of Explain and
// Preview must be set; File names the proposed file for preview.
type ConfigRequest struct {
	Invocation
	File    string
	Explain bool
	Preview bool
}

// ConfigResponse carries the read-only policy surface: envelope fields
// plus the typed config data as JSON, byte-identical to the --json
// envelope's data object.
type ConfigResponse struct {
	Result
}

// Config explains the stored effective policy (--explain) or previews a
// proposed file with current settings and environment (--preview). It makes
// no live calls and no writes, and never approves. Resolving the repository
// root runs one `git rev-parse` probe through the environment, not through an
// injected Commander, so a caller that sandboxes execution must cover that
// probe too.
func (c *Client) Config(ctx context.Context, req *ConfigRequest) (*ConfigResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := config.Run(ctx, &core.ConfigRequest{
		ReadRequest: rr,
		Flags: core.ConfigFlags{
			Explain: req.Explain, Preview: req.Preview, File: req.File,
		},
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &ConfigResponse{
		Result: resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
	}, nil
}

// GateRequest composes the CI gate dimensions. An empty MaxSeverity skips the
// findings tier; Live stays empty for the auto posture. Coverage stays empty
// for the `require` posture, the CLI's default: an absent or partial stored
// diff-coverage receipt fails the gate, and "warn" reports it without failing.
type GateRequest struct {
	Invocation
	MaxSeverity string
	Live        string
	Coverage    string
	RequireHead bool
}

// GateDimension is one gate dimension's verdict and its own tally. Count
// leads so the struct scans no pointer bytes; the wire key order is fixed in
// the JSON builder, not here.
type GateDimension struct {
	Count              *int
	ID, Status, Detail string
}

// GateResponse carries the composed gate verdict. A failed gate is a verdict,
// not a refusal: the error stays nil and Passed is false.
type GateResponse struct {
	Result
	Dimensions []GateDimension
	Passed     bool
}

// Gate runs the composed CI gate: findings at-or-above MaxSeverity, a
// completed post for the fetched HEAD (RequireHead), the stored diff-coverage
// receipt for that HEAD (Coverage, default require), and live unresolved
// threads (Live, default auto). At least one dimension is required: Live and
// Coverage alone only set postures, so an empty request is a refusal. It never
// posts, merges, or writes a store.
func (c *Client) Gate(ctx context.Context, req *GateRequest) (*GateResponse, error) {
	if req.MaxSeverity == "" && !req.RequireHead {
		return nil, refusalErr(&core.RefusalError{
			Code: "bad-flags",
			Message: "gate needs at least one dimension: set MaxSeverity or RequireHead " +
				"(Live and Coverage alone only set postures)",
		})
	}
	coverage, err := gateCoveragePosture(req.Coverage)
	if err != nil {
		return nil, err
	}
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := reportcore.Run(ctx, &reportcore.Request{
		ReadRequest: rr,
		Flags: reportcore.Flags{
			Check: req.MaxSeverity, Completeness: req.RequireHead, Live: req.Live, Coverage: coverage,
		},
		Effective: core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	// Gate is always populated here: the guard above rejects the request that
	// sets no dimension, and every gating flag fills the block.
	gate := resp.Data.Gate
	dims := make([]GateDimension, 0, len(gate.Dimensions))
	for _, dim := range gate.Dimensions {
		dims = append(dims, GateDimension{ID: dim.ID, Status: dim.Status, Detail: dim.Detail, Count: dim.Count})
	}
	return &GateResponse{
		Result:     resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
		Passed:     gate.Passed,
		Dimensions: dims,
	}, nil
}

// gateCoveragePosture resolves the coverage posture the way the CLI parser
// does: unset reads require, and anything outside the published pair refuses
// instead of silently leaving the dimension out.
func gateCoveragePosture(posture string) (string, error) {
	switch posture {
	case "":
		return coverageRequire, nil
	case coverageRequire, coverageWarn:
		return posture, nil
	default:
		return "", refusalErr(&core.RefusalError{
			Code:    "bad-flags",
			Message: "unknown coverage posture '" + posture + "' for gate (want require|warn)",
		})
	}
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := reportcore.RunNext(ctx, &core.NextRequest{
		ReadRequest: rr,
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := reportcore.RunShow(ctx, &core.ShowRequest{
		ReadRequest: rr,
		Sig:         req.Sig,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &ShowResponse{
		Repo: resp.Repo, PR: resp.Pr, At: resp.At, Warnings: resp.Warnings, Item: findingOf(resp.Item),
	}, nil
}

// RecordRequest merges a local review.json into the findings store. DryRun
// reports what would be recorded and writes nothing. Strict promotes
// warning-only input to a refusal. CostNames asks for the cost-name repair,
// naming models[] entries from CostPanel in the order the panel ran (an
// empty panel with CostNames set reads the panel the bundle pinned).
type RecordRequest struct {
	Invocation
	CostPanel []string
	DryRun    bool
	Strict    bool
	CostNames bool
}

// Record validates and merges a local review.json into the stores.
func (c *Client) Record(ctx context.Context, req *RecordRequest) (*Result, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := recordcore.Run(ctx, &recordcore.Request{
		ReadRequest: rr,
		Effective:   core.Defaults(),
		DryRun:      req.DryRun,
		Strict:      req.Strict,
		Flags: recordcore.Flags{
			CostNames: req.CostNames, CostPanel: req.CostPanel,
		},
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// PostRequest carries the post options: approval, review file, and guards.
// AllowSecretValues publishes a review whose text carries a secret-shaped
// value; the pre-submit scan refuses post-secret-shaped without it.
type PostRequest struct {
	Invocation
	Approve, ReviewFile, MinSeverity    string
	Summary                             string
	RequestChanges, Bind, Force, DryRun bool
	AllowSecretValues                   bool
}

// Post submits the review payload; DryRun renders without submitting.
func (c *Client) Post(ctx context.Context, req *PostRequest) (*Result, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := postcore.Run(ctx, &postcore.Request{
		ReadRequest: rr,
		Flags: postcore.Flags{
			Approve: req.Approve, ReviewFile: req.ReviewFile, Summary: req.Summary,
			RequestChanges: req.RequestChanges, Bind: req.Bind, Force: req.Force, DryRun: req.DryRun,
			AllowSecretValues: req.AllowSecretValues,
			MinSeverity:       req.MinSeverity,
		},
		Effective: core.Defaults(),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// VerifyRequest runs the recorded verifyCmd under the watchdog, or one deep
// plan group's explicit command when Group and Command are both set.
type VerifyRequest struct {
	Invocation
	Timeout string
	Group   string
	Command string
	Env     []string
	// EnvAllowlist passes explicit parent environment variable names through
	// default-deny filtering and secret-name dropping.
	EnvAllowlist []string
	// Sandbox wraps the command in the host's enforced sandbox: bwrap on
	// Linux (no network, empty credential dirs, a writable reviewed root, a
	// private /tmp), sandbox-exec on macOS (no network, loopback included;
	// credentials stay visible). The call refuses with sandbox-unavailable
	// when the host cannot enforce it instead of running unwrapped.
	Sandbox bool
}

// VerifyResponse carries the verify outcome and the recorded proof.
type VerifyResponse struct { //nolint:govet // field order is the public API surface
	Result
	Exit     int
	PatchDir string
	TimedOut bool
	Tail     string
	Group    string // deep validation: the group the receipt certifies
	Head     string // deep validation: the head the command ran on
	Receipt  string // deep validation: the recorded receipt path
}

// Verify runs the recorded verifyCmd with the watchdog and records proof.
func (c *Client) Verify(ctx context.Context, req *VerifyRequest) (*VerifyResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := verifycore.Run(ctx, &verifycore.Request{
		ReadRequest:  rr,
		TimeoutRaw:   req.Timeout,
		Group:        req.Group,
		Command:      req.Command,
		Sandbox:      req.Sandbox,
		EnvAllowlist: req.EnvAllowlist,
		Environ:      req.Env,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	data := resp.Data
	return &VerifyResponse{
		Result:   resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, data),
		Exit:     resp.Exit,
		PatchDir: resp.PatchDir,
		TimedOut: resp.TimedOut,
		Tail:     resp.Tail,
		Group:    data.Group,
		Head:     data.Head,
		Receipt:  data.Receipt,
	}, nil
}

// FetchRequest carries the fetch options: base/provider overrides and flags.
// HeadControls reads the review controls from HEAD/the worktree instead of
// the PR base commit — the explicit opt-out that warns on every run.
type FetchRequest struct {
	Invocation
	BaseSHA, Provider                 string
	Context                           []string
	Issue                             []string
	Ignore                            []string
	NoDefaultExcludes                 bool
	BudgetConsent, ShowPrompts, Chunk bool
	HeadControls                      bool
	// Preview returns the reviewed and withheld file lists plus the resolved
	// review budget tier without writing a bundle or launching an agent.
	Preview bool
}

// Fetch fetches a pull request into .verdict/state/<pr>.
func (c *Client) Fetch(ctx context.Context, req *FetchRequest) (*Result, error) {
	rr, err := c.requestFetch(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, err := fetchcore.Run(ctx, &fetchcore.Request{
		ReadRequest: rr,
		Flags: fetchcore.Flags{
			Context: req.Context, Issue: req.Issue, Ignore: req.Ignore, NoDefaultExcludes: req.NoDefaultExcludes,
			BaseSHA: req.BaseSHA, Provider: req.Provider, BudgetConsent: req.BudgetConsent,
			ShowPrompts: req.ShowPrompts, ChunkFlag: req.Chunk, HeadControls: req.HeadControls,
			Preview: req.Preview,
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

// FetchLocalRequest carries the local-diff fetch options. Worktree builds
// the bundle from a temporary isolated checkout of the review target, and
// HeadControls reads the review controls from HEAD/the worktree instead of
// the base commit.
type FetchLocalRequest struct {
	Invocation
	Context                                             []string
	Ignore                                              []string
	Range, BaseRef, Stash                               string
	Paths                                               []string
	Staged, HasStash, BudgetConsent, ShowPrompts, Chunk bool
	Worktree, HeadControls                              bool
	NoDefaultExcludes                                   bool
	// Preview returns the reviewed and withheld file lists plus the resolved
	// review budget tier without writing a bundle or launching an agent.
	Preview bool
}

// FetchLocal fetches a local git diff into .verdict/state/local-<name>.
func (c *Client) FetchLocal(ctx context.Context, req *FetchLocalRequest) (*Result, error) {
	rr, err := c.requestFetch(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, err := fetchcore.RunLocal(ctx, &fetchcore.LocalRequest{
		ReadRequest: rr,
		Flags: fetchcore.LocalFlags{
			Context: req.Context, Ignore: req.Ignore, NoDefaultExcludes: req.NoDefaultExcludes,
			RangeSpec: req.Range, BaseRef: req.BaseRef, StashN: req.Stash, Paths: req.Paths,
			Staged: req.Staged, HasStash: req.HasStash, BudgetConsent: req.BudgetConsent,
			ShowPrompts: req.ShowPrompts, ChunkFlag: req.Chunk, Worktree: req.Worktree,
			HeadControls: req.HeadControls, Preview: req.Preview,
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

// ThreadsRequest carries the threads options: the provider override ("" reads
// GitHub, like the CLI) and the resolve mode's named threads and dry run.
type ThreadsRequest struct {
	Invocation
	Provider string
	Threads  []string
	Resolve  bool
	DryRun   bool
}

// ThreadsResolveSkip is one candidate a resolve run left open and why.
type ThreadsResolveSkip struct {
	ID     string
	Reason string
}

// ThreadsResponse carries the threads envelope plus the refreshed feed tally.
// Data is the `threads` profile byte-for-byte; the typed fields mirror it. A
// resolve run fills ResolvedAt/Resolved/Skipped instead of the feed tally.
type ThreadsResponse struct { //nolint:govet // field order is the public API surface
	Result
	Dir, File, Provider, HeadSHA, FetchedAt string
	Threads, Open, Outdated, Verdict        int
	Truncated                               bool
	ResolvedAt                              string
	Resolved                                []string
	Skipped                                 []ThreadsResolveSkip
}

// Threads re-reads one provider's review conversations and rewrites only
// .verdict/state/<pr>/threads.json, or with Resolve closes the selected
// threads and writes threads-resolve.json. Human progress streams through the
// Options.Progress observer.
func (c *Client) Threads(ctx context.Context, req *ThreadsRequest) (*ThreadsResponse, error) {
	rr, err := c.requestFetch(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := fetchcore.RunThreads(ctx, &fetchcore.ThreadsRequest{
		ReadRequest: rr,
		Provider:    req.Provider,
		Threads:     req.Threads,
		Resolve:     req.Resolve,
		DryRun:      req.DryRun,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	if resp.Resolve != nil {
		return resolveResponseOf(resp), nil
	}
	return &ThreadsResponse{
		Result: resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data),
		Dir:    resp.Data.Dir, File: resp.Data.File, Provider: resp.Data.Provider,
		HeadSHA: resp.Data.HeadSHA, FetchedAt: resp.Data.FetchedAt,
		Threads: resp.Data.Threads, Open: resp.Data.Open,
		Outdated: resp.Data.Outdated, Verdict: resp.Data.Verdict,
		Truncated: resp.Data.Truncated,
	}, nil
}

// resolveResponseOf mirrors one resolve run's data profile onto the public
// response.
func resolveResponseOf(resp *fetchcore.ThreadsResponse) *ThreadsResponse {
	data := resp.Resolve
	skipped := make([]ThreadsResolveSkip, 0, len(data.Skipped))
	for _, skip := range data.Skipped {
		skipped = append(skipped, ThreadsResolveSkip{ID: skip.ID, Reason: skip.Reason})
	}
	return &ThreadsResponse{
		Result: resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, data),
		Dir:    data.Dir, File: data.File, Provider: data.Provider,
		HeadSHA: data.HeadSHA, ResolvedAt: data.ResolvedAt,
		Resolved: append([]string{}, data.Resolved...), Skipped: skipped,
	}
}

// ReadyRequest gates on merge readiness.
type ReadyRequest struct {
	Invocation
	WaitFor string
	Timeout string
	Check   bool
}

// ReadyResponse carries the merge-readiness advisory and gate outcome.
type ReadyResponse struct {
	Result
	Ready, GateFailed bool
}

// Ready reports merge readiness (checks, threads, draft/conflict).
func (c *Client) Ready(ctx context.Context, req *ReadyRequest) (*ReadyResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := readycore.Run(ctx, &readycore.Request{
		ReadRequest: rr,
		Check:       req.Check,
		WaitFor:     req.WaitFor, Timeout: req.Timeout,
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
	Repo       string
	Rules      bool
	Resolution bool
	Overlap    bool
	Incidents  bool
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
	rr, err := c.request(ctx, req.Repo, "")
	if err != nil {
		return nil, err
	}
	resp, ref := trendcore.Run(ctx, &trendcore.Request{
		ReadRequest: rr,
		Overlap:     req.Overlap,
		Rules:       req.Rules,
		Resolution:  req.Resolution,
		Incidents:   req.Incidents,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &TrendResponse{
		Repo: resp.Repo, At: resp.At, Next: resp.Next,
		Warnings: resp.Warnings, Human: resp.Human, Data: wireJSON(resp.Data),
	}, nil
}

// RiskRequest ranks the bundles under .verdict/state into the triage queue.
// PR is unused; risk scopes by repo. Limit 0 reads the published default.
type RiskRequest struct {
	Repo, Status string
	Limit        int
}

// RiskResponse carries the ranked queue and the human summary.
type RiskResponse struct {
	Repo, At, Next string
	Warnings       []string
	Human          []string
	Data           json.RawMessage
}

// Risk ranks the local bundles into the advisory triage queue.
func (c *Client) Risk(ctx context.Context, req *RiskRequest) (*RiskResponse, error) {
	if req == nil {
		req = &RiskRequest{}
	}
	rr, err := c.request(ctx, req.Repo, "")
	if err != nil {
		return nil, err
	}
	resp, ref := riskcore.Run(ctx, &riskcore.QueueRequest{
		ReadRequest: rr, Status: req.Status, Limit: req.Limit,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &RiskResponse{
		Repo: resp.Repo, At: resp.At, Next: resp.Next,
		Warnings: resp.Warnings, Human: resp.Human, Data: wireJSON(resp.Data),
	}, nil
}

// HandoffRequest projects the open inline findings a fix run would take. PR
// names the bundle; MinSeverity is the severity floor (empty reads important)
// and Paths are the portable globs that scope the projection.
type HandoffRequest struct {
	Repo, Pr, MinSeverity string
	Paths                 []string
}

// HandoffResponse carries the projection and the markdown a caller can print.
type HandoffResponse struct {
	Repo, At, Next string
	Warnings       []string
	Human          []string
	Data           json.RawMessage
}

// Handoff hands one bundle's open findings to a human or another agent.
func (c *Client) Handoff(ctx context.Context, req *HandoffRequest) (*HandoffResponse, error) {
	if req == nil {
		req = &HandoffRequest{}
	}
	rr, err := c.request(ctx, req.Repo, req.Pr)
	if err != nil {
		return nil, err
	}
	resp, ref := handoffcore.Run(ctx, &handoffcore.Request{
		ReadRequest: rr,
		MinSeverity: req.MinSeverity,
		Paths:       req.Paths,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &HandoffResponse{
		Repo: resp.Repo, At: resp.At, Next: resp.Next,
		Warnings: resp.Warnings, Human: resp.Human, Data: wireJSON(resp.Data),
	}, nil
}

// IndexRequest queries the local code index. Query is any index subcommand —
// callers, status, tests-for, co-changed, owners, build, or prune; Symbol is
// the caller or tests-for symbol, Path the tests-for, co-changed, or owners
// path, At names a tree or ref, From overrides the checkout, Since,
// Commits, and MinCount bound the co-change window, and Limit bounds rows.
type IndexRequest struct {
	Query, Symbol, Path, From, At, Since string
	Limit, Commits, MinCount             int
}

// IndexResponse carries the index data profile and its human lines.
type IndexResponse struct {
	At       string
	Next     string
	Warnings []string
	Human    []string
	Data     json.RawMessage
}

// Index runs one index query. The MCP tool serves callers and status only;
// this method forwards every query, build and prune included.
func (c *Client) Index(ctx context.Context, req *IndexRequest) (*IndexResponse, error) {
	rr, err := c.requestBare(ctx)
	if err != nil {
		return nil, err
	}
	resp, ref := indexcore.Run(ctx, &indexcore.Request{
		ReadRequest: rr, Query: req.Query, Symbol: req.Symbol, Path: req.Path,
		From: req.From, At: req.At, Since: req.Since, Limit: req.Limit,
		Commits: req.Commits, MinCount: req.MinCount,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &IndexResponse{
		At: resp.At, Next: resp.Next, Warnings: resp.Warnings,
		Human: resp.Human, Data: wireJSON(resp.Data),
	}, nil
}

// CalibrateRequest carries the calibration options: offline probe or live
// ground-truth join.
type CalibrateRequest struct { //nolint:govet // field order is the public API surface
	Invocation
	Reactions      bool
	ReactionReason string
	Probe          bool
	Specs          []string
	JudgeFile      string
}

// Calibrate compares stored findings against ground truth.
func (c *Client) Calibrate(ctx context.Context, req *CalibrateRequest) (*Result, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := calibratecore.Run(ctx, &calibratecore.Request{
		ReadRequest: rr,
		Reactions:   req.Reactions, ReactionReason: req.ReactionReason,
		Probe:     req.Probe,
		Specs:     req.Specs,
		JudgeFile: req.JudgeFile,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return resultPtr(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// DoctorRequest names the host to preflight: the fetch enum, and an empty
// Provider keeps the GitHub posture.
type DoctorRequest struct {
	Provider string
}

// DoctorResponse carries the preflight result.
type DoctorResponse struct {
	Outcome
	Healthy bool
}

// Doctor preflights one code host's CLI and auth, git, and storage, and
// reports health.
func (c *Client) Doctor(ctx context.Context, req *DoctorRequest) (*DoctorResponse, error) {
	provider := ""
	if req != nil {
		provider = req.Provider
	}
	rr, err := c.requestBare(ctx)
	if err != nil {
		// Only cancellation aborts. Every other probe failure is exactly what
		// doctor exists to report (a missing git is a warning row, never a
		// refusal), so fall back to the cwd-relative posture and let the tool
		// rows speak. The root error is sealed at the service boundary, so
		// errors.Is on a sentinel can never match it here.
		if ctx.Err() != nil {
			return nil, err
		}
		// The probe cannot answer (an absent git fails it): doctor diagnoses
		// the harness from the cwd-relative posture instead of refusing, so
		// the tool rows still report.
		root := c.dir
		rr = core.ReadRequest{
			Runtime:  &core.Runtime{Env: c.env, Now: c.now, Progress: c.progress, Commander: c.commander, PackFS: skillfs.FS},
			Repo:     "",
			Pr:       "",
			Forces:   &core.GlobalForces{},
			Root:     &root,
			RepoRoot: ".",
		}
	}
	resp, ref := doctorcore.Run(ctx, &doctorcore.Request{ReadRequest: rr, Provider: provider})
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := checktreecore.Run(ctx, &checktreecore.Request{
		ReadRequest: rr,
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := triagecore.Run(ctx, &triagecore.Request{
		ReadRequest: rr,
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := adoptcore.Run(ctx, &adoptcore.Request{
		ReadRequest: rr,
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

// IngestRequest imports OASIS SARIF 2.1.0 results into the target bundle's
// findings store. From names the source format (SourceSARIF, or empty for the
// same thing); SARIF is a file path and SARIFData carries pre-read bytes
// instead. Every document is capped at 8 MiB and read offline.
type IngestRequest struct {
	Invocation
	From      string
	SARIF     string
	SARIFData []byte
}

// SourceSARIF is the one source format Ingest reads; From defaults to it.
const SourceSARIF = ingestcore.SourceSARIF

// IngestResponse carries the ingest summary and the human line.
type IngestResponse struct { //nolint:govet // field order is the public API surface
	Outcome
	Human string
}

// Ingest maps SARIF 2.1.0 results into the bundle's findings store, sharing the
// deduplication, hunk anchoring, and path-exclusion withholding paths. Each
// result is graded against the bundle's pinned diff: an anchored finding
// carries the verified diff line and pointed rigor, and a result the diff
// cannot anchor is published in the response data's skipped[] with its typed
// reason instead of being dropped or stored at line 0.
func (c *Client) Ingest(ctx context.Context, req *IngestRequest) (*IngestResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	creq := &ingestcore.Request{
		ReadRequest: rr,
		From:        req.From,
		SARIF:       req.SARIF,
	}
	if len(req.SARIFData) > 0 {
		creq.SARIF = ""
		creq.SARIFData = req.SARIFData
		creq.SARIFInline = true
	}
	resp, ref := ingestcore.Run(ctx, creq)
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &IngestResponse{
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
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := fixcore.Run(ctx, &fixcore.Request{
		ReadRequest: rr,
		Sig:         req.Sig,
		Human:       c.streamWriter(true),
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return outcomePtr(resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// LearnRequest reads or updates the local knowledge base. It takes no
// repo/PR positionals; the base lives under .verdict/. Incident, Sha, Paths,
// Note, URL, and At attach one revert or incident to a recorded bundle.
type LearnRequest struct {
	FromBundle, Pr, Accept, Reject                                    string
	Pack, Rule, Scope, ID, Provenance, Rigor, Reason, Retire, Restore string
	Incident, Sha, Note, URL, At                                      string
	Paths                                                             []string
	Propose, DryRun                                                   bool
	Status                                                            bool
}

// Learn reads or updates the local structured knowledge base.
func (c *Client) Learn(ctx context.Context, req *LearnRequest) (*Outcome, error) {
	rr, err := c.requestBare(ctx)
	if err != nil {
		return nil, err
	}
	rr.Pr = req.Pr
	rr.Repo = req.FromBundle
	resp, ref := learncore.Run(ctx, &learncore.Request{
		ReadRequest: rr,
		Pack:        req.Pack,
		FromBundle:  req.FromBundle, Accept: req.Accept, Reject: req.Reject, Propose: req.Propose, DryRun: req.DryRun,
		Rule:       req.Rule,
		Scope:      req.Scope,
		ID:         req.ID,
		Provenance: req.Provenance,
		Rigor:      req.Rigor,
		Reason:     req.Reason,
		Retire:     req.Retire,
		Restore:    req.Restore,
		Status:     req.Status,
		Incident:   req.Incident,
		Sha:        req.Sha,
		Paths:      req.Paths,
		Note:       req.Note,
		URL:        req.URL,
		At:         req.At,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return outcomePtr(resp.At, resp.Next, resp.Warnings, resp.Data), nil
}

// InitRequest scaffolds .verdict control files from a style preset, and with CI
// set, the every-PR workflow too. Non-interactive only: pass Yes to confirm.
// The fields mirror the CLI flags of the same name, so a facade call writes the
// bytes the explicit `verdict init` invocation writes.
type InitRequest struct {
	Preset string
	// CI names the host to write a workflow for: github, forgejo, or gitlab.
	CI string
	// Agent names the shipped agent the workflow launches; empty means the
	// default claude on a CI run.
	Agent string
	// KeySecret overrides the repository secret holding the agent's model key.
	KeySecret string
	// CommentTrigger adds GitHub's opt-in /verdict pull-request comment re-run.
	CommentTrigger bool
	// Print renders the workflow without writing it.
	Print bool
	Yes   bool
}

// InitResponse carries the scaffold plan and exit code.
type InitResponse struct {
	Outcome
	ExitCode int
}

// PackRequest carries the pack options: List reads the inventory, and Check
// resolves one repository-relative path through the same layers a review uses.
type PackRequest struct {
	Invocation
	Check string
	List  bool
}

// PackResponse is the positionals-free pack envelope: the list rows, or one
// path's resolution.
type PackResponse struct {
	Outcome
}

// Pack lists the built-in, org, and repo lens packs with their layer,
// source, hash, and declared ids, or resolves the packs, lenses, and rules
// one path applies with Check set. Neither mode makes live calls or writes
// anything.
func (c *Client) Pack(ctx context.Context, req *PackRequest) (*PackResponse, error) {
	rr, err := c.requestBare(ctx)
	if err != nil {
		return nil, err
	}
	settings := config.SettingsPath(c.env["HOME"], c.env["XDG_CONFIG_HOME"])
	if req.Check != "" {
		return checkPack(ctx, rr, settings, req.Check)
	}
	resp, ref := packcore.RunList(ctx, &packcore.Request{
		ReadRequest:  rr,
		SettingsPath: settings,
		List:         req.List,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &PackResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
	}, nil
}

// checkPack resolves one path, reading the built-in pack composition from the
// trusted base configuration the same way fetch does. It is a free function on
// purpose: the facade's verb surface is its *Client methods, and `pack check`
// is a subcommand of Pack, not a verb of its own.
func checkPack(
	ctx context.Context, rr core.ReadRequest, settings, path string,
) (*PackResponse, error) {
	toggles, source, ref := packcore.BaseToggles(ctx, rr.Commander, rr.RepoRootPath())
	if ref != nil {
		return nil, refusalErr(ref)
	}
	resp, ref := packcore.RunCheck(ctx, &packcore.CheckRequest{
		ReadRequest: rr, SettingsPath: settings, Toggles: toggles, ConfigSource: source,
		Path: path,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &PackResponse{
		Outcome: outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
	}, nil
}

// ListPacks is the read-only pack list entry in the facade: the same result
// as Pack with List set, named for the command it serves.
func ListPacks(ctx context.Context, c *Client) (*PackResponse, error) {
	return c.Pack(ctx, &PackRequest{List: true})
}

// CheckPack resolves one repository-relative path: the packs in precedence
// order, the lenses offered for it, and the rules whose globs match it.
func CheckPack(ctx context.Context, c *Client, path string) (*PackResponse, error) {
	return c.Pack(ctx, &PackRequest{Check: path})
}

// Init scaffolds .verdict control files from a style preset.
func (c *Client) Init(ctx context.Context, req *InitRequest) (*InitResponse, error) {
	rr, err := c.requestBare(ctx)
	if err != nil {
		return nil, err
	}
	resp, ref := initcore.Run(ctx, &initcore.Request{
		ReadRequest:    rr,
		Preset:         req.Preset,
		CI:             req.CI,
		Agent:          req.Agent,
		KeySecret:      req.KeySecret,
		CommentTrigger: req.CommentTrigger,
		Print:          req.Print,
		Yes:            req.Yes,
		Human:          c.streamWriter(true),
		Diag:           c.streamWriter(false),
		ReadAnswer:     func() string { return "" },
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &InitResponse{
		Outcome:  outcomeOf(resp.At, resp.Next, resp.Warnings, resp.Data),
		ExitCode: resp.ExitCode,
	}, nil
}

// PRWaitRequest requests a bounded foreground PR wait.
type PRWaitRequest struct {
	Invocation
	Until    string
	Timeout  string
	Interval string
	Provider string
	Once     bool
}

// PRWaitResponse carries the final observation and typed outcome.
type PRWaitResponse struct{ Result }

// PRWait waits for a PR condition; timeout remains successful response data.
func (c *Client) PRWait(ctx context.Context, req *PRWaitRequest) (*PRWaitResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := readycore.Wait(ctx, &readycore.WaitRequest{
		ReadRequest: rr, Until: req.Until, Timeout: req.Timeout,
		Interval: req.Interval, Provider: req.Provider, Once: req.Once,
	})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &PRWaitResponse{Result: resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data)}, nil
}

// JournalRequest reads one bundle's append-only invocation journal.
type JournalRequest struct {
	Invocation
}

// JournalResponse carries the folded journal summary and human lines.
type JournalResponse struct {
	Result
	Human []string
}

// Journal reads a bundle's external invocation journal without taking a lock
// or writing any files.
func (c *Client) Journal(ctx context.Context, req *JournalRequest) (*JournalResponse, error) {
	rr, err := c.request(ctx, req.Repo, req.PR)
	if err != nil {
		return nil, err
	}
	resp, ref := journalcore.Run(ctx, &journalcore.Request{ReadRequest: rr})
	if ref != nil {
		return nil, refusalErr(ref)
	}
	return &JournalResponse{
		Result: resultOf(resp.Repo, resp.Pr, resp.At, resp.Next, resp.Warnings, resp.Data.Wire()),
		Human:  resp.Human,
	}, nil
}
