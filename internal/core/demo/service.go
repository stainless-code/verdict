// Package democore is the demo domain service: one zero-config review of a
// public GitHub pull request or of the caller's own working tree, composed from
// the shipped fetch → run → record services and confined to a disposable temp
// directory.
//
// The service never writes in the caller's tree (every artifact lands under a
// temp root it owns), never reads argv/stdio/environment directly (the adapter
// injects the runtime, the streams, and the PATH probe), and never publishes:
// it does not import internal/core/post, names no publish symbol, and calls no
// posting entry point. The publish package does reach this graph transitively
// through fetch's `threads --resolve` host seam, which predates this verb, so
// `internal/archtest` pins that one door instead of claiming the package is
// unreachable.
package democore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/gitcmd"
)

const (
	// codeAgentMissing is the launcher's own code for "installed nowhere":
	// reusing it keeps one meaning per code across verbs.
	codeAgentMissing = "agent-missing"
	// codeNotARepo is the shared "no repository here" refusal.
	codeNotARepo = "not-a-repo"
	// codeBadRange is the shared "this range names nothing" refusal.
	codeBadRange = "bad-range"
	// codeFetchFailed is the fetch boundary's own code for an untyped failure.
	codeFetchFailed = "fetch-failed"
	// codeMissingTool is the shared "install this binary" refusal.
	codeMissingTool = "missing-tool"

	// verbName is the CLI verb, used for display and the envelope.
	verbName = "demo"
	// providerGitHub is the only provider a demo reads by URL.
	providerGitHub = "github"
	// localName is the fetch-local bundle name the local mode writes; the
	// store prefixes it, so record and run resolve `local-demo`.
	localName = "demo"
	// findersOne is the hard-locked finder topology: a demo is one fast pass,
	// and there is no flag to widen it.
	findersOne = "1"
	// ghHostname is the host whose login the demo probes.
	ghHostname = "github.com"
	// ghNoRefresh keeps the login probe from refreshing stored credentials, so
	// a demo sends no request of its own; ghActive asks about the account gh
	// would use; ghUnknownFlag is what a gh older than a flag answers.
	ghAuthStatus  = "auth"
	ghStatusVerb  = "status"
	ghHostFlag    = "--hostname"
	ghNoRefresh   = "--no-refresh"
	ghActive      = "--active"
	ghUnknownFlag = "unknown flag"
	// binGh is the hosting CLI; binCurl is the anonymous transport.
	binGh   = "gh"
	binCurl = "curl"
	// originRemote is the remote the local mode reads its repo slug from.
	originRemote = "origin"
	// fetchedViaGit names the transport value for a local run.
	fetchedViaGit = "git"
	// parentsRange is the range a clean tree falls back to: the last commit.
	parentsRange = "HEAD~1..HEAD"
	// msgNothingToReview is the empty-worktree refusal.
	msgNothingToReview = "nothing to review: clean tree and a root commit — make a change, then re-run `verdict demo`"
	// msgUntrackedExcluded explains the one change the commit range cannot see.
	msgUntrackedExcluded = "untracked files are not part of this review — stage them to include them, then re-run `verdict demo`"
	// msgNoCheckout names both invocation forms for a caller outside a repo.
	msgNoCheckout = "demo needs a public pull request URL or a git repository: " +
		"pass https://github.com/<owner>/<repo>/pull/<n>, or run inside a git repository"
	// msgGhAnon explains the anonymous transport the public read fell back to.
	msgGhAnon = "gh is not signed in for github.com — reading the public API anonymously"
	// tempPattern is the isolated directory's name: one per run, removed on
	// exit unless --keep.
	tempPattern = "verdict-demo-*"
	// slugSep joins the two halves of the owner/repo spelling.
	slugSep = "/"
)

