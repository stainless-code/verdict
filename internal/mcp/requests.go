package mcp

import (
	"io"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
)

// bindings carries the session state the transport injects into a typed
// request. Each wire function names the fields it fills, so a renamed request
// field stops the build instead of silently losing an injection.
type bindings struct { //nolint:govet // grouping documents the injection surface; alignment is irrelevant
	env       map[string]string
	now       func() time.Time
	progress  core.Progress
	root      string
	effective core.Effective
	warnings  []string
	environ   []string
	stderr    io.Writer
	commander core.Commander
}

// wireRead injects the shared runtime the CLI's flag parser produces,
// keeping the decoded repo/pr positionals intact.
func (b *bindings) wireRead(req *core.ReadRequest) {
	root := b.root
	req.Runtime = &core.Runtime{
		Env:       b.env,
		Now:       b.now,
		Progress:  b.progress,
		Commander: b.commander,
	}
	req.Forces = &core.GlobalForces{}
	req.Root = &root
}

// warningList copies the session's prefs warnings so a service cannot mutate
// the server's slice.
func (b *bindings) warningList() []string { return append([]string{}, b.warnings...) }

// environList copies the process environment for services that scrub it.
func (b *bindings) environList() []string { return append([]string{}, b.environ...) }

// decodeAdopt fills adopt's request.
func decodeAdopt(req *adoptcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("from", &req.From)
}

// wireAdopt injects the session runtime.
func wireAdopt(b *bindings, req *adoptcore.Request) {
	b.wireRead(&req.ReadRequest)
}

// decodeCalibrate fills calibrate's request.
func decodeCalibrate(req *calibratecore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.boolean("probe", &req.Probe)
	a.strings("specs", &req.Specs)
	a.str("judgeFile", &req.JudgeFile)
}

// wireCalibrate injects the session runtime and prefs warnings.
func wireCalibrate(b *bindings, req *calibratecore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeCheckTree fills check-tree's request.
func decodeCheckTree(req *checktreecore.Request, a *args) {
	a.str("expect", &req.Expect)
}

// wireCheckTree injects the session runtime and prefs warnings.
func wireCheckTree(b *bindings, req *checktreecore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeDoctor fills doctor's request: it exposes no arguments.
func decodeDoctor(_ *doctorcore.Request, _ *args) {}

// wireDoctor injects the session runtime and prefs warnings.
func wireDoctor(b *bindings, req *doctorcore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeFetch fills fetch's request.
func decodeFetch(req *core.FetchRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str("baseSHA", &req.Flags.BaseSHA)
		f.str("provider", &req.Flags.Provider)
		f.boolean("budgetConsent", &req.Flags.BudgetConsent)
		f.boolean("showPrompts", &req.Flags.ShowPrompts)
		f.boolean("chunkFlag", &req.Flags.ChunkFlag)
		f.boolean("headControls", &req.Flags.HeadControls)
	})
}

// wireFetch injects the session runtime, render config, streams, and the
// presence bits that distinguish an absent flag from an empty value.
func wireFetch(b *bindings, req *core.FetchRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
	req.Human = b.stderr
	req.Diag = b.stderr
	req.Flags.HasBase = req.Flags.BaseSHA != ""
	req.Flags.HasProvider = req.Flags.Provider != ""
}

// decodeFetchLocal fills fetch-local's request.
func decodeFetchLocal(req *core.FetchLocalRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str("rangeSpec", &req.Flags.RangeSpec)
		f.str("baseRef", &req.Flags.BaseRef)
		f.str("stashN", &req.Flags.StashN)
		f.strings("paths", &req.Flags.Paths)
		f.boolean("staged", &req.Flags.Staged)
		f.boolean("worktree", &req.Flags.Worktree)
		f.boolean("budgetConsent", &req.Flags.BudgetConsent)
		f.boolean("showPrompts", &req.Flags.ShowPrompts)
		f.boolean("chunkFlag", &req.Flags.ChunkFlag)
		f.boolean("headControls", &req.Flags.HeadControls)
	})
}

// wireFetchLocal injects the session runtime, render config, streams, and the
// presence bits that distinguish an absent flag from an empty value.
func wireFetchLocal(b *bindings, req *core.FetchLocalRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
	req.Human = b.stderr
	req.Diag = b.stderr
	req.Flags.HasStash = req.Flags.StashN != ""
}

// decodeFix fills fix's request.
func decodeFix(req *fixcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
}

// wireFix injects the session runtime and the human stream.
func wireFix(b *bindings, req *fixcore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Human = b.stderr
}

