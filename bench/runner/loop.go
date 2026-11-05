package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// LoopConfig bounds one sample repair loop.
type LoopConfig struct {
	MaxRounds int
	PerRound  time.Duration
	Total     time.Duration
}

// DryRunResult is one dry-run observation.
type DryRunResult struct { //nolint:govet // bench keeps raw bytes first, not shipped.
	Envelope   []byte
	Normalized []byte
	Problems   []core.Problem
	Repairs    []core.Repair
	Warnings   []string
	Human      string
	Exit       int
	Truncated  int
}

// AttemptCost is one round spend.
type AttemptCost struct {
	EstUSD *float64
	Wall   float64
	HasEst bool
}

// RoundAttempt is one round.
type RoundAttempt struct { //nolint:govet // bench order keeps identity first.
	ReviewBytes  []byte
	Dry          DryRunResult
	Cost         AttemptCost
	PromptSHA    string
	ReviewSHA    string
	AdapterError string
	Round        int
	Valid        bool
	TimedOut     bool
}

// LoopOutcome is one sample loop.
type LoopOutcome struct {
	Attempts       []RoundAttempt
	Added          []string
	Dropped        []string
	Round1Problems []core.Problem
	FinalProblems  []core.Problem
	RoundsToValid  int
	FinalValid     bool
	GiveUp         bool
}

// FixRate counts one code.
type FixRate struct { //nolint:govet // field order is the published property order.
	Code       string   `json:"code"`
	Level      string   `json:"level"`
	Shown      int      `json:"shown"`
	Fixed      int      `json:"fixed"`
	Dropped    int      `json:"dropped"`
	RepairOnly int      `json:"repair_only"`
	Rate       *float64 `json:"rate"`
}

// LoopCost sums attempts.
type LoopCost struct {
	Attempts     int     `json:"attempts"`
	WithEstimate int     `json:"attempts_with_estimate"`
	Usd          float64 `json:"measured_usd"`
	Measured     bool    `json:"measured"`
}

// LoopSummary publishes one loop.
type LoopSummary struct { //nolint:govet // field order is the published property order.
	Slug           string               `json:"slug"`
	Case           string               `json:"case"`
	Run            int                  `json:"run"`
	RoundsToValid  int                  `json:"rounds_to_valid"`
	FinalValid     bool                 `json:"final_valid"`
	GiveUp         bool                 `json:"give_up"`
	ValidFirstTry  bool                 `json:"valid_first_try"`
	Added          []string             `json:"added,omitempty"`
	Dropped        []string             `json:"dropped,omitempty"`
	Round1Problems []core.Problem       `json:"round1_problems,omitempty"`
	FinalProblems  []core.Problem       `json:"final_problems,omitempty"`
	Attempts       []LoopAttemptSummary `json:"attempts"`
}

