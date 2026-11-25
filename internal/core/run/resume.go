package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The launcher's resume family. Both refusals fail closed: nothing is launched
// until the recorded fingerprint and the bound artifacts have been verified.
const (
	// codeCannotResume names a bundle with no recorded execution to continue,
	// or one whose recorded topology this invocation cannot continue.
	codeCannotResume = "cannot-resume"
	// codeResumeDrift names a bundle whose inputs or bound outputs moved since
	// the interrupted run recorded them.
	codeResumeDrift = "resume-drift"
)

// The resume block's wire keys. One constant per key keeps the record, the
// digest, and the reader in step.
const (
	// resumeSchema versions the launcher record's resume block. A record written
	// under another version is a clean "nothing to resume": the fields a build
	// verifies are the fields it wrote.
	resumeSchema      = "3"
	resumeRecordKey   = "resume"
	unionPhase        = "union"
	statusInProgress  = "in-progress"
	statusComplete    = "complete"
	statusPartial     = "partial"
	keyFullDiffSHA256 = "full_diff_sha256"
	keyRunJSONSHA256  = "run_json_sha256"
	keyInputsSHA256   = "inputs_sha256"
	keyConfigSHA256   = "config_sha256"
	keyPromptsHash    = "prompts_hash"
	keyBinaryPrompts  = "binary_prompts_hash"
	keyHeadSHA        = "head_sha"
	keyAgent          = "agent"
	keyAgentSource    = "agent_source"
	keyFinders        = "finders"
	keyFinderSource   = "finder_source"
	keyFingerprint    = "fingerprint"
	keyContextTXT     = "context_txt_sha256"
	keyThreadsJSON    = "threads_json_sha256"
	keyLaunchDef      = "launch_def_sha256"
	keyPassThrough    = "pass_through_sha256"
)

const (
	// resumeArtifactCap bounds one recorded execution artifact a resume reads
	// back. A review or verdicts file is far smaller; the cap only stops a
	// hand-grown one from exhausting memory.
	resumeArtifactCap int64 = 4 << 20
	// maxResumeRecordBytes bounds the launcher record itself. It carries the
	// bound artifact map, so it grows with the bundle, but never to 8 MiB.
	maxResumeRecordBytes int64 = 8 << 20
)

// The launcher keys a resume reads and writes beside the resume block.
const (
	keyExecutionID = "execution_id"
	keyFailures    = "failures"
	keyStatus      = "status"
	keyPhase       = "phase"
)

// resumeFingerprint is the deterministic identity of one two-finder
// execution's inputs: the diff, the captured run state (which seals the base
// controls and the resolved config), the Procedure prompts, the reviewed head,
// the agent, and the launch topology. Every field reuses a digest the bundle
// already carries rather than minting a second one.
type resumeFingerprint struct { //nolint:govet // field order is the published property order
	Fingerprint       string `json:"fingerprint"`
	FullDiffSHA256    string `json:"full_diff_sha256"`
	RunJSONSHA256     string `json:"run_json_sha256"`
	InputsSHA256      string `json:"inputs_sha256"`
	ConfigSHA256      string `json:"config_sha256"`
	PromptsHash       string `json:"prompts_hash"`
	BinaryPromptsHash string `json:"binary_prompts_hash"`
	HeadSHA           string `json:"head_sha"`
	Agent             string `json:"agent"`
	AgentSource       string `json:"agent_source,omitempty"`
	ContextTXTSHA256  string `json:"context_txt_sha256"`
	ThreadsJSONSHA256 string `json:"threads_json_sha256"`
	LaunchDefSHA256   string `json:"launch_def_sha256"`
	PassThroughSHA256 string `json:"pass_through_sha256"`
	Finders           int    `json:"finders"`
	FinderSource      string `json:"finder_source"`
}

// fingerprintRow is one sealed input: the key it is recorded under, the
// operator-facing note a drift earns, and the value compared.
type fingerprintRow struct {
	key   string
	note  string
	value string
}

// rows renders the sealed inputs in one fixed order, so the composite digest
// and the drift comparison cannot disagree about what was compared.
func (f *resumeFingerprint) rows() []fingerprintRow {
	return []fingerprintRow{
		{key: keyFullDiffSHA256, note: "full.diff changed since the interrupted run", value: f.FullDiffSHA256},
		{key: keyRunJSONSHA256, note: runStateFile + " changed since the interrupted run", value: f.RunJSONSHA256},
		{key: keyInputsSHA256, note: "the captured controls changed since the interrupted run", value: f.InputsSHA256},
		{key: keyConfigSHA256, note: "the captured config changed since the interrupted run", value: f.ConfigSHA256},
		{
			key: keyPromptsHash, note: "the bundle's " + keyPromptsHash + " changed since the interrupted run",
			value: f.PromptsHash,
		},
		{
			key: keyBinaryPrompts, note: "this binary's Procedure prompts differ from the interrupted run's",
			value: f.BinaryPromptsHash,
		},
		{key: keyHeadSHA, note: "the reviewed head changed since the interrupted run", value: f.HeadSHA},
		{key: keyAgent, note: "the agent differs from the interrupted run's", value: f.Agent},
		{
			key: keyAgentSource, note: "the agent definition source changed since the interrupted run",
			value: f.AgentSource,
		},
		{key: keyContextTXT, note: "context.txt changed since the interrupted run", value: f.ContextTXTSHA256},
		{key: keyThreadsJSON, note: "threads.json changed since the interrupted run", value: f.ThreadsJSONSHA256},
		{
			key: keyLaunchDef, note: "the agent's launch definition changed since the interrupted run",
			value: f.LaunchDefSHA256,
		},
		{
			key: keyPassThrough, note: "the pass-through arguments differ from the interrupted run's",
			value: f.PassThroughSHA256,
		},
		{key: keyFinders, note: "the finder count differs from the interrupted run's", value: strconv.Itoa(f.Finders)},
		{key: keyFinderSource, note: "the finder source changed since the interrupted run", value: f.FinderSource},
	}
}

