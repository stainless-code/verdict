// Package backtest mines SZZ bug-introducing PR candidates from a local git
// clone. It never calls a model and writes a deterministic artifact: the same
// repository, window, and run id produce byte-identical mine.json. The verb
// is a candidate generator, not a verdict: a candidate names a merged PR and
// the lines a later fix blamed on it, and nothing here proves the blame is a
// real defect.
package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Refusal codes this verb owns.
const (
	codeBadSince       = "bad-since"
	codeBadUntil       = "bad-until"
	codeRunMismatch    = "run-mismatch"
	codeBacktestFail   = "backtest-failed"
	codeNotARepo       = "not-a-repo"
	codeBadSlug        = "bad-slug"
	codeCorruptStore   = "corrupt-store"
	codeCanceled       = "canceled"
	codeBadFlags       = "bad-flags"
	codeWindowTooLarge = "window-too-large"
	modeMine           = "mine"
	methodSZZ          = "szz-blame-v1"
	schemaVersion      = "1"
	headSourceMerge    = "merge-time"
	msgCanceled        = "canceled before backtest mine completed"
	shallowWarning     = "shallow clone: an introducer at the clone boundary reads as no-pr; " +
		"run 'git fetch --unshallow' for the full history"
	artifactDir       = ".verdict/backtest"
	commandOutputCap  = 16 << 20
	gitDateLayout     = "2006-01-02T15:04:05Z"
	humanDateLayout   = "2006-01-02"
	shortSHA          = 7
	dayHours          = 24
	weekDays          = 7
	monthsPerYear     = 12
	fetchDiffCapBytes = 512000
	maxFixLines       = 200
	maxFixFiles       = 5
	numStatFields     = 3
	colonSep          = ":"
	dirPerm           = 0o755
)

// Request is one `backtest mine` invocation. The adapter supplies the parsed
// mode and window values plus the injected runtime; direct callers may omit
// Runtime, in which case the wall clock and the production commander run.
type Request struct {
	Runtime  *core.Runtime
	RepoRoot string
	Mode     string
	Since    string
	Until    string
	Run      string
	Warnings []string
}

// Response carries the envelope data and the accumulated warnings.
type Response struct { //nolint:govet // field grouping documents the response surface
	Data     *Data
	Warnings []string
	Next     string
	At       string
}

// Data is the envelope's data object, in published key order.
type Data struct { //nolint:govet // field order is the published property order
	Run       string     `json:"run"`
	Mode      string     `json:"mode"`
	Since     Resolution `json:"since"`
	Until     Resolution `json:"until"`
	Method    string     `json:"method"`
	Artifacts Artifacts  `json:"artifacts"`
	Mine      *Mine      `json:"mine"`
}

// Wire renders the data object in published key order.
func (d *Data) Wire() *jq.Obj {
	return jq.NewObj(
		"run", d.Run,
		"mode", d.Mode,
		"since", core.Wire(d.Since),
		"until", core.Wire(d.Until),
		"method", d.Method,
		"artifacts", core.Wire(d.Artifacts),
		"mine", mineWire(d.Mine),
	)
}

// mineWire renders the nested mine object, or null when absent.
func mineWire(m *Mine) any {
	if m == nil {
		return nil
	}
	return jq.NewObj("candidates", core.Wire(m.Candidates), "skipped", core.Wire(m.Skipped))
}

// Resolution is one end of the mined window: the spec the operator passed,
// the commit it resolved to, and that commit's committer date. Cutoff is the
// calendar cutoff a duration spec computed; a ref spec leaves it empty.
type Resolution struct {
	Spec   string `json:"spec"`
	SHA    string `json:"sha"`
	Date   string `json:"date"`
	Cutoff string `json:"cutoff,omitempty"`
}

// Artifacts names the two files a run writes, relative to the repository root.
type Artifacts struct {
	Run  string `json:"run"`
	Mine string `json:"mine"`
}

// Mine is the artifact body shared by mine.json and the envelope.
type Mine struct {
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
}

