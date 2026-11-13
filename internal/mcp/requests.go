package mcp

import (
	"context"
	"io"
	"time"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

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

func (b *bindings) wireRead(ctx context.Context, req *core.ReadRequest) error {
	root := b.root
	req.Runtime = &core.Runtime{
		Env:       b.env,
		Now:       b.now,
		Progress:  b.progress,
		Commander: b.commander,
		PackFS:    skillfs.FS,
	}
	req.Forces = &core.GlobalForces{}
	req.Root = &root
	repoRoot, err := readcommon.RepoRoot(ctx)
	if err != nil {
		return jq.Sealed("resolve repository root", err)
	}
	req.RepoRoot = repoRoot
	return nil
}

func (b *bindings) warningList() []string { return append([]string{}, b.warnings...) }

func (b *bindings) environList() []string { return append([]string{}, b.environ...) }

func decodeAdopt(req *adoptcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("from", &req.From)
}

func wireAdopt(ctx context.Context, b *bindings, req *adoptcore.Request) error {
	return b.wireRead(ctx, &req.ReadRequest)
}

func decodeCalibrate(req *calibratecore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.boolean("probe", &req.Probe)
	a.strings("specs", &req.Specs)
	a.str("judgeFile", &req.JudgeFile)
}

func wireCalibrate(ctx context.Context, b *bindings, req *calibratecore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeCheckTree(req *checktreecore.Request, a *args) {
	a.str("expect", &req.Expect)
}

func wireCheckTree(ctx context.Context, b *bindings, req *checktreecore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeConfig(req *core.ConfigRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.boolean("explain", &req.Flags.Explain)
		f.boolean("preview", &req.Flags.Preview)
		f.str("file", &req.Flags.File)
	})
}

func wireConfig(ctx context.Context, b *bindings, req *core.ConfigRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeDoctor(req *doctorcore.Request, a *args) {
	a.str("provider", &req.Provider)
}

func wireDoctor(ctx context.Context, b *bindings, req *doctorcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeFetch(req *core.FetchRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str("baseSHA", &req.Flags.BaseSHA)
		f.str("provider", &req.Flags.Provider)
		f.str("index", &req.Flags.Index)
		f.boolean("budgetConsent", &req.Flags.BudgetConsent)
		f.boolean("showPrompts", &req.Flags.ShowPrompts)
		f.boolean("chunkFlag", &req.Flags.ChunkFlag)
		f.boolean("headControls", &req.Flags.HeadControls)
	})
}

func wireFetch(ctx context.Context, b *bindings, req *core.FetchRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
	req.Human = b.stderr
	req.Diag = b.stderr
	core.DerivePresences(&req.Flags)
	return nil
}

func decodeFetchLocal(req *core.FetchLocalRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str("rangeSpec", &req.Flags.RangeSpec)
		f.str("baseRef", &req.Flags.BaseRef)
		f.str("stashN", &req.Flags.StashN)
		f.str("index", &req.Flags.Index)
		f.strings("paths", &req.Flags.Paths)
		f.boolean("staged", &req.Flags.Staged)
		f.boolean("worktree", &req.Flags.Worktree)
		f.boolean("budgetConsent", &req.Flags.BudgetConsent)
		f.boolean("showPrompts", &req.Flags.ShowPrompts)
		f.boolean("chunkFlag", &req.Flags.ChunkFlag)
		f.boolean("headControls", &req.Flags.HeadControls)
	})
}

func wireFetchLocal(ctx context.Context, b *bindings, req *core.FetchLocalRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
	req.Human = b.stderr
	req.Diag = b.stderr
	core.DerivePresences(&req.Flags)
	return nil
}

func decodeFix(req *fixcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
}

func wireFix(ctx context.Context, b *bindings, req *fixcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Human = b.stderr
	return nil
}

func decodeIndex(req *indexcore.Request, a *args) {
	a.str("query", &req.Query)
	a.str("symbol", &req.Symbol)
	a.str("at", &req.At)
	a.str("from", &req.From)
	a.str("path", &req.Path)
	a.str("since", &req.Since)
	a.str(argPr, &req.Pr)
	a.strings("paths", &req.Paths)
	a.integer("commits", &req.Commits)
	a.integer("minCount", &req.MinCount)
	a.integer("limit", &req.Limit)
	a.integer("depth", &req.Depth)
}

