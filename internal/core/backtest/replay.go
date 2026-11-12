package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Replay constants. The bundle name is the fetch-local name the store prefixes
// with `local-`, so `local-bt-<n>-<run>` is what the operator sees on disk: the
// index comes first so the store's name cap can only ever cut the run tail.
const (
	modeReplay         = "replay"
	replayBundlePrefix = "bt-"
	// replaySlugMax mirrors the 48-byte cap fetch-local's localSlug keeps of a
	// bundle name (internal/core/fetch); it is unexported there.
	replaySlugMax = 48
	// replayMaxReviews is the ceiling --max-reviews may not pass: 99 is the
	// widest candidate index [runIDMax] budgets for.
	replayMaxReviews = 99
	// replayIndexWidth is [replayMaxReviews] in digits: the widest candidate
	// index a bundle name can carry.
	replayIndexWidth = len("99")
	// runIDMax is the longest --run id either verb accepts. A bundle is named
	// `bt-<n>-<run>` and fetch-local keeps only the first [replaySlugMax] bytes
	// of it, so a longer id would let two runs sharing a prefix land on one
	// bundle: the id carries what the frame leaves of the cap.
	runIDMax          = replaySlugMax - len(replayBundlePrefix) - replayIndexWidth - len("-")
	replayRepo        = "local"
	replayPRsDir      = "prs"
	replayFile        = "replay.json"
	findingsFile      = "findings.json"
	replayPRWidth     = 3
	worktreePrefix    = ".worktree-bt-"
	defaultMaxReviews = 10
	defaultReplayTO   = "300"
	defaultRounds     = "1"
	priceFormat       = "$%.2f"
	labelSep          = ": "
	floatBits         = 64
	gitWorktree       = "worktree"
	gitWorktreeAdd    = "add"
	gitWorktreeRemove = "remove"
	gitWorktreePrune  = "prune"
	flagForce         = "--force"
	flagDetach        = "--detach"
)

// Replay statuses and stop reasons. A status is `ok` for a review that was
// recorded into findings.json, otherwise the refusal code that stopped that
// PR's replay (bundle-failed when the nested service reported a seam error
// rather than a refusal), so a scoring pass skips exactly the rows that
// produced nothing.
const (
	statusOK           = "ok"
	statusBundleFailed = "bundle-failed"
	stopComplete       = "complete"
	stopMaxReviews     = "max-reviews"
	stopBudgetUSD      = "budget-usd"
)

// msgReplayCanceled is the frozen replay cancel text.
const msgReplayCanceled = "canceled before backtest replay completed"

// Replay's shared refusal codes: the run artifacts are a bundle of stored
// inputs, and a timeout is the launcher's own contract.
const (
	codeMissingBundle = "missing-bundle"
	codeBadTimeout    = "bad-timeout"
)

// replayStopCodes end the whole replay instead of one PR: the agent cannot run
// at all, so every later candidate would fail the same way. Every other
// refusal is that PR's own outcome and the run continues.
var replayStopCodes = []string{
	"agent-missing", "agent-spawn-failed", "unknown-agent", "bad-flags", "bad-timeout", codeCanceled,
}

// ReplayRequest is one `backtest replay` invocation: the injected runtime, the
// operator's agent and its pass-through arguments, the budgets, and the
// positionals the run artifacts live under.
type ReplayRequest struct { //nolint:govet // field grouping documents the request surface
	Runtime       *core.Runtime
	LookPath      func(string) (string, error)
	Effective     core.Effective
	PassThrough   []string
	Environ       []string
	RepoRoot      string
	Root          string
	Run           string
	Agent         string
	MaxReviewsRaw string
	BudgetRaw     string
	TimeoutRaw    string
	RoundsRaw     string
	SettingsPath  string
	JSONMode      bool
	Warnings      []string
}

// ReplayResponse carries the replay envelope inputs and the accumulated
// warnings.
type ReplayResponse struct { //nolint:govet // field grouping documents the response surface
	Data     *ReplayData
	Warnings []string
	Next     string
	At       string
}

// ReplayData is the replay mode's envelope data object, in published key
// order.
type ReplayData struct {
	Run        string        `json:"run"`
	Mode       string        `json:"mode"`
	Agent      string        `json:"agent"`
	Budgets    ReplayBudgets `json:"budgets"`
	StopReason string        `json:"stop_reason"`
	PRs        []ReplayPR    `json:"prs"`
}

// ReplayBudgets is the resolved budget block: the caps the operator set, the
// wall clock the run gave each review, and the spend the run could price.
// SpentUSD is a lower bound when Unpriced is non-zero, and null when no
// completed review reported a price — unknown, never zero.
type ReplayBudgets struct { //nolint:govet // field order is the published property order
	MaxReviews int      `json:"max_reviews"`
	BudgetUSD  *float64 `json:"budget_usd"`
	TimeoutS   int      `json:"timeout_s"`
	SpentUSD   *float64 `json:"spent_usd"`
	Unpriced   int      `json:"unpriced_reviews"`
}

// ReplayPR is one row of the replay table: the candidate's position in the
// newest-first list, its PR number (null when the miner recovered none), the
// bundle the replay built, the outcome, and the agent-reported spend.
type ReplayPR struct { //nolint:govet // field order is the published property order
	PR      *int     `json:"pr"`
	CostUSD *float64 `json:"cost_usd"`
	N       int      `json:"n"`
	Status  string   `json:"status"`
	Bundle  string   `json:"bundle"`
}