// Candidate is one merged PR: the head and base it was reviewed at, how the
// head was reconstructed, the fixes that blamed lines on it, and the union of
// those bug anchors.
type Candidate struct {
	Head           string   `json:"head"`
	Base           string   `json:"base"`
	LandedBy       string   `json:"landed_by"`
	PR             *int     `json:"pr"`
	Reconstruction string   `json:"reconstruction"`
	HeadSource     string   `json:"head_source"`
	MergedAt       string   `json:"merged_at"`
	HeadAt         string   `json:"head_at"`
	Fixes          []string `json:"fixes"`
	Bugs           []Bug    `json:"bugs"`
}

// Bug is one anchored line at the PR head. Fix is the seed fix whose deleted
// line produced the anchor.
type Bug struct { //nolint:govet // field order is the published property order
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Fix  string `json:"fix"`
}

// Skipped is one seed fix that produced no candidate, with the deterministic
// reason that excluded it.
type Skipped struct {
	Reason string `json:"reason"`
	Fix    string `json:"fix"`
	PR     string `json:"pr,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// MineDoc is mine.json: the candidate list, the skips, and the algorithm id.
// It carries no timestamps, so a rerun is byte-identical.
type MineDoc struct {
	SchemaVersion string      `json:"schema_version"`
	Method        string      `json:"method"`
	Candidates    []Candidate `json:"candidates"`
	Skipped       []Skipped   `json:"skipped"`
}

// RunDoc is run.json: the only artifact carrying the resolution, the git
// version, and timestamps.
type RunDoc struct {
	SchemaVersion string     `json:"schema_version"`
	Run           string     `json:"run"`
	Mode          string     `json:"mode"`
	CreatedAt     string     `json:"created_at"`
	GitVersion    string     `json:"git_version"`
	Since         Resolution `json:"since"`
	Until         Resolution `json:"until"`
	Method        string     `json:"method"`
	Artifacts     Artifacts  `json:"artifacts"`
}

// Run runs the mine mode: resolve the window, collect seeds, blame their
// deleted lines, map introducers to PRs, anchor the blamed text at the PR
// head, and write run.json and mine.json.
//
//nolint:revive // the resolution ladder is one refusal check per step
func Run(ctx context.Context, req *Request) (resp *Response, ref *core.RefusalError) {
	st := &state{req: req, rt: req.Runtime, warnings: append([]string{}, req.Warnings...)}
	defer st.carry(&resp, &ref)
	if err := ctx.Err(); err != nil {
		return nil, st.refuse(codeCanceled, msgCanceled)
	}
	if req.Mode != modeMine {
		return nil, st.refuse(codeBadFlags, "backtest needs the 'mine' mode")
	}
	st.trust = st.readTrust(ctx)
	root, ref := st.repoRoot(ctx)
	if ref != nil {
		return nil, ref
	}
	st.root = root
	version, ref := st.gitVersion(ctx)
	if ref != nil {
		return nil, ref
	}
	st.shallow(ctx)
	until, ref := st.until(ctx)
	if ref != nil {
		return nil, ref
	}
	since, ref := st.since(ctx, until)
	if ref != nil {
		return nil, ref
	}
	run, ref := st.runID(since, until)
	if ref != nil {
		return nil, ref
	}
	if ref := st.checkRunDir(run, since, until); ref != nil {
		return nil, ref
	}
	candidates, skipped, ref := st.mine(ctx, since, until)
	if ref != nil {
		return nil, ref
	}
	data := st.data(run, since, until, candidates, skipped)
	if ref := st.write(data, version); ref != nil {
		return nil, ref
	}
	st.human(data)
	return &Response{
		Data:     data,
		Warnings: st.warnings,
		Next:     "inspect mine.json, then add chosen candidates to the benchmark corpus by hand",
		At:       st.timeUTC(),
	}, nil
}

// state carries the injected runtime, the resolved repository root, the user's
// forwarded safe.directory list, the verb's cancel text, and the warnings. The
// mine and replay modes share it: everything git-facing reads these fields and
// nothing here is mine-specific.
type state struct {
	req       *Request
	rt        *core.Runtime
	cancelMsg string
	root      string
	trust     []string
	warnings  []string
}

// carry replays the warnings onto the response or the refusal.
func (st *state) carry(resp **Response, ref **core.RefusalError) {
	if *ref != nil {
		(*ref).Warnings = st.warnings
	}
	if *resp != nil {
		(*resp).Warnings = st.warnings
	}
}

// refuse builds a typed refusal.
func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg}
}

// canceled refuses a run whose context is done, so an interrupt reports the
// cancel (exit 3) rather than whichever step happened to notice it.
func (st *state) canceled(ctx context.Context) *core.RefusalError {
	if ctx.Err() == nil {
		return nil
	}
	return st.refuse(codeCanceled, st.cancelText())
}

// cancelText is the mode's frozen cancel message: the miner's unless the caller
// set the mode's own.
func (st *state) cancelText() string {
	if st.cancelMsg != "" {
		return st.cancelMsg
	}
	return msgCanceled
}

// gitFailure refuses a failed git step: a canceled run reports the cancel,
// never the killed child's message.
func (st *state) gitFailure(ctx context.Context, msg string) *core.RefusalError {
	if ctx.Err() != nil {
		return st.refuse(codeCanceled, st.cancelText())
	}
	return st.refuse(codeBacktestFail, msg)
}

// now reads the injected clock.
func (st *state) now() time.Time {
	if st.rt != nil && st.rt.Now != nil {
		return st.rt.Now()
	}
	return time.Now()
}

// timeUTC formats the injected clock to the second.
func (st *state) timeUTC() string {
	return st.now().UTC().Format(gitDateLayout)
}

// commander returns the injected commander, falling back to the separated
// read runner so a direct caller still runs and stderr stays out of stdout.
func (st *state) commander() core.Commander {
	if st.rt != nil && st.rt.Commander != nil {
		return st.rt.Commander
	}
	return coreexec.Runner{MissingTool: true}
}

// human writes one human line through the injected observer.
func (st *state) humanLine(line string) {
	if st.rt != nil {
		st.rt.Human(line)
	}
}

// notARepoStderr is git's own phrasing for a path outside every worktree;
// every other non-zero rev-parse exit keeps git's line.
const notARepoStderr = "not a git repository"

// repoRoot resolves the git top level: the checkout outside a repository is a
// typed not-a-repo, never a silent cwd-relative read. Any other git failure —
// a checkout owned by another uid is the one an operator hits — is a
// backtest-failed carrying git's first stderr line, so the cause is named
// instead of hidden behind not-a-repo.
func (st *state) repoRoot(ctx context.Context) (string, *core.RefusalError) {
	return st.resolveRoot(ctx, st.req.RepoRoot)
}

// resolveRoot resolves one candidate repository path to its git top level.
func (st *state) resolveRoot(ctx context.Context, root string) (string, *core.RefusalError) {
	if root == "" {
		root = "."
	}
	out, exit, stderr, err := st.git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 && !strings.Contains(stderr, notARepoStderr) {
		return "", st.gitFailure(ctx, "git rev-parse failed: "+firstLine(stderr))
	}
	if exit != 0 || strings.TrimSpace(out) == "" {
		return "", st.refuse(codeNotARepo, "not a git repository: "+root)
	}
	return filepath.Clean(strings.TrimSpace(out)), nil
}

// warn records one warning on the envelope and streams it through the
// injected progress observer, whose adapter adds the `warn: ` prefix. The
// same message is recorded once: nested services (a replay's fetch, run, and
// record) report the same fact through their own warning lists.
func (st *state) warn(msg string) {
	if slices.Contains(st.warnings, msg) {
		return
	}
	st.warnings = append(st.warnings, msg)
	if st.rt != nil {
		st.rt.Warn(msg)
	}
}

// shallow warns when the window is read from a shallow clone: a boundary
// commit carries no parents in the clone, so an introducer outside it reads
// as no-pr. The probe is advisory, so an old git without the flag stays quiet.
func (st *state) shallow(ctx context.Context) {
	out, exit, _, err := st.git(ctx, st.root, "rev-parse", "--is-shallow-repository")
	if err != nil || exit != 0 || strings.TrimSpace(out) != "true" {
		return
	}
	st.warn(shallowWarning)
}

// gitVersion records the exact git that produced the artifact.
func (st *state) gitVersion(ctx context.Context) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "--version")
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return "", st.gitFailure(ctx, "git --version failed: "+firstLine(stderr))
	}
	version := strings.TrimSpace(firstLine(out))
	if version == "" {
		return "", st.refuse(codeBacktestFail, "git --version printed nothing")
	}
	return version, nil
}

// resolveRef resolves one window ref, reporting a cancel as a refusal and a
// plain miss as unresolved so the caller names the window end it refused.
func (st *state) resolveRef(ctx context.Context, spec string) (string, bool, *core.RefusalError) {
	sha, ok := st.revParse(ctx, spec)
	if ok {
		return sha, true, nil
	}
	if ref := st.canceled(ctx); ref != nil {
		return "", false, ref
	}
	return "", false, nil
}

// until resolves --until (default HEAD) and requires it to be in HEAD's
// history, so the mined window never mixes unrelated lines.
//
//nolint:revive // the window-resolution ladder is one refusal check per rule
func (st *state) until(ctx context.Context) (Resolution, *core.RefusalError) {
	spec := st.req.Until
	if spec == "" {
		spec = "HEAD"
	}
	sha, ok, ref := st.resolveRef(ctx, spec)
	if ref != nil {
		return Resolution{}, ref
	}
	if !ok {
		return Resolution{}, st.refuse(codeBadUntil, "cannot resolve --until '"+spec+"' to a commit")
	}
	head, ok, ref := st.resolveRef(ctx, "HEAD")
	if ref != nil {
		return Resolution{}, ref
	}
	if !ok {
		return Resolution{}, st.refuse(codeBadUntil, "HEAD does not resolve to a commit")
	}
	if sha != head {
		ancestor, ref := st.isAncestor(ctx, sha, head)
		if ref != nil {
			return Resolution{}, ref
		}
		if !ancestor {
			return Resolution{}, st.refuse(codeBadUntil,
				"--until '"+spec+"' is not an ancestor of HEAD")
		}
	}
	date, ref := st.commitDate(ctx, sha)
	if ref != nil {
		return Resolution{}, ref
	}
	return Resolution{Spec: spec, SHA: sha, Date: date}, nil
}

// since resolves --since: a duration is a calendar span back from --until's
// commit date, a ref is a commit that must be an ancestor of --until.
//
//nolint:revive // the window-resolution ladder is one refusal check per rule
func (st *state) since(ctx context.Context, until Resolution) (Resolution, *core.RefusalError) {
	spec := st.req.Since
	if spec == "" {
		return Resolution{}, st.refuse(codeBadSince,
			"backtest mine needs --since <90d|12w|18mo|2y|ref>")
	}
	if bareMonthRe.MatchString(spec) {
		return Resolution{}, st.refuse(codeBadSince,
			"bare m is not a duration unit; use d, w, mo, or y")
	}
	if span, ok := parseSpan(spec); ok {
		return st.durationSince(ctx, spec, span, until)
	}
	sha, ok, ref := st.resolveRef(ctx, spec)
	if ref != nil {
		return Resolution{}, ref
	}
	if !ok {
		return Resolution{}, st.refuse(codeBadSince, "cannot resolve --since '"+spec+"' to a commit")
	}
	ancestor, ref := st.isAncestor(ctx, sha, until.SHA)
	if ref != nil {
		return Resolution{}, ref
	}
	if !ancestor {
		return Resolution{}, st.refuse(codeBadSince,
			"--since '"+spec+"' is not an ancestor of --until '"+until.Spec+"'")
	}
	date, ref := st.commitDate(ctx, sha)
	if ref != nil {
		return Resolution{}, ref
	}
	return Resolution{Spec: spec, SHA: sha, Date: date}, nil
}

// durationSince turns a calendar span into the newest commit at or before the
// cutoff, falling back to the history root when the cutoff predates it.
func (st *state) durationSince(
	ctx context.Context, spec string, span span, until Resolution,
) (Resolution, *core.RefusalError) {
	at, err := time.Parse(time.RFC3339, until.Date)
	if err != nil {
		return Resolution{}, st.refuse(codeBacktestFail, "unreadable --until commit date: "+until.Date)
	}
	cutoff := span.back(at.UTC()).Format(gitDateLayout)
	sha, ref := st.commitBefore(ctx, until.SHA, cutoff)
	if ref != nil {
		return Resolution{}, ref
	}
	if sha == "" {
		sha, ref = st.rootCommit(ctx, until.SHA)
		if ref != nil {
			return Resolution{}, ref
		}
	}
	if sha == "" {
		return Resolution{}, st.refuse(codeBadSince,
			"no commit at or before "+cutoff+" in --until '"+until.Spec+"'")
	}
	date, ref := st.commitDate(ctx, sha)
	if ref != nil {
		return Resolution{}, ref
	}
	return Resolution{Spec: spec, SHA: sha, Date: date, Cutoff: cutoff}, nil
}

// span is one parsed duration: an amount and its unit.
type span struct {
	unit string
	n    int
}

// durationRe matches the accepted duration shapes. A bare `m` never matches.
var durationRe = regexp.MustCompile(`^([0-9]+)(d|w|mo|y)$`)

// bareMonthRe matches the refused bare-month form.
var bareMonthRe = regexp.MustCompile(`^[0-9]+m$`)

// parseSpan parses one --since duration.
func parseSpan(spec string) (span, bool) {
	match := durationRe.FindStringSubmatch(spec)
	if match == nil {
		return span{}, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n <= 0 {
		return span{}, false
	}
	return span{n: n, unit: match[2]}, true
}

// back moves a commit date by the span.
func (s span) back(at time.Time) time.Time {
	switch s.unit {
	case "d":
		return at.AddDate(0, 0, -s.n)
	case "w":
		return at.AddDate(0, 0, -weekDays*s.n)
	case "mo":
		return at.AddDate(0, -s.n, 0)
	case "y":
		return at.AddDate(0, -monthsPerYear*s.n, 0)
	default:
		return at
	}
}

// runIDSlug is the one --run rule both verbs apply: the id is one path segment
// under .verdict/backtest, so it must already be slug-shaped, and it must leave
// the frame for the bundle name inside fetch-local's name cap — a longer id
// would let two runs sharing a prefix be cut onto the same bundle.
func runIDSlug(run string) *core.RefusalError {
	if readcommon.Slugify(run) != run {
		return &core.RefusalError{
			Code:    codeBadSlug,
			Message: "--run must be lowercase letters, digits, and dashes: '" + run + "'",
		}
	}
	if len(run) > runIDMax {
		return &core.RefusalError{Code: codeBadSlug, Message: fmt.Sprintf(
			"--run must be at most %d bytes: 'bt-<n>-' plus the id must fit the %d-byte bundle name cap, got %d",
			runIDMax, replaySlugMax, len(run))}
	}
	return nil
}

// runID resolves the default `<until7>-<since-slug>` id, or validates --run.
func (st *state) runID(since, until Resolution) (string, *core.RefusalError) {
	if st.req.Run != "" {
		if ref := runIDSlug(st.req.Run); ref != nil {
			return "", ref
		}
		return st.req.Run, nil
	}
	slug := sinceSlug(since)
	if slug == "" {
		slug = since.SHA[:shortSHA]
	}
	id := until.SHA[:shortSHA] + "-" + slug
	// A derived id is refused rather than clipped: clipping would silently merge
	// two windows onto one id. The operator chose the ref, not the id, so the
	// refusal names --run as the way out.
	if len(id) > runIDMax {
		return "", st.refuse(codeBadSlug, fmt.Sprintf(
			"the run id '%s' from --since is %d bytes; pass --run with at most %d bytes "+
				"('bt-<n>-' plus the id must fit the %d-byte bundle name cap)",
			id, len(id), runIDMax, replaySlugMax))
	}
	return id, nil
}

// hexRe matches a commit id the operator may have passed as --since.
var hexRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// sinceSlug renders the since side of the default run id.
func sinceSlug(since Resolution) string {
	if _, ok := parseSpan(since.Spec); ok {
		return since.Spec
	}
	if hexRe.MatchString(since.Spec) && since.SHA != "" {
		return since.SHA[:shortSHA]
	}
	return readcommon.Slugify(since.Spec)
}

// checkRunDir refuses an existing run whose stored resolution differs.
func (st *state) checkRunDir(run string, since, until Resolution) *core.RefusalError {
	path := filepath.Join(st.root, artifactDir, run, "run.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return st.refuse(codeBacktestFail, "cannot read "+display(st.root, path)+": "+err.Error())
	}
	var doc RunDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return st.refuse(codeCorruptStore, "unreadable "+display(st.root, path)+"; pick another --run")
	}
	if doc.Mode != modeMine || doc.Since.SHA != since.SHA || doc.Until.SHA != until.SHA {
		return st.refuse(codeRunMismatch, fmt.Sprintf(
			"run '%s' was mined for %s..%s; pick another --run or delete %s",
			run, short(doc.Since.SHA), short(doc.Until.SHA), display(st.root, filepath.Dir(path))))
	}
	return nil
}

// mine collects candidates and skips over the resolved window.
func (st *state) mine(ctx context.Context, since, until Resolution) ([]Candidate, []Skipped, *core.RefusalError) {
	seed := newSeed(st, since, until)
	commits, ref := st.rangeCommits(ctx, since.SHA, until.SHA)
	if ref != nil {
		return nil, nil, ref
	}
	for _, commit := range commits {
		if ref := st.canceled(ctx); ref != nil {
			return nil, nil, ref
		}
		if ref := seed.consider(ctx, commit); ref != nil {
			return nil, nil, ref
		}
	}
	candidates, skipped := seed.finish()
	return candidates, skipped, nil
}

// newSeed builds one empty accumulator.
func newSeed(st *state, since, until Resolution) *seedState {
	return &seedState{
		st: st, since: since, until: until,
		units: map[string]prUnit{}, prCaps: map[string]bool{},
		builders: map[string]*candidateBuilder{},
	}
}

// rangeCommits lists every commit in since..until, newest first, with parents.
// A window too large for one capture refuses by name so the operator narrows
// --since instead of reading a git failure.
func (st *state) rangeCommits(ctx context.Context, since, until string) ([]commit, *core.RefusalError) {
	out, exit, stderr, truncated, err := st.gitCapped(ctx, commandOutputCap, st.root,
		"rev-list", "--parents", since+".."+until)
	if err != nil {
		return nil, st.gitFailure(ctx, err.Error())
	}
	if truncated {
		return nil, st.refuse(codeWindowTooLarge,
			"the window "+short(since)+".."+short(until)+" is too large to list in one capture; narrow --since")
	}
	if exit != 0 {
		return nil, st.gitFailure(ctx, "git rev-list failed: "+firstLine(stderr))
	}
	var commits []commit
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		commits = append(commits, commit{sha: fields[0], parents: fields[1:]})
	}
	return commits, nil
}

// commit is one commit id and its parents.
type commit struct {
	sha     string
	parents []string
}

// data builds the envelope data object.
func (st *state) data(run string, since, until Resolution, candidates []Candidate, skipped []Skipped) *Data {
	dir := filepath.Join(st.root, artifactDir, run)
	runPath := display(st.root, filepath.Join(dir, "run.json"))
	minePath := display(st.root, filepath.Join(dir, "mine.json"))
	return &Data{
		Run:       run,
		Mode:      modeMine,
		Since:     since,
		Until:     until,
		Method:    methodSZZ,
		Artifacts: Artifacts{Run: runPath, Mine: minePath},
		Mine:      &Mine{Candidates: candidates, Skipped: skipped},
	}
}

// write lands mine.json then run.json atomically; run.json's presence means
// the deterministic artifact beside it is complete.
func (st *state) write(data *Data, gitVersion string) *core.RefusalError {
	dir := filepath.Join(st.root, artifactDir, data.Run)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return st.refuse(codeBacktestFail, "cannot create "+display(st.root, dir)+": "+err.Error())
	}
	mine := MineDoc{SchemaVersion: schemaVersion, Method: methodSZZ, Candidates: data.Mine.Candidates, Skipped: data.Mine.Skipped}
	if err := atomic.WriteFile(filepath.Join(dir, "mine.json"), []byte(jq.Pretty(mine))); err != nil {
		return st.refuse(codeBacktestFail, "cannot write mine.json: "+err.Error())
	}
	run := RunDoc{
		SchemaVersion: schemaVersion, Run: data.Run, Mode: data.Mode, CreatedAt: st.timeUTC(),
		GitVersion: gitVersion, Since: data.Since, Until: data.Until, Method: data.Method,
		Artifacts: data.Artifacts,
	}
	if err := atomic.WriteFile(filepath.Join(dir, "run.json"), []byte(jq.Pretty(run))); err != nil {
		return st.refuse(codeBacktestFail, "cannot write run.json: "+err.Error())
	}
	return nil
}

// human renders the header, one line per candidate, the skipped footer, and
// the not-citable line.
func (st *state) human(data *Data) {
	st.humanLine(fmt.Sprintf("backtest mine %s  (%s..%s)",
		data.Run, humanSide(data.Since), humanSide(data.Until)))
	if len(data.Mine.Candidates) == 0 {
		st.humanLine("  (no candidates)")
	}
	for i := range data.Mine.Candidates {
		st.humanLine(candidateLine(&data.Mine.Candidates[i]))
	}
	st.humanLine(skippedLine(data.Mine.Skipped))
	st.humanLine("not citable — SZZ-derived: candidates are blame-derived guesses, not verified defects")
}

// humanSide renders one window end: a ref keeps its name, a raw commit id
// collapses to its short form.
func humanSide(r Resolution) string {
	if hexRe.MatchString(r.Spec) {
		return short(r.SHA)
	}
	return r.Spec + " " + short(r.SHA)
}

// candidateLine renders one candidate as one human line.
func candidateLine(c *Candidate) string {
	label := short(c.Head)
	if c.PR != nil {
		label = "#" + strconv.Itoa(*c.PR)
	}
	bugs := make([]string, 0, len(c.Bugs))
	for _, bug := range c.Bugs {
		bugs = append(bugs, bug.Path+":"+strconv.Itoa(bug.Line)+" "+bug.Kind)
	}
	return fmt.Sprintf("  %s  %s  %s  %s  %s",
		short(c.Head), dateOnly(c.MergedAt), label, c.Reconstruction, strings.Join(bugs, ", "))
}

// skippedLine renders the skipped-by-reason footer in reason order.
func skippedLine(skipped []Skipped) string {
	if len(skipped) == 0 {
		return "skipped: none"
	}
	counts := map[string]int{}
	for _, skip := range skipped {
		counts[skip.Reason]++
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	slices.Sort(reasons)
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%s %d", reason, counts[reason]))
	}
	return "skipped: " + strings.Join(parts, ", ")
}

// display renders a path relative to root, slash-form.
func display(root, path string) string {
	return readcommon.DisplayPath(root, path)
}

// short renders a commit id's seven-character form, or "-" when empty.
func short(sha string) string {
	if len(sha) < shortSHA {
		return sha
	}
	return sha[:shortSHA]
}

// dateOnly renders an RFC 3339 date as its calendar day.
func dateOnly(date string) string {
	if len(date) >= len(humanDateLayout) {
		return date[:len(humanDateLayout)]
	}
	return date
}

// firstLine returns s before its first newline, trimmed.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