// ghAuthLadder is the login probe in preference order: the active account with
// no credential refresh first, then each older spelling a gh that does not know
// one of the flags still understands.
var ghAuthLadder = [][]string{
	{ghAuthStatus, ghStatusVerb, ghHostFlag, ghHostname, ghActive, ghNoRefresh},
	{ghAuthStatus, ghStatusVerb, ghHostFlag, ghHostname, ghActive},
	{ghAuthStatus, ghStatusVerb, ghHostFlag, ghHostname, ghNoRefresh},
	{ghAuthStatus, ghStatusVerb, ghHostFlag, ghHostname},
}

// Request is the demo service invocation: the injected runtime and streams,
// the adapter's resolved options, the optional target URL, and the seams the
// service cannot invent (the PATH probe and the launcher's commander).
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	SettingsPath string `json:"-"`
	Effective    core.Effective
	// Target is the optional `<public-github-pr-url>` positional; empty asks
	// for the local mode.
	Target string
	// Agent is the operator's --agent override; empty auto-detects.
	Agent string
	// TimeoutRaw is --timeout: the launcher's watchdog budget in seconds. Empty
	// keeps the run verb's default; the run service refuses a value that is not
	// a positive number.
	TimeoutRaw string
	// Checkout is the reviewed checkout: the caller's git top level for the
	// local form, "" for a URL review.
	Checkout string `json:"-"`
	// CallerRoot is the caller's git top level whatever the form ("" when the
	// process is outside a repository). The URL form reads no checkout but must
	// still never write inside the one the operator is standing in.
	CallerRoot string `json:"-"`
	// TmpRoot is the parent of the disposable directory ("" reads the process
	// temp directory, which honors TMPDIR).
	TmpRoot string `json:"-"`
	// LookPath is the injected PATH probe the agent detection uses, exactly
	// like the launcher's own seam.
	LookPath func(string) (string, error) `json:"-"`
	// Commander runs the host and git reads (fetch, record); AgentCommander
	// runs the launcher's watchdog spawn. The two postures differ, so the
	// adapter owns both.
	Commander      fetchcore.Commander `json:"-"`
	AgentCommander core.Commander      `json:"-"`
	Environ        []string            `json:"-"`
	Human          io.Writer           `json:"-"`
	Diag           io.Writer           `json:"-"`
	Warnings       []string
	JSONMode       bool
	Keep           bool
	// RemoveAll deletes the disposable directory; nil reads [os.RemoveAll]. The
	// adapter never sets it: the seam exists so a cleanup failure is graded
	// without depending on directory modes, which a root-run CI ignores.
	RemoveAll func(string) error `json:"-"`
}

// Response carries the demo outcome: the envelope inputs, the exact review body
// record rendered, the topology block the human render adds, and the bundle
// location.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *Data
	Body     string `json:"-"`
	Header   string `json:"-"`
	Facts    string `json:"-"`
	Topology string `json:"-"`
	// TopologyAppended reports that the human render must add the block record's
	// body does not carry (record draws it only above the diagram size gate).
	TopologyAppended bool `json:"-"`
	// Steps is the human "next" block: what to run after the demo.
	Steps    []string `json:"-"`
	Next     string   `json:"-"`
	Warnings []string
}

// Run executes `demo [<public-github-pr-url>] [--agent <name>] [--keep]
// [--json]`: one isolated temp directory, one fetch, one agent launch with a
// single finder, one record, and the rendered review.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, canceled()
	}
	target, agent, ref := prepare(req)
	if ref != nil {
		return nil, ref
	}
	dir, ref := tempRoot(req)
	if ref != nil {
		return nil, ref
	}
	resp := &Response{At: req.TimeUTC()}
	defer removeTemp(req, dir, resp)
	started := nowOf(req)

	b, fetched, ref := fetchBundle(ctx, req, target, dir)
	if ref != nil {
		return nil, ref
	}
	resp.Warnings = stageWarnings(resp.Warnings, fetched.Warnings)
	ran, ref := launch(ctx, req, &b, agent, resp.Warnings)
	if ref != nil {
		return nil, ref
	}
	resp.Warnings = stageWarnings(resp.Warnings, ran.Warnings)
	recorded, ref := recordBundle(ctx, req, &b, resp.Warnings)
	if ref != nil {
		return nil, ref
	}
	resp.Warnings = stageWarnings(resp.Warnings, recorded.Warnings)
	resp.Repo, resp.Pr = b.repo, b.pr
	fillResponse(resp, req, &b, outcome{fetched: fetched, ran: ran, record: recorded}, started)
	return resp, nil
}