// decodeInit fills init's request.
func decodeInit(req *initcore.Request, a *args) {
	a.str("preset", &req.Preset)
	a.boolean("yes", &req.Yes)
}

// wireInit injects the session runtime and the human/diagnostic streams.
func wireInit(b *bindings, req *initcore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Human = b.stderr
	req.Diag = b.stderr
}

// decodeLearn fills learn's request.
func decodeLearn(req *learncore.Request, a *args) {
	a.str("rule", &req.Rule)
	a.str("scope", &req.Scope)
	a.str("id", &req.ID)
	a.str("provenance", &req.Provenance)
	a.str("rigor", &req.Rigor)
	a.str("reason", &req.Reason)
	a.str("retire", &req.Retire)
	a.str("restore", &req.Restore)
	a.boolean(argStatus, &req.Status)
}

// wireLearn injects the session runtime and JSON mode.
func wireLearn(b *bindings, req *learncore.Request) {
	b.wireRead(&req.ReadRequest)
	req.JSONMode = true
}

// decodeNext fills next's request.
func decodeNext(req *core.NextRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str(argStatus, &req.Status)
}

// wireNext injects the session runtime, render config, and prefs warnings.
func wireNext(b *bindings, req *core.NextRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.Warnings = b.warningList()
}

// decodePost fills post's request.
func decodePost(req *core.PostRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str("approve", &req.Flags.Approve)
		f.str("reviewFile", &req.Flags.ReviewFile)
		f.boolean("requestChanges", &req.Flags.RequestChanges)
		f.boolean("bind", &req.Flags.Bind)
		f.boolean("force", &req.Flags.Force)
		f.boolean("dryRun", &req.Flags.DryRun)
	})
}

// wirePost injects the session runtime, render config, JSON mode, and prefs
// warnings.
func wirePost(b *bindings, req *core.PostRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
}

// decodeReady fills ready's request.
func decodeReady(req *readycore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.boolean("check", &req.Check)
}

// wireReady injects the session runtime and prefs warnings.
func wireReady(b *bindings, req *readycore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeRecord fills record's request: its Flags struct exposes no fields.
func decodeRecord(req *core.RecordRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
}

// wireRecord injects the session runtime, render config, and prefs warnings.
func wireRecord(b *bindings, req *core.RecordRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.Warnings = b.warningList()
}

// decodeReport fills report's request.
func decodeReport(req *core.ReportRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str(argStatus, &req.Flags.Status)
		f.str("kind", &req.Flags.Kind)
		f.str("output", &req.Flags.Output)
		f.str("sarif", &req.Flags.Sarif)
		f.str("check", &req.Flags.Check)
		f.str("live", &req.Flags.Live)
		f.boolean("cost", &req.Flags.Cost)
		f.boolean("completeness", &req.Flags.Completeness)
	})
}

// wireReport injects the session runtime, render config, and prefs warnings.
func wireReport(b *bindings, req *core.ReportRequest) {
	b.wireRead(&req.ReadRequest)
	req.Effective = b.effective
	req.Warnings = b.warningList()
}

// decodeShow fills show's request.
func decodeShow(req *core.ShowRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
}

// wireShow injects the session runtime and prefs warnings.
func wireShow(b *bindings, req *core.ShowRequest) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeThreads fills threads' request.
func decodeThreads(req *fetchcore.ThreadsRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("provider", &req.Provider)
}

// wireThreads injects the session runtime and prefs warnings.
func wireThreads(b *bindings, req *fetchcore.ThreadsRequest) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeTrend fills trend's request.
func decodeTrend(req *trendcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.boolean("overlap", &req.Overlap)
}

// wireTrend injects the session runtime and prefs warnings.
func wireTrend(b *bindings, req *trendcore.Request) {
	b.wireRead(&req.ReadRequest)
	req.Warnings = b.warningList()
}

// decodeTriage fills triage's request.
func decodeTriage(req *triagecore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
	a.str(argStatus, &req.Status)
	a.str("rationale", &req.Rationale)
	a.boolean("force", &req.Force)
	a.boolean("dryRun", &req.DryRun)
}

// wireTriage injects the session runtime.
func wireTriage(b *bindings, req *triagecore.Request) {
	b.wireRead(&req.ReadRequest)
}

// decodeVerify fills verify's request.
func decodeVerify(req *core.VerifyRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("timeoutRaw", &req.TimeoutRaw)
}

// wireVerify injects the session runtime, scrubbed environment, JSON mode,
// and prefs warnings.
func wireVerify(b *bindings, req *core.VerifyRequest) {
	b.wireRead(&req.ReadRequest)
	req.Environ = b.environList()
	req.JSONMode = true
	req.Warnings = b.warningList()
}
