package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// Config is one benchmark run's inputs. Everything an operator can vary is
// here; everything a run derives from the corpus stays derived.
type Config struct {
	Render   string
	Now      time.Time
	Summary  io.Writer
	Out      string
	Corpus   string
	Verdict  string
	WorkDir  string
	Agent    string
	Model    string
	Repo     string
	Root     string
	Panel    []string
	Cases    []string
	Timeout  time.Duration
	K        int
	Weights  core.SeverityWeights
	Runs     int
	Loop     int
	Internal bool
}

const (
	// benchRepo is the label every corpus verb takes.
	benchRepo = "acme/bench"
	// reviewRoot is the ignored state home every corpus bundle lands in,
	// and reviewConfig is the tracked typed configuration the panel pins.
	reviewRoot   = ".verdict/state"
	reviewConfig = ".verdict/config.json"
	millis       = 1000.0
	rounding     = 0.5
	dirPerm      = 0o755
	// defaultTimeout bounds one sample, so a wedged agent cannot hold the run.
	defaultTimeout = 15 * time.Minute
)

func (cfg *Config) validate() error {
	if err := cfg.validateSampling(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(cfg.Model) == "":
		return jq.NewErr("-model is required: every bundle is attributed to exactly one model")
	case strings.TrimSpace(cfg.Agent) == "":
		return jq.NewErr("-agent is required: the command that produces a review for one prompt")
	case cfg.Runs < 1:
		return jq.NewErrf("-runs must be at least 1, got %d", cfg.Runs)
	case cfg.Loop < 0:
		return jq.NewErrf("-loop must be at least 1, got %d", cfg.Loop)
	case cfg.Loop > loopMaxRounds:
		return jq.NewErrf("-loop must be at most %d, got %d", loopMaxRounds, cfg.Loop)
	}
	if cfg.Loop == 0 {
		cfg.Loop = 1
	}
	if cfg.Repo == "" {
		cfg.Repo = benchRepo
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if len(cfg.Panel) == 0 {
		cfg.Panel = []string{"bench-" + slug(cfg.Model)}
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	cfg.Now = cfg.Now.UTC()
	return nil
}

// runner holds one run's derived state: the resolved paths, the grader, and
// what each sample measured.
type runner struct {
	started time.Time
	grader  grader
	cfg     *Config
	work    string
	prompts string
	commit  string
	cases   []corpusCase
	scores  []*score
	loops   []loopRecord
	total   int
	dirty   bool
}

// loopRecord keeps one loop outcome with its sample identity.
type loopRecord struct { //nolint:govet // bench order keeps outcome first.
	out    LoopOutcome
	slug   string
	caseID string
	run    int
}

// Run measures one agent over the corpus and writes the publication JSON. It
// moves the process working directory to the run dir, so it runs once per
// process.
func Run(ctx context.Context, cfg *Config) (*Report, error) {
	return runGraded(ctx, cfg, nil)
}

// runGraded is Run with the scorer pinned: a nil grader drives the shipped
// binary, and tests pass a canned one.
func runGraded(ctx context.Context, cfg *Config, g grader) (*Report, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	r, err := newRunner(ctx, cfg, g)
	if err != nil {
		return nil, err
	}
	return r.run(ctx)
}

func newRunner(ctx context.Context, cfg *Config, g grader) (*runner, error) {
	if runtime.GOOS == "windows" {
		return nil, jq.NewErr("the bench runner drives agents through sh(1): run it on a POSIX host")
	}
	resolved := *cfg
	if err := resolvePaths(&resolved); err != nil {
		return nil, err
	}
	work, err := prepareWork(resolved.WorkDir)
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(work); err != nil {
		return nil, jq.NewErrf("chdir %s: %s", work, err)
	}
	if err := copyPrompts(resolved.Root, work); err != nil {
		return nil, err
	}
	if g == nil {
		g = tool{bin: resolved.Verdict, repo: resolved.Repo, root: resolved.Root, work: work}
	}
	r := &runner{cfg: &resolved, work: work, grader: g, started: time.Now()}
	if r.cases, r.total, err = loadCases(resolved.Corpus, resolved.Cases); err != nil {
		return nil, err
	}
	r.commit, r.dirty = repoState(ctx, resolved.Root)
	*cfg = resolved
	return r, r.pinPanel(resolved.Panel)
}

// resolvePaths settles every path against the invocation directory, before a
// chdir invalidates a relative one, then fills the derived defaults.
func resolvePaths(cfg *Config) error {
	if err := absolutize(cfg); err != nil {
		return err
	}
	if cfg.Corpus == "" {
		cfg.Corpus = filepath.Join(cfg.Root, "bench", "corpus")
	}
	if cfg.Verdict == "" {
		cfg.Verdict = filepath.Join(cfg.Root, "bin", "verdict")
	}
	if _, err := os.Stat(cfg.Verdict); err != nil {
		return jq.NewErrf("%s: build it first with `make build`: %s", cfg.Verdict, err)
	}
	if cfg.Out == "" {
		dir := filepath.Join(cfg.Root, "bench", "results")
		if cfg.Internal {
			dir = filepath.Join(dir, "pilot")
		}
		cfg.Out = filepath.Join(dir, runID(cfg)+".json")
	}
	return nil
}

func absolutize(cfg *Config) error {
	paths := []struct {
		path *string
		name string
	}{
		{path: &cfg.Root, name: "-root"},
		{path: &cfg.Corpus, name: "-corpus"},
		{path: &cfg.Verdict, name: "-verdict"},
		{path: &cfg.WorkDir, name: "-workdir"},
		{path: &cfg.Out, name: "-out"},
	}
	for _, p := range paths {
		if *p.path == "" {
			continue
		}
		abs, err := filepath.Abs(*p.path)
		if err != nil {
			return jq.NewErrf("%s: %s", p.name, err)
		}
		*p.path = abs
	}
	var err error
	cfg.Weights, err = rootWeights(cfg.Root)
	return err
}

// prepareWork makes the run dir and refuses to reuse one: trend scores every
// corpus bundle under .verdict/state, so one dir holds one run's bundles.
func prepareWork(work string) (string, error) {
	if work == "" {
		dir, err := os.MkdirTemp("", "verdict-bench-")
		if err != nil {
			return "", jq.NewErrf("run dir: %s", err)
		}
		return dir, nil
	}
	if err := os.MkdirAll(work, dirPerm); err != nil {
		return "", jq.NewErrf("run dir: %s", err)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return "", jq.NewErrf("run dir: %s", err)
	}
	if len(entries) > 0 {
		return "", jq.NewErrf("run dir %s is not empty (%s): give each run its own dir", work, entries[0].Name())
	}
	return work, nil
}

// runID names the run: the UTC date and model an operator reads first.
func runID(cfg *Config) string {
	id := cfg.Now.UTC().Format("2006-01-02") + "-" + slug(cfg.Model)
	if cfg.Internal {
		return id + "-pilot"
	}
	return id
}

// slug reduces a model id to the bundle-name alphabet.
func slug(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	return strings.Trim(sb.String(), "-")
}

// corpusCase is one checked-in manifest plus the bytes every artifact copies
// verbatim.
type corpusCase struct {
	id       string
	manifest []byte
	diff     []byte
}

type manifest struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Bugs          []json.RawMessage `json:"bugs"`
	LineTolerance int               `json:"line_tolerance"`
}

// loadCases reads the corpus and validates what the scoring path needs: a v1
// manifest, an id matching its directory, a diff, and at least one
// ground-truth bug.
func loadCases(dir string, only []string) ([]corpusCase, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, jq.NewErrf("corpus: %s (checked-in cases live under bench/corpus)", err)
	}
	known := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			known = append(known, e.Name())
		}
	}
	cases, err := selectCases(dir, known, only)
	if err != nil {
		return nil, 0, err
	}
	return cases, len(known), nil
}