// prepare parses the target URL and resolves the agent name, both before any
// directory or host is touched: a refusal here costs nothing and leaves no temp
// directory behind.
func prepare(req *Request) (*Target, string, *core.RefusalError) {
	var target *Target
	if strings.TrimSpace(req.Target) != "" {
		parsed, ref := ParseTarget(req.Target)
		if ref != nil {
			return nil, "", ref
		}
		target = &parsed
	}
	agent := req.Agent
	if agent == "" {
		row, ok := detectAgent(req.LookPath)
		if !ok {
			return nil, "", noAgentRefusal()
		}
		agent = row.Name
	}
	return target, agent, nil
}

// tempRoot creates the one disposable directory every artifact lives in. The
// caller's tree is never a candidate: without this, a local run would write
// `.verdict/` into the checkout it is reviewing.
//
// The path is resolved through symlinks before anything is written: a macOS
// TMPDIR reaches the volume through /var, and the store's no-follow writes
// refuse a path whose parent is a link.
func tempRoot(req *Request) (string, *core.RefusalError) {
	checkout, ref := protectedCheckout(req)
	if ref != nil {
		return "", ref
	}
	if ref := tempParentRefusal(req, checkout); ref != nil {
		return "", ref
	}
	created, err := os.MkdirTemp(req.TmpRoot, tempPattern)
	if err != nil {
		return "", &core.RefusalError{
			Code:    codeFetchFailed,
			Message: fmt.Sprintf("could not create the demo's temp directory: %v", err),
		}
	}
	dir, ref := resolveTempDir(created)
	if ref != nil {
		return "", ref
	}
	// The created directory is checked again after resolution: a symlink target
	// can change between the parent probe and the mkdir, and the promise is
	// about where the bundle actually lands.
	if ref := tempDirRefusal(req, dir, checkout); ref != nil {
		return "", ref
	}
	return dir, nil
}

// tempDirRefusal refuses a created directory that resolved inside the checkout,
// removing it first: nothing will write into it and no --keep run can name it,
// so leaving it behind would leak one directory per refused invocation.
func tempDirRefusal(req *Request, dir, checkout string) *core.RefusalError {
	if checkout == "" || !inside(dir, checkout) {
		return nil
	}
	if err := removeAllOf(req)(dir); err != nil {
		// The refusal below is the operator-facing report: the directory is
		// unusable inside the checkout either way, and the message names it.
		_ = err
	}
	return &core.RefusalError{
		Code: codeFetchFailed,
		Message: "the demo's temp directory " + dir + " resolved inside the checkout " + checkout +
			" — point TMPDIR outside the checkout",
	}
}

// protectedCheckout resolves the checkout the demo must never write into: the
// reviewed tree for a local run, and the caller's own top level for a URL run,
// which reviews someone else's pull request from inside a clone.
func protectedCheckout(req *Request) (string, *core.RefusalError) {
	root := req.Checkout
	if root == "" {
		root = req.CallerRoot
	}
	if root == "" {
		return "", nil
	}
	return resolvedDir(root)
}

// tempParentRefusal refuses a temp parent that is the checkout or sits inside
// it. A TMPDIR pointed at the tree (or a --keep run reading it) would write the
// bundle into the very checkout the demo promises not to touch, so the run
// refuses before creating anything.
func tempParentRefusal(req *Request, checkout string) *core.RefusalError {
	if checkout == "" {
		return nil
	}
	parent := req.TmpRoot
	if parent == "" {
		parent = os.TempDir()
	}
	resolved, ref := resolvedDir(parent)
	if ref != nil {
		return ref
	}
	if inside(resolved, checkout) {
		return &core.RefusalError{
			Code: codeFetchFailed,
			Message: "the demo's temp parent " + resolved + " is inside the checkout " + checkout +
				" — point TMPDIR outside the checkout",
		}
	}
	return nil
}