// digest is the composite the record carries so two fingerprints compare in one
// string. The rows are ordered, so the same inputs always hash the same way.
func (f *resumeFingerprint) digest() string {
	var sb strings.Builder
	for _, row := range f.rows() {
		sb.WriteString(row.key)
		sb.WriteByte('=')
		sb.WriteString(row.value)
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// driftAgainst names the first input that no longer matches the recorded
// fingerprint, or "" when the two agree. The note is the operator's whole
// diagnosis, so it names the input rather than the digest that moved; the two
// rows whose recorded value is the useful detail carry it too.
func (f *resumeFingerprint) driftAgainst(prior *resumeFingerprint) string {
	now, then := f.rows(), prior.rows()
	for i := range now {
		if now[i].value != then[i].value {
			return driftNote(now[i], then[i].value)
		}
	}
	return ""
}

// driftNote spells one drifted row, adding the recorded value where that value
// is the diagnosis (which agent, how many finders).
func driftNote(row fingerprintRow, recorded string) string {
	switch row.key {
	case keyAgent, keyFinders:
		return row.note + " (" + recorded + ")"
	default:
		return row.note
	}
}

// launcherResume is the launcher record's resume block: the fingerprint the
// run was started from, plus the provenance a later --resume reports.
type launcherResume struct {
	resumeFingerprint
	SchemaVersion string   `json:"schema_version"`
	ExecutionID   string   `json:"execution_id"`
	PriorStatus   string   `json:"prior_status,omitempty"`
	RecordedAt    string   `json:"recorded_at"`
	Reused        []string `json:"reused,omitempty"`
	Executed      []string `json:"executed,omitempty"`
	Attempts      int      `json:"attempts,omitempty"`
	AttemptsValid bool     `json:"-"`
}

// wire renders the block in published key order.
func (r *launcherResume) wire() *jq.Obj {
	obj := jq.NewObj(
		"schema_version", r.SchemaVersion,
		keyExecutionID, r.ExecutionID,
		keyFingerprint, r.Fingerprint,
		keyFullDiffSHA256, r.FullDiffSHA256,
		keyRunJSONSHA256, r.RunJSONSHA256,
		keyInputsSHA256, r.InputsSHA256,
		keyConfigSHA256, r.ConfigSHA256,
		keyPromptsHash, r.PromptsHash,
		keyBinaryPrompts, r.BinaryPromptsHash,
		keyHeadSHA, r.HeadSHA,
		keyAgent, r.Agent,
		keyContextTXT, r.ContextTXTSHA256,
		keyThreadsJSON, r.ThreadsJSONSHA256,
		keyLaunchDef, r.LaunchDefSHA256,
		keyPassThrough, r.PassThroughSHA256,
		keyFinders, r.Finders,
		keyFinderSource, r.FinderSource,
		"recorded_at", r.RecordedAt,
	)
	if r.AgentSource != "" {
		obj.Set(keyAgentSource, r.AgentSource)
	}
	if r.PriorStatus != "" {
		obj.Set("prior_status", r.PriorStatus)
	}
	if r.Attempts > 0 {
		obj.Set("attempts", r.Attempts)
	}
	if len(r.Reused) > 0 {
		obj.Set("reused", stringValues(r.Reused))
	}
	if len(r.Executed) > 0 {
		obj.Set("executed", stringValues(r.Executed))
	}
	return obj
}

// stringValues renders a string slice the jq encoder prints as a JSON array.
func stringValues(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// launcherResumeOf reads the block back, returning nil when the record predates
// --resume or the block is unusable. A legacy record is a clean "nothing to
// resume", never a drift.
func launcherResumeOf(manifest *jq.Obj) *launcherResume {
	block, ok := readcommon.FieldOf(manifest, resumeRecordKey).(*jq.Obj)
	if !ok || jq.StrOf(readcommon.FieldOf(block, "schema_version")) != resumeSchema {
		return nil
	}
	attempts, valid := wholeAttempts(readcommon.FieldOf(block, "attempts"))
	record := &launcherResume{
		SchemaVersion: resumeSchema,
		ExecutionID:   jq.StrOf(readcommon.FieldOf(block, keyExecutionID)),
		PriorStatus:   jq.StrOf(readcommon.FieldOf(block, "prior_status")),
		RecordedAt:    jq.StrOf(readcommon.FieldOf(block, "recorded_at")),
		Attempts:      attempts,
		AttemptsValid: valid,
	}
	record.resumeFingerprint = resumeFingerprint{
		Fingerprint:       jq.StrOf(readcommon.FieldOf(block, keyFingerprint)),
		FullDiffSHA256:    jq.StrOf(readcommon.FieldOf(block, keyFullDiffSHA256)),
		RunJSONSHA256:     jq.StrOf(readcommon.FieldOf(block, keyRunJSONSHA256)),
		InputsSHA256:      jq.StrOf(readcommon.FieldOf(block, keyInputsSHA256)),
		ConfigSHA256:      jq.StrOf(readcommon.FieldOf(block, keyConfigSHA256)),
		PromptsHash:       jq.StrOf(readcommon.FieldOf(block, keyPromptsHash)),
		BinaryPromptsHash: jq.StrOf(readcommon.FieldOf(block, keyBinaryPrompts)),
		HeadSHA:           jq.StrOf(readcommon.FieldOf(block, keyHeadSHA)),
		Agent:             jq.StrOf(readcommon.FieldOf(block, keyAgent)),
		AgentSource:       jq.StrOf(readcommon.FieldOf(block, keyAgentSource)),
		ContextTXTSHA256:  jq.StrOf(readcommon.FieldOf(block, keyContextTXT)),
		ThreadsJSONSHA256: jq.StrOf(readcommon.FieldOf(block, keyThreadsJSON)),
		LaunchDefSHA256:   jq.StrOf(readcommon.FieldOf(block, keyLaunchDef)),
		PassThroughSHA256: jq.StrOf(readcommon.FieldOf(block, keyPassThrough)),
		Finders:           int(readcommon.NumOr(readcommon.FieldOf(block, keyFinders))),
		FinderSource:      jq.StrOf(readcommon.FieldOf(block, keyFinderSource)),
	}
	if record.ExecutionID == "" || record.Fingerprint == "" {
		return nil
	}
	return record
}

// wholeAttempts parses a recorded attempt count without truncating fractional
// numbers: the launcher only ever writes whole integers >= 1.
func wholeAttempts(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v >= 1
	case int64:
		return int(v), v >= 1 && v <= int64(math.MaxInt)
	case json.Number:
		n, err := strconv.Atoi(string(v))
		return n, err == nil && n >= 1
	case float64:
		return wholeFloat(v)
	default:
		return 0, false
	}
}

func wholeFloat(v float64) (int, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
		return 0, false
	}
	if v < 1 || v > float64(math.MaxInt) {
		return 0, false
	}
	return int(v), true
}

// resumePlan is the verified prior execution one --resume invocation
// continues: which phases it may skip, which it must re-run, and the record
// the launcher writes when the run finishes.
type resumePlan struct { //nolint:govet // the fingerprint value sits beside the record fields it names
	manifest     *jq.Obj
	failed       map[string]bool
	finderBytes  [][]byte
	crossBytes   [][]byte
	reuseFinder  []bool
	reuseCross   []bool
	crossPlanned []bool
	crossBatch   []bool
	executionID  string
	priorStatus  string
	finderSource string
	fingerprint  resumeFingerprint
	attempts     int
}

// launchesPlanned reports whether this attempt will spawn an agent, which is
// what a spend authorization is for: a resume that reuses every phase of a
// finished execution spends nothing and no guard has to clear it.
func (p *resumePlan) launchesPlanned() bool {
	if !p.findersReused() {
		return true
	}
	for i := range finderCount {
		if p.crossBatch[i] && !p.reuseCross[i] {
			return true
		}
	}
	return false
}

// reusableFinder reports whether finder index's artifact may be reused.
func (p *resumePlan) reusableFinder(index int) bool { return p.reuseFinder[index] }

// findersReused reports whether every finder artifact survived verification, so
// the union and both cross batches still describe the same findings.
func (p *resumePlan) findersReused() bool {
	for _, reuse := range p.reuseFinder {
		if !reuse {
			return false
		}
	}
	return true
}

// reusedPhases names every phase this invocation skipped, in bundle order.
func (p *resumePlan) reusedPhases() []string {
	out := make([]string, 0, finderCount*2)
	for i := range finderCount {
		if p.reuseFinder[i] {
			out = append(out, finderPhase(i))
		}
	}
	for i := range finderCount {
		if p.crossPlanned[i] && p.reuseCross[i] {
			out = append(out, crossPhase(i))
		}
	}
	return out
}

// executedPhases names every phase this invocation ran, in bundle order. The
// union is always listed: a resumed run re-derives review.json and the plan
// from the phases it kept.
func (p *resumePlan) executedPhases() []string {
	out := make([]string, 0, finderCount*2+1)
	for i := range finderCount {
		if !p.reuseFinder[i] {
			out = append(out, finderPhase(i))
		}
	}
	out = append(out, unionPhase)
	for i := range finderCount {
		if p.crossPlanned[i] && !p.reuseCross[i] {
			out = append(out, crossPhase(i))
		}
	}
	return out
}

// readLauncherObject reads the bundle's launcher record, returning nil when
// there is none. A record that is not a JSON object is the launcher's own
// corruption, which a resume refuses rather than guesses around.
func (c *coordinator) readLauncherObject() (*jq.Obj, *core.RefusalError) {
	record := c.recordPath()
	raw, ref := c.recordBytes(record)
	if ref != nil || raw == nil {
		return nil, ref
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record %s is not readable JSON: %v; re-run without --resume", record, err))
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record %s is not a JSON object; re-run without --resume", record))
	}
	return obj, nil
}

// recordBytes reads the launcher record whole, refusing a path that is not a
// plain file or a record grown past the resume record cap.
func (c *coordinator) recordBytes(record string) ([]byte, *core.RefusalError) {
	path := filepath.Join(c.dir, launcherFile)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"cannot inspect the launcher record %s: %v; re-run without --resume", record, err))
	case !info.Mode().IsRegular():
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record %s is not a plain file; re-run without --resume", record))
	case info.Size() > maxResumeRecordBytes:
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record %s is %d bytes, over the %d-byte record cap; re-run without --resume",
			record, info.Size(), maxResumeRecordBytes))
	}
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record %s cannot be read: %v; re-run without --resume", record, err))
	}
	return raw, nil
}