// Wire renders the data object in published key order.
func (d *ReplayData) Wire() *jq.Obj {
	prs := make([]any, 0, len(d.PRs))
	for i := range d.PRs {
		prs = append(prs, d.PRs[i].wire())
	}
	return jq.NewObj(
		"run", d.Run,
		"mode", d.Mode,
		"agent", d.Agent,
		"budgets", d.Budgets.wire(),
		"stop_reason", d.StopReason,
		"prs", prs,
	)
}

// wire renders one table row.
func (p *ReplayPR) wire() *jq.Obj {
	return jq.NewObj(
		"n", p.N,
		"pr", optionalInt(p.PR),
		"status", p.Status,
		"bundle", p.Bundle,
		"cost_usd", optionalFloat(p.CostUSD),
	)
}

// wire renders the budget block.
func (b *ReplayBudgets) wire() *jq.Obj {
	return jq.NewObj(
		"max_reviews", b.MaxReviews,
		"budget_usd", optionalFloat(b.BudgetUSD),
		"timeout_s", b.TimeoutS,
		"spent_usd", optionalFloat(b.SpentUSD),
		"unpriced_reviews", b.Unpriced,
	)
}

// ReplayPRDoc is `.verdict/backtest/<run>/prs/<NNN>/replay.json`: the resume
// record for one candidate. It carries the agent and pass-through arguments so
// a rerun with another agent refuses instead of overwriting a priced review.
type ReplayPRDoc struct { //nolint:govet // field order is the published property order
	SchemaVersion string   `json:"schema_version"`
	Run           string   `json:"run"`
	Head          string   `json:"head"`
	Base          string   `json:"base"`
	Agent         string   `json:"agent"`
	Status        string   `json:"status"`
	Bundle        string   `json:"bundle,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	At            string   `json:"at"`
	PR            *int     `json:"pr"`
	CostUSD       *float64 `json:"cost_usd"`
	N             int      `json:"n"`
	PassThrough   []string `json:"pass_through"`
}

// MarkerDoc is the bundle marker a replay leaves in the bundle it built. It is
// the one guard the history rollups read: a marked bundle is benchmark input,
// never the operator's own review.
type MarkerDoc struct { //nolint:govet // field order is the published property order
	SchemaVersion string `json:"schema_version"`
	Mode          string `json:"mode"`
	Run           string `json:"run"`
	Head          string `json:"head"`
	Base          string `json:"base"`
	Agent         string `json:"agent"`
	PR            *int   `json:"pr"`
	N             int    `json:"n"`
}

// RunReplay executes `backtest replay`: for each mined candidate, newest first,
// build a local bundle at the candidate's own head and base, launch the
// operator's agent against a detached checkout of that head, record the review,
// and price it against the budgets. It never chooses a model: the agent name
// and everything after `--` are the operator's.
func RunReplay(ctx context.Context, req *ReplayRequest) (resp *ReplayResponse, ref *core.RefusalError) {
	st := &state{rt: req.Runtime, cancelMsg: msgReplayCanceled, warnings: append([]string{}, req.Warnings...)}
	defer st.carryReplay(&resp, &ref)
	if ref := st.canceled(ctx); ref != nil {
		return nil, ref
	}
	run, ref := replayRunID(req.Run)
	if ref != nil {
		return nil, ref
	}
	budgets, ref := parseReplayBudgets(req)
	if ref != nil {
		return nil, ref
	}
	if ref := replayAgent(req.Agent); ref != nil {
		return nil, ref
	}
	st.trust = st.readTrust(ctx)
	root, ref := st.resolveRoot(ctx, req.RepoRoot)
	if ref != nil {
		return nil, ref
	}
	st.root = root
	candidates, ref := st.replayCandidates(run)
	if ref != nil {
		return nil, ref
	}
	data, ref := st.replayAll(ctx, req, run, candidates, budgets)
	if ref != nil {
		return nil, ref
	}
	st.humanReplay(data)
	return &ReplayResponse{
		Data:     data,
		Warnings: st.warnings,
		Next: "inspect .verdict/backtest/" + run + "/prs/<NNN>/replay.json; " +
			"scoring against the later fix lands in a later slice",
		At: st.timeUTC(),
	}, nil
}

// carryReplay replays the warnings onto the response or the refusal.
func (st *state) carryReplay(resp **ReplayResponse, ref **core.RefusalError) {
	if *ref != nil {
		(*ref).Warnings = st.warnings
	}
	if *resp != nil {
		(*resp).Warnings = st.warnings
	}
}

// replayRunID validates the --run id: replay needs one, and it is the same
// shared rule the miner applies.
func replayRunID(run string) (string, *core.RefusalError) {
	if run == "" {
		return "", &core.RefusalError{Code: codeBadFlags, Message: "backtest replay needs --run <id> (the run 'backtest mine' wrote)"}
	}
	if ref := runIDSlug(run); ref != nil {
		return "", ref
	}
	return run, nil
}

// replayAgent refuses an absent or unknown agent before any bundle is built: a
// typo must not leave one marked bundle per candidate behind.
func replayAgent(agent string) *core.RefusalError {
	if agent == "" {
		return &core.RefusalError{
			Code:    codeBadFlags,
			Message: "backtest replay needs --agent <name> (known: " + strings.Join(runcore.AgentNames(), ", ") + ")",
		}
	}
	if !slices.Contains(runcore.AgentNames(), agent) {
		return &core.RefusalError{Code: "unknown-agent", Message: fmt.Sprintf(
			"unknown agent '%s' (known: %s)", agent, strings.Join(runcore.AgentNames(), ", "))}
	}
	return nil
}

// replayCandidates reads the mined candidate list, newest first: the newest
// merge is the one a reviewer would look at first, and the resume index is
// positional, so the order must not depend on how mine.json was sorted.
func (st *state) replayCandidates(run string) ([]Candidate, *core.RefusalError) {
	path := filepath.Join(st.root, artifactDir, run, "mine.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, st.refuse(codeMissingBundle, fmt.Sprintf(
			"backtest run '%s' has no mine.json — mine it first (verdict backtest mine --since <span> --run %s)",
			run, run))
	}
	if err != nil {
		return nil, st.refuse(codeBacktestFail, "cannot read "+display(st.root, path)+labelSep+err.Error())
	}
	var doc MineDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, st.refuse(codeCorruptStore, "unreadable "+display(st.root, path)+"; re-mine the run")
	}
	candidates := slices.Clone(doc.Candidates)
	slices.SortFunc(candidates, compareNewest)
	return candidates, nil
}

// compareNewest publishes candidates newest merge first, then by head so the
// order is total: the reverse of the miner's own published order.
//
//nolint:gocritic // slices.SortFunc's comparator takes value rows
func compareNewest(a, b Candidate) int {
	return -compareCandidates(a, b)
}

// replayBudgets is the resolved spend and clock plan one replay runs under.
type replayBudgets struct {
	budgetUSD  *float64
	timeoutRaw string
	roundsRaw  string
	maxReviews int
	timeout    int
}

// parseReplayBudgets parses the three budgets and the pass-through round count.
func parseReplayBudgets(req *ReplayRequest) (replayBudgets, *core.RefusalError) {
	out := replayBudgets{timeoutRaw: req.TimeoutRaw, roundsRaw: req.RoundsRaw}
	if out.timeoutRaw == "" {
		out.timeoutRaw = defaultReplayTO
	}
	if out.roundsRaw == "" {
		out.roundsRaw = defaultRounds
	}
	capped, ref := positiveInt(req.MaxReviewsRaw, defaultMaxReviews, codeBadFlags,
		"--max-reviews must be a positive whole number, got '%s'")
	if ref != nil {
		return out, ref
	}
	if capped > replayMaxReviews {
		return out, &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf(
			"--max-reviews must be at most %d: a `bt-<n>-<run>` name has room for %d digits, got %d",
			replayMaxReviews, replayIndexWidth, capped)}
	}
	out.maxReviews = capped
	timeout, ref := positiveInt(out.timeoutRaw, 0, codeBadTimeout,
		"timeout must be a positive number of seconds, got '%s'")
	if ref != nil {
		return out, ref
	}
	out.timeout = timeout
	if ref := replayRounds(out.roundsRaw); ref != nil {
		return out, ref
	}
	budget, ref := replayBudgetUSD(req.BudgetRaw)
	if ref != nil {
		return out, ref
	}
	out.budgetUSD = budget
	return out, nil
}

// replayRounds applies run's --rounds range here, before any worktree or
// bundle exists: the launcher would refuse the same value, but by then the
// marker and the bundle are already written.
func replayRounds(raw string) *core.RefusalError {
	n, err := strconv.Atoi(raw)
	if err != nil || n < runcore.MinRounds || n > runcore.MaxRounds || !digitsOnly(raw) {
		return &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf(
			"--rounds must be %d-%d, got '%s'", runcore.MinRounds, runcore.MaxRounds, raw)}
	}
	return nil
}

// positiveInt parses one positive count, falling back to dflt for an empty
// value.
func positiveInt(raw string, dflt int, code, msg string) (int, *core.RefusalError) {
	if raw == "" {
		return dflt, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || !digitsOnly(raw) {
		return 0, &core.RefusalError{Code: code, Message: fmt.Sprintf(msg, raw)}
	}
	return n, nil
}

// digitsOnly reports whether s is a run of ASCII digits, so "+5"/" 5"/"5.0"
// never parse as a count.
func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// replayBudgetUSD parses --budget-usd: absent reads no ceiling, and anything
// that is not a positive finite dollar amount refuses (a budget of zero would
// read as "already spent", which is not what an operator means).
func replayBudgetUSD(raw string) (*float64, *core.RefusalError) {
	if raw == "" {
		return nil, nil
	}
	usd, err := strconv.ParseFloat(raw, floatBits)
	if err != nil || usd <= 0 || math.IsNaN(usd) || math.IsInf(usd, 0) {
		return nil, &core.RefusalError{
			Code:    codeBadFlags,
			Message: "--budget-usd must be a positive dollar amount, got '" + raw + "'",
		}
	}
	return &usd, nil
}

// replaySession is one replay in progress: the accumulated rows, the prices
// that can project the next review's cost, and the resolved plan.
type replaySession struct {
	st      *state
	req     *ReplayRequest
	run     string
	prs     []ReplayPR
	priced  []float64
	budgets replayBudgets
}

// replayAll walks the candidate list until a budget or the review cap stops
// it, recording one row per candidate that reached a decision.
func (st *state) replayAll(
	ctx context.Context, req *ReplayRequest, run string, candidates []Candidate, budgets replayBudgets,
) (*ReplayData, *core.RefusalError) {
	s := &replaySession{st: st, req: req, run: run, budgets: budgets}
	data := &ReplayData{
		Run: run, Mode: modeReplay, Agent: req.Agent, StopReason: stopComplete,
		Budgets: ReplayBudgets{MaxReviews: budgets.maxReviews, BudgetUSD: budgets.budgetUSD, TimeoutS: budgets.timeout},
	}
	st.humanLine(replayHeader(data))
	reason, ref := s.walk(ctx, candidates)
	if ref != nil {
		return nil, ref
	}
	data.StopReason = reason
	data.PRs = s.prs
	data.Budgets.SpentUSD, data.Budgets.Unpriced = s.spend()
	return data, nil
}

// walk replays the candidates in order until the review cap, the budget, a
// cancellation, or a whole-run refusal stops it, returning the stop reason.
func (s *replaySession) walk(ctx context.Context, candidates []Candidate) (string, *core.RefusalError) {
	for i := range candidates {
		if ref := s.st.canceled(ctx); ref != nil {
			return "", ref
		}
		if reason, stop := s.stop(); stop {
			return reason, nil
		}
		entry, ref := s.one(ctx, i+1, &candidates[i])
		if ref != nil {
			return "", ref
		}
		s.prs = append(s.prs, entry)
		s.st.humanLine(replayLine(&entry))
	}
	return stopComplete, nil
}

// stop reports whether the budgets end the walk before the next candidate.
func (s *replaySession) stop() (string, bool) {
	if len(s.prs) >= s.budgets.maxReviews {
		return stopMaxReviews, true
	}
	if s.overBudget() {
		return stopBudgetUSD, true
	}
	return "", false
}

// one replays a single candidate: a recorded row is reported from its resume
// record, and the rest go through [replaySession.replay]. A nil refusal is
// that PR's row; a non-nil one stops the whole run.
func (s *replaySession) one(ctx context.Context, n int, c *Candidate) (ReplayPR, *core.RefusalError) {
	doc, complete, ref := s.stored(n, c)
	if ref != nil {
		return ReplayPR{}, ref
	}
	if complete {
		return s.settle(ReplayPR{N: n, PR: c.PR, Status: doc.Status, Bundle: doc.Bundle, CostUSD: doc.CostUSD}), nil
	}
	return s.replay(ctx, n, c)
}

// replay checks out the candidate's head, builds its bundle, and reviews it
// there. The checkout is removed however the review it reaches ends; a
// checkout git cannot create is this candidate's failure, never the walk's.
func (s *replaySession) replay(ctx context.Context, n int, c *Candidate) (ReplayPR, *core.RefusalError) {
	wt, status, detail, ref := s.st.worktreeAdd(ctx, s.run, n, c.Head)
	if ref != nil {
		return ReplayPR{}, ref
	}
	if status != "" {
		return s.failed(n, c, "", status, detail)
	}
	defer s.st.worktreeRemove(ctx, wt)
	dir, status, detail, ref := s.bundle(ctx, n, c)
	if ref != nil {
		return ReplayPR{}, ref
	}
	if status != "" {
		return s.failed(n, c, "", status, detail)
	}
	entry := ReplayPR{N: n, PR: c.PR, Status: statusOK, Bundle: readcommon.BundleName(dir)}
	if ref := s.st.writeMarker(dir, s.run, n, c, s.req.Agent); ref != nil {
		return ReplayPR{}, ref
	}
	return s.reviewed(ctx, n, c, dir, wt, entry)
}

// reviewed launches the agent against the historical checkout, prices what it
// reported, and records the review.
func (s *replaySession) reviewed(
	ctx context.Context, n int, c *Candidate, dir, wt string, entry ReplayPR,
) (ReplayPR, *core.RefusalError) {
	status, detail, ref := s.review(ctx, wt, entry.Bundle)
	if ref != nil {
		return ReplayPR{}, ref
	}
	if status != statusOK {
		return s.failed(n, c, entry.Bundle, status, detail)
	}
	entry.CostUSD = bundleCost(dir)
	if ref := s.record(ctx, entry.Bundle); ref != nil {
		// A cancel belongs to the walk, not to this row: the row would name the
		// record step and every later candidate would stop anyway.
		if ref.Code == codeCanceled {
			return ReplayPR{}, ref
		}
		return s.failed(n, c, entry.Bundle, ref.Code, ref.Message)
	}
	if ref := s.st.writePR(s.run, n, s.prDoc(n, c, entry.Bundle, statusOK, "", entry.CostUSD)); ref != nil {
		return ReplayPR{}, ref
	}
	return s.settle(entry), nil
}

// failed renders one PR that produced no record, keeping its bundle for
// inspection and the resume record for the retry.
func (s *replaySession) failed(n int, c *Candidate, bundle, status, detail string) (ReplayPR, *core.RefusalError) {
	if ref := s.st.writePR(s.run, n, s.prDoc(n, c, bundle, status, detail, nil)); ref != nil {
		return ReplayPR{}, ref
	}
	return ReplayPR{N: n, PR: c.PR, Status: status, Bundle: bundle}, nil
}

// prDoc renders one candidate's resume record.
func (s *replaySession) prDoc(n int, c *Candidate, bundle, status, detail string, cost *float64) *ReplayPRDoc {
	return &ReplayPRDoc{
		SchemaVersion: schemaVersion,
		Run:           s.run,
		N:             n,
		PR:            c.PR,
		Head:          c.Head,
		Base:          c.Base,
		Agent:         s.req.Agent,
		PassThrough:   append([]string{}, s.req.PassThrough...),
		Status:        status,
		Bundle:        bundle,
		CostUSD:       cost,
		Detail:        detail,
		At:            s.st.timeUTC(),
	}
}

// settle records one completed row and its price for the next projection.
func (s *replaySession) settle(entry ReplayPR) ReplayPR {
	if entry.Status == statusOK && entry.CostUSD != nil {
		s.priced = append(s.priced, *entry.CostUSD)
	}
	return entry
}

// stored reads one candidate's resume record. complete is true only for a row
// whose review was recorded and whose bundle still carries it: a row that
// stopped short, or one whose bundle or findings.json is gone, is replayed
// again. A row recorded by another agent, other pass-through arguments, or
// another resolution refuses run-mismatch rather than silently rewriting it.
func (s *replaySession) stored(n int, c *Candidate) (*ReplayPRDoc, bool, *core.RefusalError) {
	var doc ReplayPRDoc
	raw, err := os.ReadFile(s.st.prPath(s.run, n))
	if errors.Is(err, fs.ErrNotExist) {
		return &doc, false, nil
	}
	if err != nil {
		return nil, false, s.st.refuse(codeBacktestFail, "cannot read "+s.st.displayPR(s.run, n)+labelSep+err.Error())
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, s.st.refuse(codeCorruptStore, "unreadable "+s.st.displayPR(s.run, n)+"; delete it to replay this PR again")
	}
	if ref := s.mismatch(&doc, c, n); ref != nil {
		return nil, false, ref
	}
	return &doc, doc.Status == statusOK && s.kept(&doc), nil
}

// kept reports whether the row's bundle still holds the findings.json its
// resume record claims: a deleted or pruned bundle is replayed, never reported
// as a priced review the operator cannot score.
func (s *replaySession) kept(doc *ReplayPRDoc) bool {
	if doc.Bundle == "" {
		return false
	}
	dir, ref := readcommon.ResolveBundleDir(s.st.root, doc.Bundle)
	if ref != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(filepath.FromSlash(dir), findingsFile))
	return err == nil && info.Mode().IsRegular()
}

// mismatch refuses a resume whose stored agent, pass-through arguments, or
// candidate resolution differ from this invocation: the earlier row priced a
// review this run would not reproduce.
func (s *replaySession) mismatch(doc *ReplayPRDoc, c *Candidate, n int) *core.RefusalError {
	if doc.Agent == s.req.Agent && slices.Equal(doc.PassThrough, s.req.PassThrough) &&
		doc.Head == c.Head && doc.Base == c.Base {
		return nil
	}
	return s.st.refuse(codeRunMismatch, fmt.Sprintf(
		"run '%s' PR %d was replayed as %s..%s with agent '%s'; re-run that way, or delete %s",
		s.run, n, short(doc.Base), short(doc.Head), doc.Agent, s.st.displayRun(s.run)))
}

// spend renders the spend the run could price: the sum of the priced reviews,
// null when no review was priced, and the count of reviews that reported
// nothing so the total reads as the lower bound it is.
func (s *replaySession) spend() (*float64, int) {
	unpriced := 0
	for i := range s.prs {
		if s.prs[i].CostUSD == nil {
			unpriced++
		}
	}
	if len(s.priced) == 0 {
		return nil, unpriced
	}
	total := readcommon.Round4(sumPrices(s.priced))
	return &total, unpriced
}

// overBudget reports whether the projected cost of the next review would pass
// the ceiling. The projection is the median of the completed reviews' own
// reported spend: an unpriced review contributes no sample, so an unknown
// price never projects as zero. With no priced sample yet there is nothing to
// project, and only spend that already crossed the ceiling stops the run.
func (s *replaySession) overBudget() bool {
	if s.budgets.budgetUSD == nil {
		return false
	}
	spent := sumPrices(s.priced)
	if spent >= *s.budgets.budgetUSD {
		return true
	}
	projected, ok := medianPrice(s.priced)
	return ok && spent+projected > *s.budgets.budgetUSD
}

// sumPrices adds the priced samples.
func sumPrices(prices []float64) float64 {
	total := 0.0
	for _, price := range prices {
		total += price
	}
	return total
}

// medianPrice is the median of the samples, or ok=false when there are none.
// An even count reads as the mean of the two middle samples.
func medianPrice(prices []float64) (float64, bool) {
	if len(prices) == 0 {
		return 0, false
	}
	sorted := slices.Clone(prices)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid], true
	}
	return (sorted[mid-1] + sorted[mid]) / 2, true
}

// bundleCost reads one finished bundle's agent-reported spend. An absent,
// unreadable, or unpriced record is unknown, never zero.
func bundleCost(dir string) *float64 {
	cost := readcommon.CostRollup(dir).Cost
	if cost == nil {
		return nil
	}
	raw, ok := cost.Get("est_usd")
	if !ok || raw == nil {
		return nil
	}
	usd, ok := readcommon.NumVal(raw)
	if !ok {
		return nil
	}
	return &usd
}

// bundle builds the candidate's bundle through fetch-local's own range mode, so
// the bundle carries exactly what a local review of `base..head` carries, and
// reports the directory the store actually used (the name is slug-capped
// there). A per-PR failure comes back as that row's status and detail; a
// non-nil refusal means the whole run cannot continue.
func (s *replaySession) bundle(ctx context.Context, n int, c *Candidate) (string, string, string, *core.RefusalError) {
	name := replayBundleName(s.run, n)
	resp, err := fetchcore.RunLocal(ctx, s.fetchRequest(name, c))
	if err == nil {
		return s.bundleDir(resp.Data.Dir), "", "", nil
	}
	if ctx.Err() != nil {
		return "", "", "", s.st.refuse(codeCanceled, msgReplayCanceled)
	}
	if _, ok := errors.AsType[*core.MissingToolError](err); ok {
		return "", "", "", s.st.refuse(codeBacktestFail, "backtest replay needs git on PATH: "+err.Error())
	}
	if typed, ok := errors.AsType[*core.RefusalError](err); ok {
		return "", typed.Code, typed.Message, nil
	}
	return "", statusBundleFailed, err.Error(), nil
}

// replayBundleName is one candidate's fetch-local name: the index first, so
// the store's name cap truncates the run tail, never the index.
func replayBundleName(run string, n int) string {
	return replayBundlePrefix + strconv.Itoa(n) + "-" + run
}

// bundleDir resolves the directory the bundle build reported: fetch-local's
// data names it absolutely when the repository root did, and relative to the
// repository root otherwise.
func (s *replaySession) bundleDir(dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(s.st.root, filepath.FromSlash(dir))
}

// fetchRequest is the fetch-local invocation that builds one replay bundle:
// the candidate's own base and head, never HEAD.
func (s *replaySession) fetchRequest(name string, c *Candidate) *core.FetchLocalRequest {
	read := s.readRequest(replayRepo, name)
	return &core.FetchLocalRequest{
		ReadRequest:  read,
		Flags:        core.FetchLocalFlags{RangeSpec: c.Base + ".." + c.Head},
		SettingsPath: s.req.SettingsPath,
		Effective:    s.req.Effective,
		JSONMode:     s.req.JSONMode,
		Human:        io.Discard,
		Diag:         io.Discard,
	}
}

// review launches the operator's agent in the detached checkout of the
// candidate's head and maps the launcher's answer onto this PR's row. A cancel
// keeps the launcher's code but reports the replay verb, the way every other
// replay step does.
func (s *replaySession) review(ctx context.Context, checkout, bundle string) (string, string, *core.RefusalError) {
	resp, ref := runcore.Run(ctx, s.runRequest(bundle, checkout))
	if ref == nil {
		return statusOK, "", nil
	}
	if slices.Contains(replayStopCodes, ref.Code) {
		out := &core.RefusalError{Code: ref.Code, Message: ref.Message}
		if ref.Code == codeCanceled {
			out.Message = msgReplayCanceled
		}
		if resp != nil {
			out.Warnings = resp.Warnings
		}
		return "", "", out
	}
	return ref.Code, ref.Message, nil
}

// runRequest is the run-core invocation: the operator's agent, their
// pass-through arguments, and the historical checkout as the child's cwd.
func (s *replaySession) runRequest(bundle, checkout string) *core.RunRequest {
	return &core.RunRequest{
		ReadRequest: s.readRequest(replayRepo, bundle),
		LookPath:    s.req.LookPath,
		PassThrough: s.req.PassThrough,
		Environ:     s.req.Environ,
		Agent:       s.req.Agent,
		Checkout:    checkout,
		TimeoutRaw:  s.budgets.timeoutRaw,
		RoundsRaw:   s.budgets.roundsRaw,
		JSONMode:    s.req.JSONMode,
	}
}

// record validates and merges the review into findings.json, which is what a
// later scoring pass reads. A record refusal is that PR's row, never the run's,
// except a cancel: the record verb would report it as its own step, so the
// context is read before and after the merge and a cancel is reported as the
// replay's.
func (s *replaySession) record(ctx context.Context, bundle string) *core.RefusalError {
	if cancel := s.st.canceled(ctx); cancel != nil {
		return cancel
	}
	resp, ref := recordcore.Run(ctx, &core.RecordRequest{
		ReadRequest: s.readRequest(replayRepo, bundle),
		Effective:   s.req.Effective,
	})
	if ref == nil {
		s.st.warnAll(resp.Warnings)
		return nil
	}
	if cancel := s.st.canceled(ctx); cancel != nil {
		return cancel
	}
	return ref
}

// readRequest is the read half every nested service shares: the replay's own
// repository root, the local pseudo-repo, and the nested runtime.
func (s *replaySession) readRequest(repo, pr string) core.ReadRequest {
	root := s.req.Root
	return core.ReadRequest{
		Runtime:  s.nestedRuntime(),
		Repo:     repo,
		Pr:       pr,
		Root:     &root,
		RepoRoot: s.st.root,
	}
}

// nestedRuntime is the runtime the nested services run under: the same
// environment, clock, commander, and payload, and a progress observer that
// keeps their warnings while dropping their narration.
func (s *replaySession) nestedRuntime() *core.Runtime {
	rt := &core.Runtime{Progress: replayProgress{st: s.st}}
	if s.st.rt == nil {
		return rt
	}
	rt.Env, rt.Now, rt.PackFS = s.st.rt.Env, s.st.rt.Now, s.st.rt.PackFS
	rt.Commander = rootedCommander{inner: s.st.rt.Commander, dir: s.st.root}
	return rt
}

// rootedCommander runs every nested command in the replay's repository root.
// The nested services shell git from the process cwd, and the replay reads a
// repository that need not be the one the operator's shell sits in.
type rootedCommander struct {
	inner core.Commander
	dir   string
}

// Run forwards one command with the repository root as its cwd unless the
// caller pinned one.
func (c rootedCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if cmd.Dir == "" {
		out := *cmd
		out.Dir = c.dir
		cmd = &out
	}
	return c.inner.Run(ctx, cmd) //nolint:wrapcheck // the seam error passes through by design
}

// LookPath forwards the tool resolution the nested services need. A commander
// that cannot resolve tools reports the tool as missing, which is exactly what
// the nested service's own contract expects of that seam.
func (c rootedCommander) LookPath(tool string) error {
	looker, ok := c.inner.(interface{ LookPath(tool string) error })
	if !ok {
		return &core.MissingToolError{Tool: tool}
	}
	return looker.LookPath(tool) //nolint:wrapcheck // the seam error passes through by design
}

// replayProgress keeps the nested services' warnings and drops their human and
// diagnostic lines: the replay's own table is its output.
type replayProgress struct{ st *state }

func (p replayProgress) Human(_ string) {}

func (p replayProgress) Warn(msg string) { p.st.warn(msg) }

func (p replayProgress) Diag(_ string) {}

// warnAll folds a nested response's warnings into the run's list.
func (st *state) warnAll(warnings []string) {
	for _, warning := range warnings {
		st.warn(warning)
	}
}

// writeMarker lands the bundle marker that keeps the replay bundle out of the
// user's history rollups.
func (st *state) writeMarker(dir, run string, n int, c *Candidate, agent string) *core.RefusalError {
	doc := MarkerDoc{
		SchemaVersion: schemaVersion, Mode: modeReplay, Run: run, N: n, PR: c.PR,
		Head: c.Head, Base: c.Base, Agent: agent,
	}
	path := filepath.Join(dir, readcommon.BacktestMarkerFile)
	if err := atomic.WriteFile(path, []byte(jq.Pretty(doc))); err != nil {
		return st.refuse(codeBacktestFail, "cannot write "+display(st.root, path)+labelSep+err.Error())
	}
	return nil
}

// writePR lands one PR's resume record atomically under prs/<NNN>/.
func (st *state) writePR(run string, n int, doc *ReplayPRDoc) *core.RefusalError {
	dir := st.prDir(run, n)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return st.refuse(codeBacktestFail, "cannot create "+display(st.root, dir)+labelSep+err.Error())
	}
	path := filepath.Join(dir, replayFile)
	if err := atomic.WriteFile(path, []byte(jq.Pretty(doc))); err != nil {
		return st.refuse(codeBacktestFail, "cannot write "+display(st.root, path)+labelSep+err.Error())
	}
	return nil
}

// prDir is one candidate's resume-record directory.
func (st *state) prDir(run string, n int) string {
	return filepath.Join(st.root, artifactDir, run, replayPRsDir, fmt.Sprintf("%0*d", replayPRWidth, n))
}

// prPath is one candidate's resume record.
func (st *state) prPath(run string, n int) string {
	return filepath.Join(st.prDir(run, n), replayFile)
}

// displayPR renders one resume record for an operator-facing message.
func (st *state) displayPR(run string, n int) string {
	return display(st.root, st.prPath(run, n))
}

// displayRun renders one run directory for an operator-facing message.
func (st *state) displayRun(run string) string {
	return display(st.root, filepath.Join(st.root, artifactDir, run))
}

// worktreeAdd materializes the candidate head as a detached checkout beside
// the bundles. A leftover checkout from an interrupted run is dropped first, so
// a retry never trips over it, and a cancel during the add drops the
// half-created tree and its administrative entry before reporting the cancel. A
// checkout git refuses is that candidate's status and detail; only a cancel, a
// missing git, or an uncreatable state directory ends the walk.
func (st *state) worktreeAdd(
	ctx context.Context, run string, n int, head string,
) (string, string, string, *core.RefusalError) {
	state := readcommon.StateDir(st.root)
	if err := os.MkdirAll(state, dirPerm); err != nil {
		return "", "", "", st.refuse(codeBacktestFail, "cannot create "+display(st.root, state)+labelSep+err.Error())
	}
	dir := filepath.ToSlash(filepath.Join(state, worktreePrefix+run+"-"+strconv.Itoa(n)))
	st.worktreeDrop(ctx, dir)
	detail, err := st.worktreeRun(ctx, gitWorktreeAdd, flagDetach, dir, head)
	if ctx.Err() != nil {
		st.worktreeDrop(ctx, dir)
		return "", "", "", st.refuse(codeCanceled, st.cancelText())
	}
	switch {
	case errors.Is(err, errGitMissing):
		return "", "", "", st.refuse(codeBacktestFail, "backtest replay needs git on PATH")
	case err != nil:
		detail = err.Error()
	}
	if detail == "" {
		return dir, "", "", nil
	}
	st.worktreeDrop(ctx, dir)
	return "", codeBacktestFail, "git " + gitWorktree + " " + gitWorktreeAdd + " failed for " + short(head) + labelSep + detail, nil
}

// worktreeRun runs one `git worktree` subcommand in the repository root,
// returning git's one-line failure detail beside the spawn error.
func (st *state) worktreeRun(ctx context.Context, args ...string) (string, error) {
	_, exit, stderr, err := st.git(ctx, st.root, append([]string{gitWorktree}, args...)...)
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return firstLine(stderr), nil
	}
	return "", nil
}

// worktreeDetail flattens worktreeRun's failure into its detail line for the
// best-effort calls: a checkout that cannot be dropped or removed warns, it
// never revokes a review that already recorded.
func (st *state) worktreeDetail(ctx context.Context, args ...string) string {
	detail, err := st.worktreeRun(ctx, args...)
	if err != nil {
		return err.Error()
	}
	return detail
}

// worktreeDrop clears one checkout path whether or not git still knows about
// it: the leftover may be a live worktree, a half-created tree, or a stale
// administrative entry.
func (st *state) worktreeDrop(ctx context.Context, dir string) {
	ctx = context.WithoutCancel(ctx)
	st.worktreeDetail(ctx, gitWorktreeRemove, flagForce, dir)
	_ = os.RemoveAll(dir)
	st.worktreeDetail(ctx, gitWorktreePrune)
}

// worktreeRemove removes the checkout after its review, on success and on
// every failure path (the deferred call detaches from a canceled run's
// context). A checkout that cannot be removed warns with the manual recovery
// instead of failing the run: the review already recorded.
func (st *state) worktreeRemove(ctx context.Context, dir string) {
	ctx = context.WithoutCancel(ctx)
	detail := st.worktreeDetail(ctx, gitWorktreeRemove, flagForce, dir)
	_ = os.RemoveAll(dir)
	pruned := st.worktreeDetail(ctx, gitWorktreePrune)
	if detail == "" && pruned == "" {
		return
	}
	st.warn(fmt.Sprintf("replay worktree %s was not removed cleanly (%s) — remove it with: git %s %s %s %s",
		display(st.root, dir), detail, gitWorktree, gitWorktreeRemove, flagForce, display(st.root, dir)))
}

// humanReplay renders the stop reason, the spend footer, and the not-citable
// line. The header and the per-PR rows already streamed as the run reached
// them.
func (st *state) humanReplay(data *ReplayData) {
	st.humanLine("stopped: " + data.StopReason)
	st.humanLine(replaySpend(&data.Budgets, len(data.PRs)))
	st.humanLine("not citable — SZZ-derived: a replay grades a blame-derived guess, not a verified defect")
}

// replayHeader renders the run's first human line.
func replayHeader(data *ReplayData) string {
	return fmt.Sprintf("backtest replay %s  (%s)", data.Run, replayPlan(data))
}

// replayPlan renders the resolved plan sentence.
func replayPlan(data *ReplayData) string {
	budget := "no budget"
	if data.Budgets.BudgetUSD != nil {
		budget = fmt.Sprintf("budget "+priceFormat, *data.Budgets.BudgetUSD)
	}
	return fmt.Sprintf("agent %s, max %d reviews, %s, timeout %ds",
		data.Agent, data.Budgets.MaxReviews, budget, data.Budgets.TimeoutS)
}

// replayLine renders one PR row.
func replayLine(p *ReplayPR) string {
	label := "—"
	if p.PR != nil {
		label = "#" + strconv.Itoa(*p.PR)
	}
	bundle := p.Bundle
	if bundle == "" {
		bundle = "—"
	}
	cost := "—"
	if p.CostUSD != nil {
		cost = fmt.Sprintf(priceFormat, *p.CostUSD)
	}
	return fmt.Sprintf("  %d  %s  %s  %s  %s", p.N, label, bundle, p.Status, cost)
}

// replaySpend renders the footer's spend line: a priced total, a lower bound
// when some reviews reported nothing, and "unknown" when none did — an
// unpriced review is never reported as zero dollars.
func replaySpend(b *ReplayBudgets, total int) string {
	priced := total - b.Unpriced
	switch {
	case total == 0:
		return "spend: $0.00 (no reviews)"
	case b.Unpriced == 0:
		return fmt.Sprintf("spend: "+priceFormat+" over "+reviewCount(total), *b.SpentUSD)
	case priced == 0:
		return "spend: unknown over " + reviewCount(total) + " (all unpriced)"
	default:
		return fmt.Sprintf("spend: ≥ "+priceFormat+" over "+reviewCount(total)+" (%d unpriced)", *b.SpentUSD, b.Unpriced)
	}
}

// reviewCount renders a review count with its noun.
func reviewCount(n int) string {
	if n == 1 {
		return "1 review"
	}
	return strconv.Itoa(n) + " reviews"
}

// optionalInt renders an optional number as its value or null.
func optionalInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// optionalFloat renders an optional amount as its value or null.
func optionalFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