// resolvedDir resolves one directory through symlinks, refusing a path that
// does not resolve: comparing unresolved spellings would miss the /var and
// /private/var difference on macOS.
func resolvedDir(path string) (string, *core.RefusalError) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", &core.RefusalError{
			Code:    codeFetchFailed,
			Message: fmt.Sprintf("could not resolve %s: %v", path, err),
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &core.RefusalError{
			Code:    codeFetchFailed,
			Message: fmt.Sprintf("could not resolve %s: %v", abs, err),
		}
	}
	return resolved, nil
}

// inside reports whether dir is path itself or lies beneath it, on whole path
// elements so /repo-2 never reads as inside /repo.
func inside(dir, path string) bool {
	if dir == path {
		return true
	}
	return strings.HasPrefix(dir, strings.TrimSuffix(path, string(filepath.Separator))+string(filepath.Separator))
}

// resolveTempDir resolves one created directory through symlinks, removing it
// when it cannot be resolved: nothing will write into it and no --keep run can
// name it, so leaving it behind would leak one directory per failed
// invocation.
func resolveTempDir(created string) (string, *core.RefusalError) {
	dir, err := filepath.EvalSymlinks(created)
	if err != nil {
		_ = os.RemoveAll(created)
		return "", &core.RefusalError{
			Code:    codeFetchFailed,
			Message: fmt.Sprintf("could not resolve the demo's temp directory: %v", err),
		}
	}
	return dir, nil
}

// removeTemp owns the cleanup contract: the directory goes away on success, on
// a refusal, and on a cancel, unless --keep asked to inspect it. A cleanup
// failure warns and never changes the exit code.
func removeTemp(req *Request, dir string, resp *Response) {
	if req.Keep {
		// Streamed as a warning so a refusal still names where the bundle is:
		// the response a refusal returns to the adapter carries no data.
		req.Warn(keptMessage + dir)
		resp.Warnings = append(resp.Warnings, keptMessage+dir)
		return
	}
	if err := removeAllOf(req)(dir); err != nil {
		// Streamed as well as recorded: a refusal never returns the response,
		// so the operator would otherwise never see the directory left behind.
		msg := fmt.Sprintf("could not remove %s: %v", dir, err)
		req.Warn(msg)
		resp.Warnings = append(resp.Warnings, msg)
	}
}

// removeAllOf is the cleanup seam: the injected remover when a test supplied
// one, [os.RemoveAll] otherwise.
func removeAllOf(req *Request) func(string) error {
	if req.RemoveAll != nil {
		return req.RemoveAll
	}
	return os.RemoveAll
}

// bundle is what the fetch phase learned about the review target.
type bundle struct {
	// dir is the temp root every composed service anchors to.
	dir string
	// repo and pr are the positionals record and run read the bundle by.
	repo string
	pr   string
	// fetchedVia names the transport: gh, public-api, or git.
	fetchedVia string
	// checkout is the agent's working directory: the operator's tree for a
	// local review, empty (the temp root) for a URL review.
	checkout string
	// rangeSpec is the range the local mode chose ("" for the working tree), so
	// the printed three-step equivalent re-reviews the same change.
	rangeSpec string
}

// dryRun reports whether the record phase is a dry run. A GitHub PR bundle is
// post's: record's artifact for it is the receipt of a publication the demo
// must never perform, so the URL form records the same dry run `post --dry-run`
// reads. A local bundle has no publication to withhold and records for real.
func (b *bundle) dryRun() bool { return b.checkout == "" }

// fetchBundle runs the one fetch the demo needs, choosing the backend from the
// invocation form.
func fetchBundle(
	ctx context.Context, req *Request, target *Target, dir string,
) (bundle, *fetchcore.Response, *core.RefusalError) {
	if target != nil {
		return fetchRemote(ctx, req, *target, dir)
	}
	return fetchLocal(ctx, req, dir)
}