// planResume verifies the recorded execution against the bundle as it stands
// now and resolves which phases may be reused. It writes nothing and launches
// nothing: every refusal it returns leaves the record the next attempt reads
// exactly as the interrupted run left it, including the bound artifacts and the
// failures it published.
func (c *coordinator) planResume(captured *config.RunState) (*resumePlan, *core.RefusalError) {
	manifest, record, ref := c.readPriorExecution()
	if ref != nil {
		return nil, ref
	}
	if ref := c.checkResumeRecord(manifest, record); ref != nil {
		return nil, ref
	}
	if ref := c.checkResumeCapture(record, captured); ref != nil {
		return nil, ref
	}
	if ref := c.checkPhasePaths(); ref != nil {
		return nil, ref
	}
	source, fingerprint, ref := c.verifyFingerprint(manifest, record, captured)
	if ref != nil {
		return nil, ref
	}
	// The continued execution keeps its own identity, and the finder source the
	// fingerprint just proved is the one the interrupted run recorded.
	c.id, c.source, c.fingerprint = record.ExecutionID, source, fingerprint
	return c.sealedPlan(manifest, record, source, &c.fingerprint)
}

// sealedPlan resolves which phases the interrupted execution may keep. It reads
// and validates only, so every refusal it returns leaves the record intact.
func (c *coordinator) sealedPlan(manifest *jq.Obj, record *launcherResume, source string,
	fingerprint *resumeFingerprint,
) (*resumePlan, *core.RefusalError) {
	reuse, sealed, ref := c.finderReuse(manifest)
	if ref != nil {
		return nil, ref
	}
	plan := &resumePlan{
		fingerprint: *fingerprint, manifest: manifest, failed: failedPhases(manifest),
		reuseFinder: reuse, finderBytes: sealed,
		reuseCross: make([]bool, finderCount), crossPlanned: make([]bool, finderCount),
		crossBatch: make([]bool, finderCount), crossBytes: make([][]byte, finderCount),
		executionID: record.ExecutionID, priorStatus: jq.StrOf(readcommon.FieldOf(manifest, keyStatus)),
		finderSource: source, attempts: record.Attempts + 1,
	}
	// The union this attempt derives reads the reviews of the finders it keeps,
	// so they are decoded from the bytes the plan sealed, once.
	for i := range finderCount {
		if plan.reuseFinder[i] {
			c.reviews[i] = discoveryObject(plan.finderBytes[i])
		}
	}
	if ref := c.validateSealed(plan); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// verifyFingerprint recomputes the recorded fingerprint from this invocation's
// resolved policy and refuses the first input that moved. The interrupted
// execution's review budget becomes this invocation's own, so a resumed run
// reports the depth it continues rather than a depth re-derived halfway.
func (c *coordinator) verifyFingerprint(manifest *jq.Obj, record *launcherResume,
	captured *config.RunState,
) (string, resumeFingerprint, *core.RefusalError) {
	if ref := c.resumeBudget(manifest); ref != nil {
		return "", resumeFingerprint{}, ref
	}
	_, source, ref := c.st.finderPolicy(captured)
	if ref != nil {
		return "", resumeFingerprint{}, ref
	}
	fingerprint, ref := c.fingerprintNow(captured, source)
	if ref != nil {
		return "", resumeFingerprint{}, ref
	}
	if drift := fingerprint.driftAgainst(&record.resumeFingerprint); drift != "" {
		return "", resumeFingerprint{}, c.st.refuse(codeResumeDrift, fmt.Sprintf(
			"cannot resume %s: %s; re-run without --resume to retire the interrupted "+
				"execution and review the bundle as it stands", c.recordPath(), drift))
	}
	return source, fingerprint, nil
}

// resumeBudget keeps the interrupted execution's review-depth decision. A
// --budget flag that resolves to a different decision refuses: the depth is
// part of the invocation the fingerprint sealed, and rewriting it halfway
// would publish a classification the interrupted attempt never made.
func (c *coordinator) resumeBudget(manifest *jq.Obj) *core.RefusalError {
	block, ok := readcommon.FieldOf(manifest, "budget").(*jq.Obj)
	if !ok {
		if c.st.budget != nil {
			return c.st.refuse(codeResumeDrift,
				"the interrupted execution recorded no review budget, and --resume cannot add one "+
					"— re-run without --resume to review at this depth")
		}
		return nil
	}
	carried, err := recordedBudget(block)
	if err != nil {
		return c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record's budget block is unreadable: %v; re-run without --resume", err))
	}
	c.st.recordBudgetCount(finderCount)
	if c.st.budget != nil && !sameBudget(carried, c.st.budget) {
		return c.st.refuse(codeResumeDrift,
			"the requested review budget differs from the interrupted execution's — "+
				"re-pass the depth it recorded, or re-run without --resume to review at this depth")
	}
	c.st.budget = carried
	return nil
}

// sameBudget compares two review-depth decisions by value, so a record whose
// JSON keys were written in another order is still the same decision.
func sameBudget(a, b *core.ReviewBudget) bool {
	return a.Tier == b.Tier && a.Source == b.Source && a.Requested == b.Requested &&
		a.Files == b.Files && a.DiffBytes == b.DiffBytes && a.Finders == b.Finders &&
		a.CrossCheck == b.CrossCheck && a.OverriddenBy == b.OverriddenBy &&
		slices.Equal(a.Reasons, b.Reasons)
}

// recordedBudget decodes one launcher budget block back into the published
// decision type, so the resumed run can carry it as its own.
func recordedBudget(block *jq.Obj) (*core.ReviewBudget, error) {
	budget := &core.ReviewBudget{}
	if err := json.Unmarshal([]byte(block.MarshalCompact()), budget); err != nil {
		return nil, jq.Sealed("decode budget block", err)
	}
	return budget, nil
}

// checkPhasePaths proves every phase directory a resume would continue is a
// plain directory. Lstat is what makes the check meaningful: a symlinked phase
// path would let the resumed run read and write artifacts outside the bundle,
// which rejectPhaseLinks only catches after a phase has already run.
func (c *coordinator) checkPhasePaths() *core.RefusalError {
	for _, name := range []string{finderPhase(0), finderPhase(1), crossPhase(0), crossPhase(1), verificationDir} {
		info, err := os.Lstat(filepath.Join(c.dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return c.st.refuse(codeRecordFailed, fmt.Sprintf("cannot inspect phase path %s: %v", name, err))
		case !info.Mode().IsDir():
			return c.st.refuse(codeResumeDrift, fmt.Sprintf(
				"cannot resume: %s is not a plain directory, so the interrupted execution's "+
					"artifacts cannot be trusted; re-run without --resume", name))
		}
	}
	return nil
}

// priorFailures is the interrupted execution's unresolved failure rows. A
// resumed attempt carries them until it runs each phase again, so a phase it
// cannot vouch for never looks solved just because this attempt has not reached
// it yet.
func priorFailures(manifest *jq.Obj) []any {
	return append([]any{}, jq.Elements(readcommon.FieldOf(manifest, keyFailures))...)
}

// validateSealed proves every artifact this invocation keeps still carries the
// bytes the interrupted execution sealed, and every reusable cross check still
// accepts the batch it would be asked about. Reuse of both finders fixes the
// union and therefore both batches, so the whole verification runs here, in
// memory, before the launcher record, the plan, or a single agent is written.
func (c *coordinator) validateSealed(plan *resumePlan) *core.RefusalError {
	if !plan.findersReused() {
		// A recomputed finder changes the union and both batches, so no recorded
		// check survives that change: every check runs again.
		return nil
	}
	if ref := c.deriveUnion(); ref != nil {
		return ref
	}
	for i := range finderCount {
		if ref := c.sealedCross(plan, i); ref != nil {
			return ref
		}
	}
	return nil
}

// sealedCross validates one cross check against the batch this attempt would
// ask about, and records the bytes the attempt may keep when it validates.
func (c *coordinator) sealedCross(plan *resumePlan, index int) *core.RefusalError {
	checker := fmt.Sprintf("finder-%d", index+1)
	batch := crossBatch(c.plan, c.review, checker)
	plan.crossBatch[index] = len(batch) > 0
	if !plan.crossBatch[index] || plan.failed[crossPhase(index)] {
		return nil
	}
	artifact := filepath.Join(crossPhase(index), "verdicts.json")
	raw, bound, ref := c.recordedArtifact(plan.manifest, artifact)
	if ref != nil || raw == nil || bound == "" {
		return ref
	}
	if _, valid := c.checkCross(raw, checker, batch); !valid {
		return c.driftRefusal(artifact + " no longer passes the cross-check validation")
	}
	plan.crossBytes[index], plan.reuseCross[index] = raw, true
	return nil
}

// seedSealed binds the cross checks this attempt keeps into the snapshot, so
// the first progress write carries them: an attempt killed between the
// discovery barrier and the cross barrier must not drop a binding the
// interrupted record already sealed.
func (c *coordinator) seedSealed() *core.RefusalError {
	if c.resume == nil {
		return nil
	}
	for i := range finderCount {
		if ref := c.seedCross(i); ref != nil {
			return ref
		}
	}
	return nil
}

func (c *coordinator) seedCross(index int) *core.RefusalError {
	if !c.resume.reuseCross[index] {
		return nil
	}
	artifact := filepath.Join(crossPhase(index), "verdicts.json")
	if ref := c.sealedBytes(c.resume.crossBytes[index], artifact); ref != nil {
		return ref
	}
	return c.bindPhase(crossPhase(index))
}

// readPriorExecution reads the recorded manifest and its resume block, each
// missing shape refusing with the remedy that produces one.
func (c *coordinator) readPriorExecution() (*jq.Obj, *launcherResume, *core.RefusalError) {
	manifest, ref := c.readLauncherObject()
	if ref != nil {
		return nil, nil, ref
	}
	if manifest == nil {
		return nil, nil, c.st.refuse(codeCannotResume, fmt.Sprintf(
			"no launcher record at %s, so there is no interrupted run to continue — "+
				"re-run without --resume to start one", c.recordPath()))
	}
	record := launcherResumeOf(manifest)
	if record == nil {
		return nil, nil, c.st.refuse(codeCannotResume,
			"the launcher record carries no resume fingerprint this build can verify "+
				"(it was written by another build) — re-run without --resume to retire it "+
				"and start a new execution")
	}
	return manifest, record, nil
}

// recordPath is the launcher record's display path for the refusal messages.
func (c *coordinator) recordPath() string {
	return c.st.display(filepath.Join(c.dir, launcherFile))
}

// checkResumeRecord refuses a recorded execution whose manifest cannot be
// continued: an unknown status, or a manifest and resume block naming different
// executions.
func (c *coordinator) checkResumeRecord(manifest *jq.Obj, record *launcherResume) *core.RefusalError {
	status := jq.StrOf(readcommon.FieldOf(manifest, keyStatus))
	switch {
	case status == "":
		return c.st.refuse(codeCannotResume,
			"the launcher record names no status, so there is no execution to continue — "+
				"re-run without --resume")
	case status != statusInProgress && status != statusComplete && status != statusPartial:
		return c.st.refuse(codeCannotResume, fmt.Sprintf(
			"the launcher record status %q is not a resumable execution — re-run without --resume", status))
	case record.ExecutionID != jq.StrOf(readcommon.FieldOf(manifest, keyExecutionID)):
		return c.st.refuse(codeCannotResume,
			"the launcher record's resume block names a different execution than its manifest — "+
				"re-run without --resume")
	case !record.AttemptsValid || record.Attempts < 1:
		return c.st.refuse(codeCannotResume,
			"the launcher record's resume block carries an invalid attempt count "+
				"(must be a whole number of at least one) — re-run without --resume")
	}
	return nil
}

// checkResumeCapture refuses a captured run state this invocation cannot
// continue: a lost execution reference, a finder topology other than the
// two-finder shape, or a finder source the record and the capture disagree on.
func (c *coordinator) checkResumeCapture(record *launcherResume, captured *config.RunState) *core.RefusalError {
	switch {
	case captured == nil || captured.Execution == nil:
		return c.st.refuse(codeCannotResume,
			runStateFile+" lost the captured execution reference — re-run without --resume")
	case captured.Execution.ID != record.ExecutionID:
		return c.st.refuse(codeResumeDrift, fmt.Sprintf(
			"%s records execution %q but the launcher record names %q; re-run without --resume",
			runStateFile, captured.Execution.ID, record.ExecutionID))
	case record.Finders != finderCount || captured.Execution.Finders != finderCount:
		return c.st.refuse(codeCannotResume, fmt.Sprintf(
			"--resume continues a two-finder execution; this bundle recorded %d finder(s)",
			max(record.Finders, captured.Execution.Finders)))
	case captured.Finders != 0 && captured.Finders != finderCount:
		return c.st.refuse(codeResumeDrift, fmt.Sprintf(
			"%s records %d finder(s) but the interrupted execution launched %d; re-run without --resume",
			runStateFile, captured.Finders, finderCount))
	case captured.FinderSource != "" && record.FinderSource != "" && captured.FinderSource != record.FinderSource:
		return c.st.refuse(codeResumeDrift,
			"the captured finder source no longer matches the interrupted execution; re-run without --resume")
	}
	return nil
}

// launchDefDigest seals the resolved launch definition: the binary, its own
// argv prefix, its identity probe, and the tool shape of a discovery launch. A
// row redefined between attempts under the same name (a prefs agent pointed at
// another executable, or given another argument shape) is a different
// reviewer, so a resume refuses it. So is a row whose documented discovery
// restriction changed, or an attempt that flipped --allow-shell: the two would
// spend the interruption's phases under a tool set the record never sealed.
// Only the digest is recorded: the record has no reason to repeat the argv.
func launchDefDigest(def *agentcatalog.Launch, restriction []string) string {
	var sb strings.Builder
	sb.WriteString("binary=")
	sb.WriteString(def.Binary)
	sb.WriteByte('\n')
	for _, arg := range def.Args {
		sb.WriteString("arg=")
		sb.WriteString(arg)
		sb.WriteByte('\n')
	}
	for _, arg := range def.ProbeArgs {
		sb.WriteString("probe=")
		sb.WriteString(arg)
		sb.WriteByte('\n')
	}
	for _, arg := range def.DiscoveryArgs {
		sb.WriteString("discovery=")
		sb.WriteString(arg)
		sb.WriteByte('\n')
	}
	for _, arg := range restriction {
		sb.WriteString("restrict=")
		sb.WriteString(arg)
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// passThroughDigest seals the operator's pass-through argument list (everything
// after `--`) without writing raw values to launcher.json: arguments can carry
// model selectors or secrets, so the record keeps only their canonical digest.
func passThroughDigest(args []string) string {
	var sb strings.Builder
	for _, arg := range args {
		sb.WriteString(strconv.Itoa(len(arg)))
		sb.WriteByte(':')
		sb.WriteString(arg)
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// fingerprintNow recomputes the fingerprint from the bundle as it stands, with
// the finder source this invocation's policy resolved. It reads only bytes the
// bundle already carries; nothing here writes.
func (c *coordinator) fingerprintNow(captured *config.RunState, source string) (resumeFingerprint, *core.RefusalError) {
	diff, ref := snapshotFileDigest(c.dir, "full.diff")
	if ref != nil {
		return resumeFingerprint{}, ref
	}
	state, ref := snapshotFileDigest(c.dir, runStateFile)
	if ref != nil {
		return resumeFingerprint{}, ref
	}
	// context.txt and threads.json are optional, and their absence is an input
	// too: snapshotFileDigest returns the literal "absent" when missing, distinct
	// from an empty file's digest, so adding, editing, or removing either between
	// attempts refuses rather than drifting silently.
	contextTXT, ref := snapshotFileDigest(c.dir, "context.txt")
	if ref != nil {
		return resumeFingerprint{}, ref
	}
	threadsJSON, ref := snapshotFileDigest(c.dir, "threads.json")
	if ref != nil {
		return resumeFingerprint{}, ref
	}
	binary := provenance.PromptsHash()
	recorded := readcommon.JSONStr(filepath.Join(c.dir, provenance.ReceiptFile), keyPromptsHash)
	if recorded == "" {
		recorded = binary
	}
	fingerprint := resumeFingerprint{
		FullDiffSHA256:    diff,
		RunJSONSHA256:     state,
		InputsSHA256:      captured.InputsSHA256,
		ConfigSHA256:      captured.ConfigSHA256,
		PromptsHash:       recorded,
		BinaryPromptsHash: binary,
		HeadSHA:           readcommon.OpenBundle(c.dir).Head(),
		Agent:             c.base.def.Name,
		AgentSource:       c.base.def.Source,
		ContextTXTSHA256:  contextTXT,
		ThreadsJSONSHA256: threadsJSON,
		LaunchDefSHA256:   launchDefDigest(&c.base.def, c.base.restriction),
		PassThroughSHA256: passThroughDigest(c.st.req.PassThrough),
		Finders:           finderCount,
		FinderSource:      source,
	}
	fingerprint.Fingerprint = fingerprint.digest()
	return fingerprint, nil
}

// failedPhases is the set of phases the recorded manifest names as failed.
func failedPhases(manifest *jq.Obj) map[string]bool {
	failed := map[string]bool{}
	for _, row := range jq.Elements(readcommon.FieldOf(manifest, keyFailures)) {
		if phase := jq.StrOf(readcommon.FieldOf(row, keyPhase)); phase != "" {
			failed[phase] = true
		}
	}
	return failed
}

// boundDigest is the digest the recorded manifest pinned for one execution
// artifact, or "" when the artifact was never bound (an execution that ended
// before its manifest was finished). The key is looked up in its portable
// spelling first, then in the platform's own, so a record written by a build
// that stored backslashes still resolves.
func boundDigest(manifest *jq.Obj, name string) string {
	artifacts := readcommon.FieldOf(manifest, "artifacts")
	portable := portableKey(name)
	if digest := jq.StrOf(readcommon.FieldOf(artifacts, portable)); digest != "" {
		return digest
	}
	if windows := strings.ReplaceAll(portable, "/", `\`); windows != portable {
		return jq.StrOf(readcommon.FieldOf(artifacts, windows))
	}
	return ""
}

// finderReuse resolves one decision per finder: the sealed bytes to continue
// with, or nil when the finder must be launched again.
func (c *coordinator) finderReuse(manifest *jq.Obj) ([]bool, [][]byte, *core.RefusalError) {
	failed := failedPhases(manifest)
	reuse := make([]bool, finderCount)
	sealed := make([][]byte, finderCount)
	for i := range finderCount {
		raw, ref := c.reusableFinder(manifest, finderPhase(i), failed[finderPhase(i)])
		if ref != nil {
			return nil, nil, ref
		}
		reuse[i], sealed[i] = raw != nil, raw
	}
	return reuse, sealed, nil
}

// reusableFinder returns the sealed bytes of one finder phase, or nil when the
// phase must be launched again. A phase the manifest names as failed is always
// re-run, and so is one whose artifact the interrupted execution never bound:
// only bytes that attempt sealed are reused, so an artifact no record vouches
// for is recomputed. A bound artifact whose bytes or shape moved refuses the
// resume instead of letting a damaged review stand in for a finder's work.
func (c *coordinator) reusableFinder(manifest *jq.Obj, name string, failed bool) ([]byte, *core.RefusalError) {
	if failed {
		return nil, nil
	}
	artifact := filepath.Join(name, reviewFile)
	raw, bound, ref := c.recordedArtifact(manifest, artifact)
	if ref != nil || raw == nil || bound == "" {
		return nil, ref
	}
	if c.discoveryValid(raw) {
		return raw, nil
	}
	return nil, c.driftRefusal(artifact + " no longer passes the review checks")
}

// discoveryValid reports whether a recorded finder artifact still passes the
// check this run applies to a freshly launched one.
func (c *coordinator) discoveryValid(raw []byte) bool {
	check, ref := recordcore.CheckDiscovery(c.dir, raw)
	return ref == nil && check.Errors == 0
}

// crossReuse hands back the rows one cross check was verified to keep during
// the read-only plan. The phase's bytes only have to be the bytes that were
// verified, so a file that moved between the plan and the launch refuses.
func (c *coordinator) crossReuse(index int, checker string, batch []any) ([]any, bool, *core.RefusalError) {
	plan := c.resume
	if plan == nil || !plan.reuseCross[index] {
		return nil, false, nil
	}
	artifact := filepath.Join(crossPhase(index), "verdicts.json")
	if ref := c.sealedBytes(plan.crossBytes[index], artifact); ref != nil {
		return nil, false, ref
	}
	rows, valid := c.checkCross(plan.crossBytes[index], checker, batch)
	if !valid {
		return nil, false, c.driftRefusal(artifact + " no longer passes the cross-check validation")
	}
	return rows, true, nil
}

// sealedBytes proves one phase artifact still holds the bytes a plan verified,
// so a run binds what it validated instead of trusting a second read.
func (c *coordinator) sealedBytes(sealed []byte, artifact string) *core.RefusalError {
	digest, ref := snapshotFileDigest(c.dir, artifact)
	if ref != nil {
		return ref
	}
	if digest != shaHex(sealed) {
		return c.driftRefusal(artifact + " changed after the resume verified it")
	}
	return nil
}

// recordedArtifact returns the bytes the interrupted execution sealed for one
// artifact, plus that binding. An unbound artifact is never read: the phase
// that owns it runs again, so its contents buy nothing and a planted file buys
// only a refusal. A bound one is digested first — streaming, so the read is
// bounded by the file rather than by memory — and only then read within the
// resume cap.
func (c *coordinator) recordedArtifact(manifest *jq.Obj, artifact string) ([]byte, string, *core.RefusalError) {
	bound := boundDigest(manifest, artifact)
	if bound == "" {
		return nil, "", c.plainArtifact(artifact)
	}
	digest, ref := snapshotFileDigest(c.dir, artifact)
	if ref != nil {
		return nil, "", ref
	}
	switch digest {
	case snapshotAbsent:
		return nil, "", c.driftRefusal(artifact + " is missing from the interrupted execution")
	case bound:
	default:
		return nil, "", c.driftRefusal(artifact + " changed since the interrupted execution")
	}
	raw, ref := c.readArtifact(artifact)
	if ref != nil {
		return nil, "", ref
	}
	if shaHex(raw) != bound {
		return nil, "", c.driftRefusal(artifact + " changed while the resume read it")
	}
	return raw, bound, nil
}

// clearRelaunched clears a phase's stale output when a resumed attempt is about
// to launch that phase again, leaving verified reused phases untouched.
func (c *coordinator) clearRelaunched(reused bool, phase, artifact string) *core.RefusalError {
	if reused || c.resume == nil {
		return nil
	}
	return c.clearStaleArtifact(phase, artifact)
}

// clearStaleArtifact removes an unsealed phase output before a relaunched phase
// starts, so a relaunched agent that exits without writing cannot inherit an
// earlier attempt's unverified review or verdicts file.
func (c *coordinator) clearStaleArtifact(phase, artifact string) *core.RefusalError {
	rel := filepath.Join(phase, artifact)
	path := filepath.Join(c.dir, rel)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return snapshotUnreadable(rel)
	case !info.Mode().IsRegular():
		return snapshotUnsafe(rel)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c.st.refuse(codeRecordFailed, fmt.Sprintf("cannot clear stale artifact %s: %v", portableKey(rel), err))
	}
	return nil
}

// plainArtifact proves an unbound artifact path is absent or a plain file,
// without reading a byte of it.
func (c *coordinator) plainArtifact(artifact string) *core.RefusalError {
	info, err := os.Lstat(filepath.Join(c.dir, artifact))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return snapshotUnreadable(artifact)
	case !info.Mode().IsRegular():
		return snapshotUnsafe(artifact)
	}
	return nil
}

// readArtifact reads one sealed artifact within the resume read cap.
func (c *coordinator) readArtifact(artifact string) ([]byte, *core.RefusalError) {
	path := filepath.Join(c.dir, artifact)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, snapshotUnreadable(artifact)
	}
	if info.Size() > resumeArtifactCap {
		return nil, c.driftRefusal(fmt.Sprintf("%s is %d bytes, over the %d-byte resume read cap",
			artifact, info.Size(), resumeArtifactCap))
	}
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, snapshotUnreadable(artifact)
	}
	return raw, nil
}

// driftRefusal wraps one drifted artifact in the typed refusal. It launches
// nothing and leaves every recorded byte where it is.
func (c *coordinator) driftRefusal(detail string) *core.RefusalError {
	return c.st.refuse(codeResumeDrift, fmt.Sprintf(
		"cannot resume: %s; re-run without --resume to retire the interrupted execution", detail))
}

// resumeRecord renders this execution's resume block. The fingerprint is
// recorded before the first phase runs, so a run interrupted at any point still
// says which inputs it started from; the reuse provenance joins it on the final
// manifest.
func (c *coordinator) resumeRecord(final bool) *jq.Obj {
	record := &launcherResume{
		SchemaVersion:     resumeSchema,
		ExecutionID:       c.id,
		resumeFingerprint: c.fingerprint,
		RecordedAt:        core.NowUTC(c.st.now()),
		Attempts:          1,
	}
	if c.resume != nil {
		record.PriorStatus = c.resume.priorStatus
		record.Attempts = c.resume.attempts
		if final {
			record.Reused = c.resume.reusedPhases()
			record.Executed = c.resume.executedPhases()
		}
	}
	return record.wire()
}

// resumeData projects the resume provenance into the envelope. It is nil for a
// run that started this execution, so a plain run keeps its data shape.
func (c *coordinator) resumeData() *core.RunResume {
	if c.resume == nil {
		return nil
	}
	return &core.RunResume{
		ExecutionID: c.id,
		PriorStatus: c.resume.priorStatus,
		Fingerprint: c.fingerprint.Fingerprint,
		PromptsHash: c.fingerprint.PromptsHash,
		Attempts:    c.resume.attempts,
		Reused:      c.resume.reusedPhases(),
		Executed:    c.resume.executedPhases(),
	}
}

// reuseRefusal refuses a --resume combination the launcher cannot continue.
// The checks name the flag at fault so a wrapper's retry cannot loop.
func (st *state) reuseRefusal() *core.RefusalError {
	if !st.req.Resume {
		return nil
	}
	switch {
	case st.req.Print || st.req.StageRaw != "":
		return st.refuse(codeBadFlags,
			"--resume continues an interrupted execution and renders no command; drop --print/--stage")
	case st.drainDigest != "":
		return st.refuse(codeBadFlags, "--resume continues a two-finder run; drain launches one agent")
	case !st.twoFinders:
		return st.refuse(codeCannotResume,
			"--resume continues a two-finder execution, and this run would launch one finder — "+
				"re-run without --resume")
	}
	return nil
}