// selectCases reads the selected manifests and refuses a subset the corpus
// cannot satisfy.
func selectCases(dir string, known, only []string) ([]corpusCase, error) {
	if missing := missingCases(only, known); len(missing) > 0 {
		return nil, jq.NewErrf("unknown case(s) %s: the corpus holds %s", strings.Join(missing, ", "), strings.Join(known, ", "))
	}
	cases, err := readSelected(dir, known, only)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, jq.NewErrf("corpus %s holds no case", dir)
	}
	slices.SortFunc(cases, func(a, b corpusCase) int { return strings.Compare(a.id, b.id) })
	return cases, nil
}

// readSelected reads the cases the operator asked for, in corpus order.
func readSelected(dir string, known, only []string) ([]corpusCase, error) {
	var cases []corpusCase
	for _, id := range known {
		if len(only) > 0 && !slices.Contains(only, id) {
			continue
		}
		c, err := readCase(dir, id)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func readCase(dir, id string) (corpusCase, error) {
	raw, err := os.ReadFile(filepath.Join(dir, id, caseManifestName))
	if err != nil {
		return corpusCase{}, jq.NewErrf("case %s: %s", id, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return corpusCase{}, jq.NewErrf("case %s: %s", id, err)
	}
	switch {
	case m.SchemaVersion != "1":
		return corpusCase{}, jq.NewErrf("case %s: schema_version %q, want \"1\"", id, m.SchemaVersion)
	case m.ID != id:
		return corpusCase{}, jq.NewErrf("case %s: manifest id %q does not match its directory", id, m.ID)
	case m.Kind == "control" && (m.Bugs == nil || len(m.Bugs) != 0):
		return corpusCase{}, jq.NewErrf("case %s: control requires bugs: []", id)
	case len(m.Bugs) == 0 && m.Kind != "control":
		return corpusCase{}, jq.NewErrf("case %s: manifest carries no ground-truth bug", id)
	}
	diff, err := os.ReadFile(filepath.Join(dir, id, fullDiffName))
	if err != nil {
		return corpusCase{}, jq.NewErrf("case %s: %s", id, err)
	}
	return corpusCase{id: id, manifest: raw, diff: diff}, nil
}

func missingCases(only, known []string) []string {
	var missing []string
	for _, id := range only {
		if !slices.Contains(known, id) {
			missing = append(missing, id)
		}
	}
	return missing
}

// pinPanel writes the typed repository panel default every real run reads,
// so a bundle's panel is auditable after the fact. An existing config keeps
// its other keys; the panel default is the only field the bench owns.
func (r *runner) pinPanel(panel []string) error {
	if err := os.MkdirAll(filepath.Dir(reviewConfig), dirPerm); err != nil {
		return jq.NewErrf("panel: %s", err)
	}
	cfg := map[string]any{}
	if raw, err := os.ReadFile(reviewConfig); err == nil {
		if uerr := json.Unmarshal(raw, &cfg); uerr != nil {
			return jq.NewErrf("panel: %s is not JSON: %s", reviewConfig, uerr)
		}
	}
	defaults, ok := cfg["defaults"].(map[string]any)
	if !ok {
		defaults = map[string]any{}
	}
	defaults["panel"], cfg["schema_version"], cfg["defaults"] = panel, "1", defaults
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return jq.NewErrf("panel: %s", err)
	}
	if werr := atomic.WriteFile(reviewConfig, append(body, '\n')); werr != nil {
		return jq.NewErrf("panel: %s", werr)
	}
	return nil
}

// run drives every selected sample, then publishes what the tool scored.
func (r *runner) run(ctx context.Context) (*Report, error) {
	prefix, nonce, err := r.grader.promptPrefix(ctx)
	if err != nil {
		return nil, err
	}
	rep := r.newReport()
	for _, c := range r.cases {
		for index := 1; index <= r.cfg.Runs; index++ {
			r.sample(ctx, rep, c, index, prefix, nonce)
		}
	}
	if err := r.finish(ctx, rep); err != nil {
		return rep, err
	}
	if r.cfg.Summary != nil {
		r.render(r.cfg.Summary, rep)
	}
	return rep, failure(rep)
}

// failure is the run's exit error: the JSON is already written, so a failure
// costs the exit status, never the evidence.
func failure(rep *Report) error {
	if len(rep.Failures) == 0 {
		return nil
	}
	return jq.NewErrf("%d of %d case runs failed: see failures[] in %s", len(rep.Failures), rep.Samples, rep.RunID)
}

// caseRun is one sample's artifacts and its measurement.
type caseRun struct {
	corpus    corpusCase
	dir       string
	slug      string
	promptSHA string
	warnings  []string
	wall      float64
	index     int
}

func (r *runner) paths(c corpusCase, index int) *caseRun {
	s := "corpus-" + c.id + "-" + slug(r.cfg.Model) + "-run" + strconv.Itoa(index)
	return &caseRun{corpus: c, index: index, slug: s, dir: filepath.Join("runs", s)}
}

// sample produces one review, scores it with the shipped binary, and records
// the outcome. A failure keeps the sample's evidence and moves on.
func (r *runner) sample(ctx context.Context, rep *Report, c corpusCase, index int, prefix, nonce string) {
	cr := r.paths(c, index)
	rep.Samples++
	err := r.measure(ctx, cr, prefix, nonce)
	if cr.promptSHA != "" {
		rep.PromptSHA[c.id] = cr.promptSHA
	}
	if err != nil {
		r.fail(rep, cr, err)
	}
}

// measure runs the agent, then the transfer of its output into a scored
// bundle.
func (r *runner) measure(ctx context.Context, cr *caseRun, prefix, nonce string) error {
	if r.cfg.Loop > 1 {
		return r.measureLoop(ctx, cr, prefix, nonce)
	}
	if err := os.MkdirAll(cr.dir, dirPerm); err != nil {
		return jq.NewErrf("sample dir: %s", err)
	}
	source := caseSource(cr.corpus.id, cr.corpus.diff)
	derived, err := toToolFreePrefix(prefix)
	if err != nil {
		return err
	}
	prompt := assemblePrompt(derived, nonce, source, cr.corpus.diff)
	if err := atomic.WriteFile(filepath.Join(cr.dir, promptFile), []byte(prompt)); err != nil {
		return jq.NewErrf("prompt: %s", err)
	}
	cr.promptSHA = hashPrompt(prefix, prompt)
	if err := r.runAgent(ctx, cr); err != nil {
		return err
	}
	return r.score(ctx, cr, source)
}

// measureLoop runs the loop, then records the final valid round only.
func (r *runner) measureLoop(ctx context.Context, cr *caseRun, prefix, nonce string) error {
	if err := os.MkdirAll(cr.dir, dirPerm); err != nil {
		return jq.NewErrf("sample dir: %s", err)
	}
	source := caseSource(cr.corpus.id, cr.corpus.diff)
	lc := loopConfig(r.cfg.Timeout, r.cfg.Loop)
	out, err := r.runLoop(ctx, cr, prefix, nonce, lc, source)
	if err != nil {
		return err
	}
	r.loops = append(r.loops, loopRecord{out: out, slug: cr.slug, caseID: cr.corpus.id, run: cr.index})
	cr.wall = roundMillis(loopTotalWall(&out))
	if out.FinalValid {
		return r.scoreLoop(ctx, cr, source, &out)
	}
	return loopGiveUp(&out)
}

func loopTotalWall(out *LoopOutcome) float64 {
	var wall float64
	for i := range out.Attempts {
		wall += out.Attempts[i].Cost.Wall
	}
	return wall
}

func loopGiveUp(out *LoopOutcome) error {
	if len(out.Attempts) == 0 {
		return jq.NewErr("loop gave up before the first round")
	}
	last := &out.Attempts[len(out.Attempts)-1]
	if last.AdapterError != "" {
		return jq.NewErrf("loop adapter failure in round %d: %s", last.Round, last.AdapterError)
	}
	return jq.NewErrf("loop gave up after %d rounds with %d problems",
		len(out.Attempts), len(last.Dry.Problems))
}

// scoreLoop assembles the final bundle and scores it.
func (r *runner) scoreLoop(ctx context.Context, cr *caseRun, source []byte, out *LoopOutcome) error {
	last := &out.Attempts[len(out.Attempts)-1]
	bundle := filepath.Join(reviewRoot, cr.slug)
	if err := r.writeLoopBundle(ctx, cr, bundle, source, last); err != nil {
		return err
	}
	warnings, err := r.grader.record(ctx, cr.slug)
	if err != nil {
		return err
	}
	cr.warnings = warnings
	stamp, err := readRecordStamp(filepath.Join(bundle, recordJSONName))
	if err != nil {
		return err
	}
	if stamp.Model != r.cfg.Model {
		return jq.NewErrf("bundle is attributed to model %q, want %q (check cost.json models[0])", stamp.Model, r.cfg.Model)
	}
	if err := r.adoptPrompts(stamp.PromptsHash); err != nil {
		return err
	}
	data, err := r.grader.calibrate(ctx, cr.slug)
	if err != nil {
		return err
	}
	return r.collect(cr, data)
}

// writeLoopBundle lays out the final bundle.
func (r *runner) writeLoopBundle(ctx context.Context, cr *caseRun, bundle string, source []byte, last *RoundAttempt) error {
	if err := os.MkdirAll(bundle, dirPerm); err != nil {
		return jq.NewErrf("bundle: %s", err)
	}
	rawCost, err := os.ReadFile(filepath.Join(roundDir(cr, last.Round), costJSONName))
	if err != nil {
		rawCost = nil
	}
	spend, err := normalizeCost(rawCost, r.cfg.Model, cr.wall)
	if err != nil {
		return err
	}
	files := []bundleFile{
		{fullDiffName, cr.corpus.diff},
		{hunksTSVName, []byte(hunks.Snapshot(string(cr.corpus.diff)))},
		{corpusJSONName, cr.corpus.manifest},
		{sourceJSONName, source},
		{panelName, []byte(strings.Join(r.cfg.Panel, nl) + nl)},
		{reviewJSONName, last.ReviewBytes},
		{costJSONName, spend},
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

// runAgent runs the operator's command under the watchdog: the run dir is the
// working directory, the prompt is a file it reads, and a non-zero exit or a
// missing review refuses the sample.
func (r *runner) runAgent(ctx context.Context, cr *caseRun) error {
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	start := time.Now()
	res, err := coreexec.Watchdog{}.Run(runCtx, &core.Command{Argv: []string{"sh", "-c", r.cfg.Agent}, Env: r.agentEnv(cr)})
	cr.wall = roundMillis(time.Since(start).Seconds())
	logPath := filepath.Join(cr.dir, agentLogName)
	if logErr := atomic.WriteFile(logPath, []byte(res.Stdout)); logErr != nil {
		return jq.NewErrf("agent log: %s", logErr)
	}
	if err != nil {
		return jq.NewErrf("agent: %s", err)
	}
	switch {
	case res.TimedOut && res.Decoy:
		return jq.NewErrf("agent exited but left a child process behind (log: %s)", logPath)
	case res.TimedOut:
		return jq.NewErrf("agent timed out after %s (log: %s)", r.cfg.Timeout, logPath)
	case res.ExitCode != 0:
		return jq.NewErrf("agent exit %d: %s (log: %s)", res.ExitCode, excerpt(res.Stdout), logPath)
	}
	if _, err := os.Stat(filepath.Join(cr.dir, reviewJSONName)); err != nil {
		return jq.NewErrf("agent wrote no %s (log: %s)", reviewJSONName, logPath)
	}
	return nil
}

// agentEnv is the agent's contract: where to read the prompt, where to write
// the review and its spend record, and where the checkout is. The agent keeps
// the operator's environment because it authenticates itself.
func (r *runner) agentEnv(cr *caseRun) []string {
	return append(os.Environ(),
		"VERDICT_BENCH_PROMPT="+filepath.Join(cr.dir, promptFile),
		"VERDICT_BENCH_OUT="+filepath.Join(cr.dir, reviewJSONName),
		"VERDICT_BENCH_COST="+filepath.Join(cr.dir, costJSONName),
		"VERDICT_BENCH_CASE="+cr.corpus.id,
		"VERDICT_BENCH_RUN="+strconv.Itoa(cr.index),
		"VERDICT_BENCH_MODEL="+r.cfg.Model,
		"VERDICT_BENCH_ROOT="+r.cfg.Root,
		"VERDICT_BENCH_WORKDIR="+r.work,
	)
}

// score assembles the bundle, records it, and calibrates it. Every number in
// the report is the tool's: the runner only decides what goes in.
func (r *runner) score(ctx context.Context, cr *caseRun, source []byte) error {
	bundle := filepath.Join(reviewRoot, cr.slug)
	if err := r.writeBundle(ctx, cr, bundle, source); err != nil {
		return err
	}
	warnings, err := r.grader.record(ctx, cr.slug)
	if err != nil {
		return err
	}
	cr.warnings = warnings
	stamp, err := readRecordStamp(filepath.Join(bundle, recordJSONName))
	if err != nil {
		return err
	}
	if stamp.Model != r.cfg.Model {
		return jq.NewErrf("bundle is attributed to model %q, want %q (check cost.json models[0])", stamp.Model, r.cfg.Model)
	}
	if err := r.adoptPrompts(stamp.PromptsHash); err != nil {
		return err
	}
	data, err := r.grader.calibrate(ctx, cr.slug)
	if err != nil {
		return err
	}
	return r.collect(cr, data)
}

// adoptPrompts keeps one prompt set per run: a bundle recorded under a
// different prompts_hash means the tree changed mid-run.
func (r *runner) adoptPrompts(hash string) error {
	switch {
	case hash == "":
		return jq.NewErr("record.json carries no prompts_hash")
	case r.prompts == "":
		r.prompts = hash
	case r.prompts != hash:
		return jq.NewErrf("prompts_hash changed mid-run (%s then %s)", r.prompts, hash)
	}
	return nil
}

// collect decodes one calibrate block for the summary and spread, and keeps
// the block verbatim for the publication.
func (r *runner) collect(cr *caseRun, data json.RawMessage) error {
	var cal calibration
	if err := json.Unmarshal(data, &cal); err != nil {
		return jq.NewErrf("calibrate: %s", err)
	}
	if cal.Model != r.cfg.Model {
		return jq.NewErrf("calibrate scored model %q, want %q", cal.Model, r.cfg.Model)
	}
	if !slices.Equal(cal.Panel, r.cfg.Panel) {
		return jq.NewErrf("calibrate scored panel %v, want %v", cal.Panel, r.cfg.Panel)
	}
	r.scores = append(r.scores, &score{
		slug: cr.slug, caseID: cr.corpus.id, run: cr.index,
		cal: cal, wall: cr.wall, warnings: cr.warnings, data: data,
	})
	return nil
}

// fail keeps the sample's evidence out of .verdict/state and names it in the
// report.
func (r *runner) fail(rep *Report, cr *caseRun, err error) {
	if rmErr := os.RemoveAll(filepath.Join(reviewRoot, cr.slug)); rmErr != nil {
		err = jq.NewErrf("%s (could not drop the partial bundle: %s)", err, rmErr)
	}
	rep.Failures = append(rep.Failures, Failure{
		Case: cr.corpus.id, Run: cr.index, Error: err.Error(),
		Evidence: cr.dir, Kind: r.failureKind(cr),
	})
}

// roundMillis keeps the published wall clock readable and diff-stable.
func roundMillis(seconds float64) float64 {
	return float64(int64(seconds*millis+rounding)) / millis
}

func (cfg *Config) validateSampling() error {
	if cfg.K < 1 || cfg.K > cfg.Runs {
		return jq.NewErr("-k must be between 1 and repeats")
	}
	return nil
}

func rootWeights(root string) (core.SeverityWeights, error) {
	raw, err := os.ReadFile(filepath.Join(root, reviewConfig))
	if os.IsNotExist(err) {
		return config.SeverityWeights(nil), nil
	}
	if err != nil {
		return core.SeverityWeights{}, jq.NewErrf("weights: %s", err)
	}
	policy, ref := config.Parse(raw)
	if ref != nil {
		return core.SeverityWeights{}, jq.NewErrf("weights: %s", ref)
	}
	return config.SeverityWeights(policy), nil
}