// fetchRemote reads a public pull request: gh when it is signed in for
// github.com, the anonymous REST API otherwise. The repository layer is absent
// either way — a demo has no checkout it may trust — so the recorded controls
// snapshot is honest about it.
func fetchRemote(
	ctx context.Context, req *Request, target Target, dir string,
) (bundle, *fetchcore.Response, *core.RefusalError) {
	signedIn := ghSignedIn(ctx, req)
	if !signedIn && present(req.LookPath, binGh) {
		req.Warn(msgGhAnon)
	}
	resp, ref := flowOf(req).FetchPR(ctx, &reviewflow.Bundle{Dir: dir, Repo: target.Slug(), Pr: target.Pr},
		&fetchcore.Flags{Provider: providerGitHub, PublicRead: !signedIn, NoRepoLayer: true}, req.Warnings)
	if ref != nil {
		return bundle{}, nil, ref
	}
	via := binGh
	if !signedIn {
		via = fetchedViaPublicAPI
	}
	return bundle{dir: dir, repo: target.Slug(), pr: target.Pr, fetchedVia: via}, resp, nil
}

// fetchLocal reads the operator's own checkout into the temp root: an
// uncommitted change when the tree is dirty, else the last commit. The base
// controls and policy still come from that checkout, so the recorded snapshot
// describes the repository the operator is standing in.
func fetchLocal(ctx context.Context, req *Request, dir string) (bundle, *fetchcore.Response, *core.RefusalError) {
	if req.Checkout == "" {
		return bundle{}, nil, &core.RefusalError{Code: codeNotARepo, Message: msgNoCheckout}
	}
	flags, ref := localMode(ctx, req)
	if ref != nil {
		return bundle{}, nil, ref
	}
	flags.RepoLayerRoot = req.Checkout
	slug := originSlug(ctx, req)
	resp, ref := flowOf(req).FetchLocal(ctx, &reviewflow.Bundle{Dir: dir, Repo: slug, Pr: localName}, &flags, req.Warnings)
	if ref != nil {
		return bundle{}, nil, ref
	}
	return bundle{
		dir: dir, repo: slug, pr: localName, fetchedVia: fetchedViaGit,
		checkout: req.Checkout, rangeSpec: flags.RangeSpec,
	}, resp, nil
}

// localMode picks the diff the local review reads: the working tree when it
// holds tracked changes, else the last commit, and a typed refusal when the
// repository has neither.
func localMode(ctx context.Context, req *Request) (fetchcore.LocalFlags, *core.RefusalError) {
	dirty, ref := gitDirty(ctx, req)
	if ref != nil {
		return fetchcore.LocalFlags{}, ref
	}
	if dirty {
		return fetchcore.LocalFlags{}, nil
	}
	parent, ref := hasParentCommit(ctx, req)
	if ref != nil {
		return fetchcore.LocalFlags{}, ref
	}
	if !parent {
		return fetchcore.LocalFlags{}, &core.RefusalError{Code: codeBadRange, Message: msgNothingToReview}
	}
	// A clean tracked state reads the last commit, which leaves untracked files
	// out of the review: say so instead of silently reviewing less than the
	// operator sees in `git status`.
	if hasUntracked(ctx, req) {
		req.Warn(msgUntrackedExcluded)
	}
	return fetchcore.LocalFlags{RangeSpec: parentsRange}, nil
}

// hasUntracked reports whether the checkout holds untracked, unignored files.
// The probe is advisory: an unreadable answer leaves the run on its commit
// range rather than failing a review over a listing it does not need.
func hasUntracked(ctx context.Context, req *Request) bool {
	res, err := runGit(ctx, req, "ls-files", "--others", "--exclude-standard")
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
}