func wireIndex(ctx context.Context, b *bindings, req *indexcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeInit(req *initcore.Request, a *args) {
	a.str("preset", &req.Preset)
	a.boolean("yes", &req.Yes)
}

func wireInit(ctx context.Context, b *bindings, req *initcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Human = b.stderr
	req.Diag = b.stderr
	return nil
}

func decodeLearn(req *learncore.Request, a *args) {
	a.str("from_bundle", &req.FromBundle)
	a.str(argPr, &req.Pr)
	a.str("accept", &req.Accept)
	a.str("reject", &req.Reject)
	a.boolean("propose", &req.Propose)
	a.boolean("dry_run", &req.DryRun)
	a.str("pack", &req.Pack)
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

func wireLearn(ctx context.Context, b *bindings, req *learncore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.JSONMode = true
	return nil
}

func decodeNext(req *core.NextRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str(argStatus, &req.Status)
	a.boolean("coverage", &req.Coverage)
}

func wireNext(ctx context.Context, b *bindings, req *core.NextRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.Warnings = b.warningList()
	return nil
}

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
		f.boolean("requireCoverage", &req.Flags.RequireCoverage)
		f.boolean("allowSecretValues", &req.Flags.AllowSecretValues)
	})
}

func wirePost(ctx context.Context, b *bindings, req *core.PostRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.JSONMode = true
	req.Warnings = b.warningList()
	return nil
}

func decodeReady(req *readycore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.boolean("check", &req.Check)
}

func wireReady(ctx context.Context, b *bindings, req *readycore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeRecord(req *core.RecordRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.boolean("dry_run", &req.DryRun)
	a.flags(func(f *args) {
		f.boolean("requireCoverage", &req.Flags.RequireCoverage)
	})
}

func wireRecord(ctx context.Context, b *bindings, req *core.RecordRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.Warnings = b.warningList()
	return nil
}

func decodeReport(req *core.ReportRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.flags(func(f *args) {
		f.str(argStatus, &req.Flags.Status)
		f.str("kind", &req.Flags.Kind)
		f.str("category", &req.Flags.Category)
		f.str("output", &req.Flags.Output)
		f.str("sarif", &req.Flags.Sarif)
		f.str("check", &req.Flags.Check)
		f.str("live", &req.Flags.Live)
		f.boolean("cost", &req.Flags.Cost)
		f.boolean("completeness", &req.Flags.Completeness)
	})
}

func wireReport(ctx context.Context, b *bindings, req *core.ReportRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Effective = b.effective
	req.Warnings = b.warningList()
	return nil
}

func decodeShow(req *core.ShowRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
}

func wireShow(ctx context.Context, b *bindings, req *core.ShowRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeThreads(req *fetchcore.ThreadsRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("provider", &req.Provider)
	a.strings("thread", &req.Threads)
	a.boolean("resolve", &req.Resolve)
	a.boolean("dryRun", &req.DryRun)
}

func wireThreads(ctx context.Context, b *bindings, req *fetchcore.ThreadsRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodePack(req *packcore.Request, a *args) {
	a.boolean("list", &req.List)
}

func wirePack(ctx context.Context, b *bindings, req *packcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.SettingsPath = config.SettingsPath(b.env["HOME"], b.env["XDG_CONFIG_HOME"])
	return nil
}

func decodeTrend(req *trendcore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.boolean("overlap", &req.Overlap)
	a.boolean("resolution", &req.Resolution)
}

func wireTrend(ctx context.Context, b *bindings, req *trendcore.Request) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Warnings = b.warningList()
	return nil
}

func decodeTriage(req *triagecore.Request, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("sig", &req.Sig)
	a.str(argStatus, &req.Status)
	a.str("rationale", &req.Rationale)
	a.boolean("force", &req.Force)
	a.boolean("dryRun", &req.DryRun)
}

func wireTriage(ctx context.Context, b *bindings, req *triagecore.Request) error {
	return b.wireRead(ctx, &req.ReadRequest)
}

func decodeVerify(req *core.VerifyRequest, a *args) {
	a.str(argRepo, &req.Repo)
	a.str(argPr, &req.Pr)
	a.str("timeoutRaw", &req.TimeoutRaw)
	a.str("group", &req.Group)
	a.str("command", &req.Command)
	a.boolean("sandbox", &req.Sandbox)
}

func wireVerify(ctx context.Context, b *bindings, req *core.VerifyRequest) error {
	if err := b.wireRead(ctx, &req.ReadRequest); err != nil {
		return err
	}
	req.Environ = b.environList()
	req.JSONMode = true
	req.Warnings = b.warningList()
	return nil
}