// LoopAttemptSummary omits raw bytes.
type LoopAttemptSummary struct { //nolint:govet // field order is the published property order.
	Round        int            `json:"round"`
	PromptSHA    string         `json:"prompt_sha256"`
	ReviewSHA    string         `json:"review_sha256,omitempty"`
	ReviewPath   string         `json:"review_path,omitempty"`
	Valid        bool           `json:"valid"`
	AdapterError string         `json:"adapter_error,omitempty"`
	Exit         int            `json:"dry_run_exit"`
	Problems     []core.Problem `json:"problems,omitempty"`
	Truncated    int            `json:"problems_truncated,omitempty"`
	Repairs      []core.Repair  `json:"repairs,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	WallSeconds  float64        `json:"wall_seconds"`
	EstUSD       *float64       `json:"est_usd,omitempty"`
	Measured     bool           `json:"measured"`
}

const (
	loopMaxRounds   = 5
	loopMinPerRound = 60 * time.Second
	feedFile        = "feedback.md"
	roundFmt        = "round-%02d"
	dryFile         = "dryrun.json"
	dryStderrFile   = "dryrun.stderr.txt"
	normFile        = "normalized.json"
	emptyJSON       = "{}\n"
	nl              = "\n"
)

// loopConfig derives budgets.
func loopConfig(total time.Duration, maxRounds int) LoopConfig {
	if maxRounds < 1 {
		maxRounds = 1
	}
	maxRounds = min(maxRounds, loopMaxRounds)
	if total <= 0 {
		total = defaultTimeout
	}
	per := total / time.Duration(maxRounds)
	per = max(per, loopMinPerRound)
	return LoopConfig{MaxRounds: maxRounds, PerRound: per, Total: total}
}

// findingID identifies a finding.
func findingID(path string, line *int, body string) string {
	text := ""
	if line != nil {
		text = strconv.Itoa(*line)
	}
	joined := path + nl + text + nl + body
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

// buildFeedback renders harness feedback.
func buildFeedback(round, maxRounds int, dry *DryRunResult) string {
	remaining := max(maxRounds-round, 0)
	head := fmt.Sprintf("round %d/%d, %d round(s) remaining%s",
		round, maxRounds, remaining, nl)
	parts := []string{head}
	if len(dry.Problems) > 0 {
		if raw, err := json.MarshalIndent(dry.Problems, "", "  "); err == nil {
			parts = append(parts, "problems:"+nl+string(raw)+nl)
		}
		if dry.Truncated > 0 {
			parts = append(parts, fmt.Sprintf("... %d more problem(s) not shown%s",
				dry.Truncated, nl))
		}
	}
	if strings.TrimSpace(dry.Human) != "" {
		parts = append(parts, "harness output:"+nl+
			strings.TrimSpace(dry.Human)+nl)
	}
	const tail = "Fix every error. Warnings do not block record. " +
		"Do not delete a finding to clear a problem; fix the shape. " +
		"Reply with one JSON document only." + "\n"
	parts = append(parts, tail)
	return strings.Join(parts, "")
}

// dryRun invokes record dry-run.
func (t tool) dryRun(ctx context.Context, slug string) (DryRunResult, error) {
	full := []string{t.bin, "record", t.repo, slug, "--dry-run", "--json"}
	res, err := coreexec.Runner{}.Run(ctx, core.Command{Argv: full, Env: toolEnv()})
	if err != nil {
		return DryRunResult{}, jq.NewErrf("record --dry-run: %s", err)
	}
	return parseDryRun(res.Stdout, res.Stderr, res.ExitCode)
}

func parseDryRun(stdout, stderr string, exit int) (DryRunResult, error) {
	out := DryRunResult{Exit: exit, Envelope: []byte(stdout), Human: stderr}
	env, err := decodeDryEnvelope(stdout, stderr)
	if err != nil {
		return DryRunResult{}, err
	}
	applyDryData(&out, env)
	applyDryError(&out, env)
	if exit == 0 && env.Error != nil {
		return DryRunResult{}, jq.NewErr("record --dry-run: exit 0 with error")
	}
	return out, nil
}

func decodeDryEnvelope(stdout, stderr string) (dryEnvelope, error) {
	var env dryEnvelope
	if len(stdout) == 0 {
		return env, nil
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		return dryEnvelope{}, jq.NewErrf("record --dry-run: unreadable: %s", excerpt(stderr))
	}
	return env, nil
}

func applyDryData(out *DryRunResult, env dryEnvelope) {
	out.Warnings = env.Warnings
	if env.Data == nil {
		return
	}
	out.Normalized = bytes.Clone(env.Data.Normalized)
	if string(out.Normalized) == "null" {
		out.Normalized = nil
	}
	out.Repairs = env.Data.Repairs
}

func applyDryError(out *DryRunResult, env dryEnvelope) {
	if env.Error == nil {
		return
	}
	out.Problems = env.Error.Problems
	out.Truncated = env.Error.Truncated
}

type dryEnvelope struct {
	Data *struct {
		Normalized json.RawMessage `json:"normalized"`
		Repairs    []core.Repair   `json:"repairs"`
	} `json:"data"`
	Error *struct {
		Problems  []core.Problem `json:"problems"`
		Truncated int            `json:"problems_truncated"`
	} `json:"error"`
	Warnings []string `json:"warnings"`
}

// roundDir is one round dir.
func roundDir(cr *caseRun, round int) string {
	return filepath.Join(cr.dir, fmt.Sprintf(roundFmt, round))
}

// runLoop drives rounds.
func (r *runner) runLoop(
	ctx context.Context,
	cr *caseRun,
	prefix, nonce string,
	lc LoopConfig,
	source []byte,
) (LoopOutcome, error) {
	totalCtx, cancel := context.WithTimeout(ctx, lc.Total)
	defer cancel()
	var out LoopOutcome
	prior := ""
	for round := 1; round <= lc.MaxRounds; round++ {
		if totalCtx.Err() != nil {
			out.GiveUp = !out.FinalValid
			return out, nil
		}
		next, err := r.runOneRound(totalCtx, cr, prefix, nonce, lc, source, &out, prior, round)
		if err != nil {
			return out, err
		}
		prior = next
		if loopDone(&out) {
			break
		}
	}
	finishOutcome(&out)
	return out, nil
}

func loopDone(out *LoopOutcome) bool {
	if len(out.Attempts) == 0 {
		return false
	}
	last := &out.Attempts[len(out.Attempts)-1]
	if last.AdapterError != "" {
		return true
	}
	return last.Valid
}

func (r *runner) runOneRound(
	ctx context.Context,
	cr *caseRun,
	prefix, nonce string,
	lc LoopConfig,
	source []byte,
	out *LoopOutcome,
	prior string,
	round int,
) (string, error) {
	prompt, roundPath, attempt, err := r.prepareRound(cr, prefix, nonce, prior, source, lc, out, round)
	if err != nil {
		return prior, err
	}
	return r.executeRound(ctx, cr, source, out, prompt, roundPath, attempt, lc, round)
}

func (r *runner) prepareRound(
	cr *caseRun,
	prefix, nonce, prior string,
	source []byte,
	lc LoopConfig,
	out *LoopOutcome,
	round int,
) (string, string, *RoundAttempt, error) {
	prompt := assembleLoopPrompt(prefix, nonce, prior, source, cr.corpus.diff, round, lc.MaxRounds, out.Attempts)
	roundPath := roundDir(cr, round)
	if err := os.MkdirAll(roundPath, dirPerm); err != nil {
		return prior, "", nil, jq.NewErrf("round dir: %s", err)
	}
	if err := atomic.WriteFile(filepath.Join(roundPath, promptFile), []byte(prompt)); err != nil {
		return prior, "", nil, jq.NewErrf("prompt: %s", err)
	}
	sum := sha256.Sum256([]byte(prompt))
	if round == 1 {
		cr.promptSHA = hex.EncodeToString(sum[:])
	}
	attempt := &RoundAttempt{Round: round, PromptSHA: hex.EncodeToString(sum[:])}
	return prompt, roundPath, attempt, nil
}

func (r *runner) executeRound(
	ctx context.Context,
	cr *caseRun,
	source []byte,
	out *LoopOutcome,
	prompt, roundPath string,
	attempt *RoundAttempt,
	lc LoopConfig,
	round int,
) (string, error) {
	feed, prev, err := loopFeedPaths(cr, round, roundPath, out, lc.MaxRounds)
	if err != nil {
		return prompt, err
	}
	raw, timedOut, wall, aerr := r.runLoopAgent(ctx, cr, roundPath, round, feed, prev, lc.PerRound)
	attempt.Cost.Wall = wall
	attempt.TimedOut = timedOut
	if aerr != nil {
		return prompt, r.finishAdapterRound(roundPath, out, attempt, nil, wall, aerr)
	}
	return prompt, r.finishAgentRound(ctx, cr, source, out, roundPath, attempt, raw, wall)
}

func (r *runner) finishAdapterRound(
	roundPath string,
	out *LoopOutcome,
	attempt *RoundAttempt,
	raw []byte,
	wall float64,
	aerr error,
) error {
	r.recordLoopAttemptCost(roundPath, attempt)
	if err := r.writeRoundArtifacts(roundPath, raw, &DryRunResult{}, wall); err != nil {
		return err
	}
	attempt.AdapterError = aerr.Error()
	out.Attempts = append(out.Attempts, *attempt)
	return nil
}

func (r *runner) finishAgentRound(
	ctx context.Context,
	cr *caseRun,
	source []byte,
	out *LoopOutcome,
	roundPath string,
	attempt *RoundAttempt,
	raw []byte,
	wall float64,
) error {
	attempt.ReviewBytes = raw
	sum := sha256.Sum256(raw)
	attempt.ReviewSHA = hex.EncodeToString(sum[:])
	if err := r.stageLoopBundle(ctx, cr, source, raw); err != nil {
		return err
	}
	dry, derr := r.grader.dryRun(ctx, cr.slug)
	if derr != nil {
		return r.finishAdapterRound(roundPath, out, attempt, raw, wall, derr)
	}
	attempt.Dry = dry
	attempt.Valid = dry.Exit == 0
	r.recordLoopAttemptCost(roundPath, attempt)
	if err := r.writeRoundArtifacts(roundPath, raw, &dry, wall); err != nil {
		return err
	}
	out.Attempts = append(out.Attempts, *attempt)
	return nil
}

func loopFeedPaths(cr *caseRun, round int, roundPath string, out *LoopOutcome,
	maxRounds int,
) (string, string, error) {
	if round <= 1 {
		return "", "", nil
	}
	feed := filepath.Join(roundPath, feedFile)
	prev := filepath.Join(roundDir(cr, round-1), reviewJSONName)
	prior := &out.Attempts[len(out.Attempts)-1].Dry
	if err := atomic.WriteFile(feed, []byte(buildFeedback(round-1, maxRounds, prior))); err != nil {
		return "", "", jq.NewErrf("feedback: %s", err)
	}
	return feed, prev, nil
}

func (r *runner) recordLoopAttemptCost(roundPath string, attempt *RoundAttempt) {
	if est, ok := loopCostEstimate(filepath.Join(roundPath, costJSONName)); ok {
		attempt.Cost.EstUSD = &est
		attempt.Cost.HasEst = true
	}
}

func finishOutcome(out *LoopOutcome) {
	setFinalValid(out)
	setRoundsToValid(out)
	setAddedDropped(out)
}

func setFinalValid(out *LoopOutcome) {
	if len(out.Attempts) == 0 {
		out.FinalValid = false
		out.GiveUp = true
		return
	}
	last := &out.Attempts[len(out.Attempts)-1]
	out.FinalValid = last.Valid && last.AdapterError == ""
	out.GiveUp = !out.FinalValid
}

func setRoundsToValid(out *LoopOutcome) {
	if !out.FinalValid {
		return
	}
	for i := range out.Attempts {
		if out.Attempts[i].Valid {
			out.RoundsToValid = out.Attempts[i].Round
			return
		}
	}
}

func setAddedDropped(out *LoopOutcome) {
	if len(out.Attempts) == 0 {
		return
	}
	first := &out.Attempts[0]
	last := &out.Attempts[len(out.Attempts)-1]
	out.Round1Problems = append([]core.Problem(nil), first.Dry.Problems...)
	out.FinalProblems = append([]core.Problem(nil), last.Dry.Problems...)
	added, dropped := diffLoopFindings(first, last)
	out.Added = added
	out.Dropped = dropped
}

// assembleLoopPrompt keeps round 1 identical.
func assembleLoopPrompt(prefix, nonce, prior string, source, diff []byte,
	round, maxRounds int, attempts []RoundAttempt,
) string {
	if round == 1 {
		return assemblePrompt(prefix, nonce, source, diff)
	}
	priorDry := &attempts[len(attempts)-1].Dry
	fb := buildFeedback(round-1, maxRounds, priorDry)
	return prior + fence(nonce, feedFile, []byte(fb))
}

// runLoopAgent invokes agent once.
func (r *runner) runLoopAgent(ctx context.Context, cr *caseRun, roundPath string,
	round int, feed, prev string, perRound time.Duration,
) ([]byte, bool, float64, error) {
	roundCtx, cancel := context.WithTimeout(ctx, perRound)
	defer cancel()
	start := time.Now()
	env := loopAgentEnv(r, cr, roundPath, round, feed, prev)
	res, err := coreexec.Watchdog{}.Run(roundCtx,
		core.Command{Argv: []string{"sh", "-c", r.cfg.Agent}, Env: env})
	wall := roundMillis(time.Since(start).Seconds())
	logPath := filepath.Join(roundPath, agentLogName)
	if werr := atomic.WriteFile(logPath, []byte(res.Stdout)); werr != nil {
		return nil, false, wall, werr
	}
	if err != nil {
		return nil, false, wall, jq.NewErrf("agent: %s", err)
	}
	if res.TimedOut && res.Decoy {
		return nil, true, wall, jq.NewErrf("agent left a child behind (log: %s)", logPath)
	}
	if res.TimedOut {
		return nil, true, wall, jq.NewErrf("agent timed out after %s (log: %s)", perRound, logPath)
	}
	if res.ExitCode != 0 {
		return nil, false, wall, jq.NewErrf("agent exit %d: %s (log: %s)",
			res.ExitCode, excerpt(res.Stdout), logPath)
	}
	raw, rerr := os.ReadFile(filepath.Join(roundPath, reviewJSONName))
	if rerr != nil {
		return nil, false, wall, jq.NewErrf("agent wrote no review (log: %s)", logPath)
	}
	return raw, false, wall, nil
}

func loopAgentEnv(r *runner, cr *caseRun, roundPath string, round int,
	feed, prev string,
) []string {
	env := r.agentEnv(cr)
	env = append(env, "VERDICT_BENCH_ROUND="+strconv.Itoa(round))
	if feed != "" {
		env = append(env, "VERDICT_BENCH_FEEDBACK="+feed)
	}
	if prev != "" {
		env = append(env, "VERDICT_BENCH_PREV="+prev)
	}
	for i, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "VERDICT_BENCH_PROMPT":
			env[i] = "VERDICT_BENCH_PROMPT=" + filepath.Join(roundPath, promptFile)
		case "VERDICT_BENCH_OUT":
			env[i] = "VERDICT_BENCH_OUT=" + filepath.Join(roundPath, reviewJSONName)
		case "VERDICT_BENCH_COST":
			env[i] = "VERDICT_BENCH_COST=" + filepath.Join(roundPath, costJSONName)
		}
	}
	return env
}

// stageLoopBundle stages bundle for dry-run.
func (r *runner) stageLoopBundle(ctx context.Context, cr *caseRun,
	source, review []byte,
) error {
	bundle := filepath.Join(reviewRoot, cr.slug)
	if err := os.MkdirAll(bundle, dirPerm); err != nil {
		return jq.NewErrf("bundle: %s", err)
	}
	panel := strings.Join(r.cfg.Panel, nl) + nl
	files := []bundleFile{
		{fullDiffName, cr.corpus.diff},
		{hunksTSVName, []byte(hunks.Snapshot(string(cr.corpus.diff)))},
		{corpusJSONName, cr.corpus.manifest},
		{sourceJSONName, source},
		{panelName, []byte(panel)},
		{reviewJSONName, review},
	}
	if err := writeFiles(bundle, files); err != nil {
		return err
	}
	base, err := sourceBase(source)
	if err != nil {
		return err
	}
	req := &controls.Request{BundleDir: bundle, BaseSHA: base, AllowHead: true}
	if _, ref := controls.Resolve(ctx, nil, req); ref != nil {
		return jq.NewErrf("controls snapshot: %s", ref)
	}
	return nil
}

// writeRoundArtifacts preserves round bytes.
func (r *runner) writeRoundArtifacts(roundPath string, review []byte,
	dry *DryRunResult, wall float64,
) error {
	if review != nil {
		sum := sha256.Sum256(review)
		shaPath := filepath.Join(roundPath, reviewJSONName+".sha256")
		if err := atomic.WriteFile(shaPath, []byte(hex.EncodeToString(sum[:])+nl)); err != nil {
			return jq.NewErrf("round sha: %s", err)
		}
	}
	if err := writeDryFiles(roundPath, dry); err != nil {
		return err
	}
	return r.writeLoopCost(roundPath, wall)
}

func writeDryFiles(roundPath string, dry *DryRunResult) error {
	envelope := dry.Envelope
	if len(envelope) == 0 {
		envelope = []byte(emptyJSON)
	}
	if err := atomic.WriteFile(filepath.Join(roundPath, dryFile), envelope); err != nil {
		return jq.NewErrf("dryrun: %s", err)
	}
	stderrPath := filepath.Join(roundPath, dryStderrFile)
	if err := atomic.WriteFile(stderrPath, []byte(dry.Human)); err != nil {
		return jq.NewErrf("dryrun stderr: %s", err)
	}
	return writeNormalized(filepath.Join(roundPath, normFile), dry.Normalized)
}

func writeNormalized(path string, raw []byte) error {
	if len(raw) == 0 {
		return atomic.WriteFile(path, []byte(emptyJSON))
	}
	pretty, err := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	if err != nil {
		return atomic.WriteFile(path, raw)
	}
	return atomic.WriteFile(path, append(pretty, nl...))
}

func (r *runner) writeLoopCost(roundPath string, wall float64) error {
	costPath := filepath.Join(roundPath, costJSONName)
	raw, err := os.ReadFile(costPath)
	if err != nil {
		raw = nil
	}
	fixed, nerr := normalizeCost(raw, r.cfg.Model, wall)
	if nerr != nil {
		return nerr
	}
	return atomic.WriteFile(costPath, fixed)
}

// loopCostEstimate reads est_usd.
func loopCostEstimate(path string) (float64, bool) {
	return costEstimate(path)
}

// loopFindingSet prefers normalized.
func loopFindingSet(review, normalized []byte) map[string]bool {
	if set := findingSetFromBytes(normalized); len(set) > 0 {
		return set
	}
	return findingSetFromBytes(review)
}

// findingSetFromBytes parses ids.
func findingSetFromBytes(raw []byte) map[string]bool {
	out := map[string]bool{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	var doc loopComments
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out
	}
	for i := range doc.Comments {
		c := &doc.Comments[i]
		if c.Path == "" && c.Body == "" {
			continue
		}
		out[findingID(c.Path, c.Line, c.Body)] = true
	}
	return out
}

type loopComments struct {
	Comments []loopComment `json:"comments"`
}

type loopComment struct {
	Path string `json:"path"`
	Line *int   `json:"line"`
	Body string `json:"body"`
}

// diffLoopFindings compares sets.
func diffLoopFindings(first, last *RoundAttempt) ([]string, []string) {
	before := loopFindingSet(first.ReviewBytes, first.Dry.Normalized)
	after := loopFindingSet(last.ReviewBytes, last.Dry.Normalized)
	return stringSetDiff(before, after)
}

func stringSetDiff(before, after map[string]bool) ([]string, []string) {
	var added, dropped []string
	for id := range after {
		if !before[id] {
			added = append(added, id)
		}
	}
	for id := range before {
		if !after[id] {
			dropped = append(dropped, id)
		}
	}
	slices.Sort(added)
	slices.Sort(dropped)
	return added, dropped
}

// summarizeLoop converts outcome.
func summarizeLoop(cr *caseRun, out *LoopOutcome) LoopSummary {
	sum := LoopSummary{
		Slug:           cr.slug,
		Case:           cr.corpus.id,
		Run:            cr.index,
		RoundsToValid:  out.RoundsToValid,
		FinalValid:     out.FinalValid,
		GiveUp:         out.GiveUp,
		Added:          out.Added,
		Dropped:        out.Dropped,
		Round1Problems: out.Round1Problems,
		FinalProblems:  out.FinalProblems,
	}
	if len(out.Attempts) > 0 {
		sum.ValidFirstTry = out.Attempts[0].Valid
	}
	for i := range out.Attempts {
		sum.Attempts = append(sum.Attempts, summarizeAttempt(cr, &out.Attempts[i]))
	}
	return sum
}

func summarizeAttempt(cr *caseRun, a *RoundAttempt) LoopAttemptSummary {
	s := LoopAttemptSummary{
		Round:       a.Round,
		PromptSHA:   a.PromptSHA,
		ReviewSHA:   a.ReviewSHA,
		Valid:       a.Valid,
		Exit:        a.Dry.Exit,
		Problems:    a.Dry.Problems,
		Truncated:   a.Dry.Truncated,
		Repairs:     a.Dry.Repairs,
		Warnings:    a.Dry.Warnings,
		WallSeconds: a.Cost.Wall,
		Measured:    a.Cost.HasEst,
		ReviewPath: filepath.Join("runs", cr.slug,
			fmt.Sprintf(roundFmt, a.Round), reviewJSONName),
	}
	if a.AdapterError != "" {
		s.AdapterError = a.AdapterError
		s.Exit = -1
	}
	if a.Cost.HasEst {
		s.EstUSD = a.Cost.EstUSD
	}
	return s
}

// loopCostRollup sums spends.
func loopCostRollup(outcomes []*LoopOutcome) LoopCost {
	var roll LoopCost
	for _, out := range outcomes {
		addOutcomeCost(&roll, out)
	}
	roll.Usd = math.Round(roll.Usd*round6) / round6
	roll.Measured = roll.Attempts > 0 && roll.WithEstimate == roll.Attempts
	return roll
}

func addOutcomeCost(roll *LoopCost, out *LoopOutcome) {
	for i := range out.Attempts {
		a := &out.Attempts[i]
		roll.Attempts++
		if a.Cost.HasEst && a.Cost.EstUSD != nil {
			roll.Usd += *a.Cost.EstUSD
			roll.WithEstimate++
		}
	}
}

// fixRatesFromOutcomes counts resolutions.
func fixRatesFromOutcomes(outcomes []*LoopOutcome) []FixRate {
	byCode := map[string]*fixCounts{}
	for _, out := range outcomes {
		addOutcomeRates(byCode, out)
	}
	return sortedFixRates(byCode)
}

type fixCounts struct { //nolint:govet // bench order keeps counts first.
	shown      int
	fixed      int
	dropped    int
	repairOnly int
	level      string
}

func addOutcomeRates(byCode map[string]*fixCounts, out *LoopOutcome) {
	for i := 0; i+1 < len(out.Attempts); i++ {
		addPairRates(byCode, &out.Attempts[i], &out.Attempts[i+1])
	}
}

func addPairRates(byCode map[string]*fixCounts, cur, next *RoundAttempt) {
	if cur.AdapterError != "" || next.AdapterError != "" {
		return
	}
	nextSet := repairCodes(next.Dry.Repairs)
	nextCodes := problemCodeSet(&next.Dry)
	curIDs := loopFindingSet(cur.ReviewBytes, cur.Dry.Normalized)
	nextIDs := loopFindingSet(next.ReviewBytes, next.Dry.Normalized)
	for i := range cur.Dry.Problems {
		addProblemRate(byCode, &cur.Dry.Problems[i], cur, next, nextSet, nextCodes, curIDs, nextIDs)
	}
}

func addProblemRate(byCode map[string]*fixCounts, p *core.Problem, cur, next *RoundAttempt,
	nextSet, nextCodes map[string]bool, curIDs, nextIDs map[string]bool,
) {
	entry := byCode[p.Code]
	if entry == nil {
		entry = &fixCounts{level: problemLevel(p)}
		byCode[p.Code] = entry
	}
	entry.shown++
	if nextCodes[p.Code] {
		return
	}
	if !findingKept(curIDs, nextIDs) {
		entry.dropped++
		return
	}
	if nextSet[p.Code] {
		entry.repairOnly++
		return
	}
	if cur.ReviewSHA != next.ReviewSHA {
		entry.fixed++
		return
	}
	entry.dropped++
}

func findingKept(curIDs, nextIDs map[string]bool) bool {
	if len(curIDs) == 0 || len(nextIDs) == 0 {
		return len(nextIDs) > 0
	}
	for id := range curIDs {
		if nextIDs[id] {
			return true
		}
	}
	return false
}

func sortedFixRates(byCode map[string]*fixCounts) []FixRate {
	out := make([]FixRate, 0, len(byCode))
	for code, c := range byCode {
		rate := 0.0
		if c.shown > 0 {
			rate = float64(c.fixed) / float64(c.shown)
		}
		cp := rate
		out = append(out, FixRate{
			Code: code, Level: c.level, Shown: c.shown,
			Fixed: c.fixed, Dropped: c.dropped,
			RepairOnly: c.repairOnly, Rate: &cp,
		})
	}
	slices.SortFunc(out, func(a, b FixRate) int {
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		return strings.Compare(a.Level, b.Level)
	})
	return out
}

func repairCodes(repairs []core.Repair) map[string]bool {
	out := map[string]bool{}
	for i := range repairs {
		out[repairs[i].Code] = true
	}
	return out
}

func problemCodeSet(dry *DryRunResult) map[string]bool {
	out := map[string]bool{}
	for i := range dry.Problems {
		out[dry.Problems[i].Code] = true
	}
	return out
}

// problemLevel reports level.
func problemLevel(p *core.Problem) string {
	if p.Level != "" {
		return p.Level
	}
	if lvl := core.ProblemCodeLevel(p.Code); lvl != "" {
		return lvl
	}
	return core.ProblemLevelError
}

// replayLoop recomputes outcome.
func replayLoop(reviews [][]byte, dry func(int, []byte) (DryRunResult, error)) LoopOutcome {
	var out LoopOutcome
	for i := range reviews {
		round := i + 1
		raw := reviews[i]
		sum := sha256.Sum256(raw)
		attempt := RoundAttempt{
			Round:       round,
			ReviewSHA:   hex.EncodeToString(sum[:]),
			ReviewBytes: bytes.Clone(raw),
		}
		dryRes, err := dry(round, raw)
		if err != nil {
			attempt.AdapterError = err.Error()
			out.Attempts = append(out.Attempts, attempt)
			break
		}
		attempt.Dry = dryRes
		attempt.Valid = dryRes.Exit == 0
		out.Attempts = append(out.Attempts, attempt)
		if attempt.Valid {
			out.FinalValid = true
			out.RoundsToValid = round
			break
		}
	}
	finishOutcome(&out)
	return out
}