// gitDirty reports whether the tracked working tree differs from HEAD. Anything
// but "clean" or "differs" is a real failure: guessing the mode from an
// unreadable repository would review the wrong thing. The one-shot config
// override stops diff from refreshing (and therefore writing) the checkout's
// index, which a read-only probe must never do, and a stopped probe is the
// cancel rather than a dirty tree.
func gitDirty(ctx context.Context, req *Request) (bool, *core.RefusalError) {
	res, err := runGit(ctx, req, "-c", "diff.autoRefreshIndex=false", "diff", "--quiet", "HEAD")
	if ctx.Err() != nil {
		return false, canceled()
	}
	if err != nil {
		return false, gitRefusal(err)
	}
	switch res.ExitCode {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, &core.RefusalError{
			Code:    codeFetchFailed,
			Message: "could not read the working tree: " + headText(res.Stderr),
		}
	}
}

// hasParentCommit reports whether HEAD has a parent, i.e. whether HEAD~1 is a
// reviewable range. A stopped probe is the cancel, never a root commit, and an
// unreadable one is a real failure.
func hasParentCommit(ctx context.Context, req *Request) (bool, *core.RefusalError) {
	res, err := runGit(ctx, req, "rev-parse", "--verify", "HEAD~1^{commit}")
	if ctx.Err() != nil {
		return false, canceled()
	}
	if err != nil {
		return false, gitRefusal(err)
	}
	return res.ExitCode == 0, nil
}

// originSlug names the local bundle the way an operator would: the origin
// remote's owner/repo, else a local/ name for the directory. It is display and
// provenance only — never a fetch target that could be mistaken for a PR.
func originSlug(ctx context.Context, req *Request) string {
	res, err := runGit(ctx, req, "remote", "get-url", originRemote)
	if err == nil && res.ExitCode == 0 {
		if slug, ok := parseSlug(strings.TrimSpace(res.Stdout)); ok {
			return slug
		}
	}
	return "local" + slugSep + checkoutName(req.Checkout)
}

// parseSlug reads owner/repo out of one git remote URL, in the three spellings
// a remote is configured with.
func parseSlug(raw string) (string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git")
	switch {
	case strings.Contains(trimmed, "://"):
		_, after, _ := strings.Cut(trimmed, "://")
		_, path, ok := strings.Cut(after, "/")
		return twoSegments(path, ok)
	case strings.Contains(trimmed, "@"):
		_, path, ok := strings.Cut(trimmed, ":")
		return twoSegments(path, ok)
	default:
		return twoSegments(trimmed, true)
	}
}

// checkoutName names the operator's checkout directory: the probe may answer a
// cwd-relative "." (every verb accepts that spelling), so the name resolves
// against the working directory first.
func checkoutName(checkout string) string {
	if abs, err := filepath.Abs(checkout); err == nil {
		return filepath.Base(abs)
	}
	return filepath.Base(checkout)
}

// twoSegments accepts exactly owner/repo, so a deeper path or a bare host never
// reads as a slug.
func twoSegments(path string, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.Trim(path, slugSep), slugSep)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + slugSep + parts[1], true
}

// runGit runs one hook-free git subcommand ([gitcmd.Argv]) from the process
// working directory: fetch-local reads the same tree the same way, so the
// demo's probes cannot disagree with the diff it asks for.
func runGit(ctx context.Context, req *Request, args ...string) (core.Result, error) {
	res, err := req.Commander.Run(ctx, &core.Command{Argv: gitcmd.Argv("", args...), Env: gitcmd.Env(nil)})
	if err != nil {
		return res, err //nolint:wrapcheck // the seam error is mapped at the caller
	}
	return res, nil
}

// gitRefusal maps a git spawn failure: a missing binary says so, anything else
// reads as an unreadable working tree.
func gitRefusal(err error) *core.RefusalError {
	if _, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &core.RefusalError{Code: codeMissingTool, Message: "git is not on PATH"}
	}
	return &core.RefusalError{Code: codeFetchFailed, Message: "could not read the working tree: " + err.Error()}
}

// ghSignedIn reports whether gh exists and has an active account for the host.
// --active asks about the account gh would actually use, so a stale second
// login cannot read as signed in; --no-refresh keeps the probe from refreshing
// stored credentials, so a demo sends no request of its own. A gh older than
// either flag refuses it as unknown, and ghAuthLadder walks back to the older
// spelling until one answers.
func ghSignedIn(ctx context.Context, req *Request) bool {
	if err := req.Commander.LookPath(binGh); err != nil {
		return false
	}
	return ghLadderSignedIn(ctx, req)
}

// ghLadderSignedIn walks the probe spellings: the first definite answer
// decides, and an unknown-flag refusal moves on to the next one.
func ghLadderSignedIn(ctx context.Context, req *Request) bool {
	for _, args := range ghAuthLadder {
		res, err := req.Commander.Run(ctx, &core.Command{Argv: append([]string{binGh}, args...)})
		if err == nil && res.ExitCode == 0 {
			return true
		}
		if err != nil || !strings.Contains(res.Stderr, ghUnknownFlag) {
			return false
		}
	}
	return false
}

// present reports whether one tool resolves on PATH.
func present(look func(string) (string, error), tool string) bool {
	if look == nil {
		return false
	}
	_, err := look(tool)
	return err == nil
}

// watchdogBudget is the launcher's budget: the seconds --timeout named, else
// the run verb's default. The raw value rides to the run service, which refuses
// a non-positive or non-numeric value as bad-timeout.
func watchdogBudget(raw string) string {
	if raw == "" {
		return runcore.DefaultTimeoutS
	}
	return raw
}

// launch runs the operator's agent against the bundle with a single finder.
func launch(
	ctx context.Context, req *Request, b *bundle, agent string, warnings []string,
) (*core.RunResponse, *core.RefusalError) {
	return flowOf(req).Run(ctx, b.flow(), reviewflow.Launch{
		Agent: agent, FindersRaw: findersOne, TimeoutRaw: watchdogBudget(req.TimeoutRaw),
	}, warnings)
}

// recordBundle records the launched review and hands back the rendered body.
func recordBundle(
	ctx context.Context, req *Request, b *bundle, warnings []string,
) (*recordcore.Response, *core.RefusalError) {
	return flowOf(req).Record(ctx, b.flow(), reviewflow.RecordOptions{DryRun: b.dryRun()}, warnings)
}

// stageWarnings folds one composed service's warnings into the run's list.
// Every composed service starts from the list it is handed and hands the whole
// list back, so the returned lines are the authority: an earlier warning the
// next stage echoes appears once, and a service that emits its own line twice
// still reads once.
func stageWarnings(acc, echoed []string) []string {
	return dedupe(union(acc, echoed))
}

// union appends one phase's warnings to the run's list, preserving order.
func union(acc, next []string) []string {
	return append(append([]string{}, acc...), next...)
}

// flowOf is the shared fetch → run → record wiring for this invocation: the
// demo's runtime, both commanders, and the caller's checkout as the tool root.
// A demo launches from the shipped agent table only and carries no personal
// spend policy, so those two fields stay empty.
func flowOf(req *Request) *reviewflow.Flow {
	return &reviewflow.Flow{
		ReadRequest:    req.ReadRequest,
		SettingsPath:   req.SettingsPath,
		Effective:      req.Effective,
		Commander:      req.Commander,
		AgentCommander: req.AgentCommander,
		LookPath:       req.LookPath,
		Environ:        req.Environ,
		Human:          req.Human,
		Diag:           req.Diag,
		JSONMode:       req.JSONMode,
	}
}

// flow is the bundle as the shared pipeline names it: the temp root every
// composed service anchors to and the agent's checkout.
func (b *bundle) flow() *reviewflow.Bundle {
	return &reviewflow.Bundle{Dir: b.dir, Repo: b.repo, Pr: b.pr, Checkout: b.checkout}
}

// canceled is the shared cancel refusal (the adapter exits 3 on it).
func canceled() *core.RefusalError {
	return &core.RefusalError{Code: "canceled", Message: "canceled before demo completed"}
}

// nowOf reads the injected clock, defaulting to the wall clock.
func nowOf(req *Request) time.Time {
	if req.Runtime != nil && req.Now != nil {
		return req.Now()
	}
	return time.Now()
}

// headText is the first line of a child's stderr, for a refusal message.
func headText(stderr string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n")
	return line
}
