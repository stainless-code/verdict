package panelcore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	fieldChecker          = "checker"
	fieldStatus           = "status"
	executionIDKey        = "execution_id"
	executionReviewSHA    = "review_sha256"
	executionVerification = "verification"
	executionHead         = "head_sha"
	executionDiffSHA      = "diff_sha256"
	executionPlanSHA      = "plan_sha256"
	twoFinders            = 2
	recordFile            = "record.json"
	postFile              = "post.json"
	fieldFinders          = "finders"
)

// CodeQuote locates the checker evidence independently of the finding.
type CodeQuote struct {
	Path string `json:"path"`
	Side string `enum:"LEFT|RIGHT" json:"side"`
	Text string `json:"text"`
	Line int    `json:"line"       min:"1"`
}

func parseQuote(raw any) (CodeQuote, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return CodeQuote{}, bad("invalid checker quote")
	}
	var quote CodeQuote
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&quote); err != nil {
		return CodeQuote{}, bad("invalid checker quote")
	}
	return quote, nil
}

// Execution keeps eligibility derived from bound files out of agent input.
type Execution struct {
	Origins        map[string][]string
	Expected       map[string][]string
	Checked        map[string][]any
	Eligible       map[string]bool
	BlockingGap    string
	ID             string
	dir, head      string
	receiptSHA     string
	Gap            string
	SpecViolations []string
	Completed      int
	Active         bool
	// CrossRequired records that the bundle captured two-finder mode. It
	// outlives the artifacts the capture bound: a receipt that still states
	// the execution forbids reading its findings as a manual review.
	CrossRequired bool
	// oneFinder records that the capture settled on one finder (see
	// readTopology). Such a capture is manual by declaration: a leftover
	// execution reference must not re-arm cross checks on it.
	oneFinder bool
	// finders is the captured count, largest seen first, so no later statement
	// can downgrade a live two-finder capture.
	finders int
}

// CheckExecution prevents captured automatic work from becoming manual.
func CheckExecution(dir string, review *jq.Obj) *Execution {
	state := executionState(dir)
	captured, _ := readObject(dir, "run.json")
	state.readTopology(captured)
	rootReview, raw := readObject(dir, "review.json")
	state.readReferences(captured, rootReview, review)
	state.readReceiptReference()
	if !state.liveCapture() || state.Gap != "" {
		return state
	}
	if review == nil {
		review = rootReview
	}
	if rootReview == nil || review == nil || captured == nil {
		state.Gap = "execution review or captured run missing"
		return state
	}
	if !state.referencesMatch(captured, rootReview, review) {
		state.Gap = "execution reference stripped"
		return state
	}

	if !sameAggregate(rootReview, review) {
		state.Gap = "execution review differs from frozen union"
		return state
	}
	state.seal(state.checkFiles(dir, review, raw))
	return state
}

func executionState(dir string) *Execution {
	state := &Execution{
		dir: dir, Eligible: make(map[string]bool), Expected: make(map[string][]string),
		Checked: make(map[string][]any), Origins: make(map[string][]string),
	}
	manifest, _ := readObject(dir, "launcher.json")
	if id := jq.StrOf(readcommon.FieldOf(manifest, executionIDKey)); id != "" {
		state.ID = id
		state.Active = true
		state.captureFinders(twoFinders)
	}
	return state
}

// storedTwoFinders reports whether a record.json or post.json receipt bound to
// the bundle's current head carries verification.finders. Only a two-finder
// capture's summary writes that object, so its presence states the capture
// even when one finder failed; a receipt for another head says nothing about
// this one.
func storedTwoFinders(dir string) bool {
	head := readcommon.OpenBundle(dir).Head()
	if head == "" {
		return false
	}
	for _, name := range []string{recordFile, postFile} {
		doc, _ := readObject(dir, name)
		if jq.StrOf(readcommon.FieldOf(doc, "head")) != head {
			continue
		}
		if readcommon.FieldOf(readcommon.FieldOf(doc, executionVerification), fieldFinders) != nil {
			return true
		}
	}
	return false
}

// captureFinders folds one capture statement into the recorded topology: the
// largest count wins, and a capture that states nothing leaves the mode
// undecided.
func (s *Execution) captureFinders(count int) {
	s.finders = max(s.finders, count)
	// At least two finders require a cross check. An out-of-range count fails
	// closed: treating it as manual would clear the stamp and let a
	// quote-only finding pass as an unreviewed capture.
	s.CrossRequired = s.finders >= twoFinders
}

// liveCapture reports whether the bound files can be read. A captured
// execution whose artifacts are gone keeps its gap: it may never be read as a
// manual review.
func (s *Execution) liveCapture() bool {
	if s.CrossRequired && !s.Active {
		s.Gap = "captured execution artifacts missing"
		return false
	}
	return s.Active
}

func (s *Execution) readReceiptReference() {
	if s.Active || s.oneFinder {
		return
	}
	receipt, _ := readObject(s.dir, filepath.Join(executionVerification, "verdicts.json"))
	if id := jq.StrOf(readcommon.FieldOf(receipt, executionIDKey)); id != "" {
		s.ID, s.Active = id, true
		s.captureFinders(twoFinders)
	}
}

func (s *Execution) readReferences(docs ...*jq.Obj) {
	for _, doc := range docs {
		s.readReference(doc)
	}
}

func (s *Execution) readReference(doc *jq.Obj) {
	if doc == nil || s.oneFinder {
		return
	}
	value, exists := doc.Get("execution")
	if !exists {
		return
	}
	s.Active = true
	s.captureFinders(twoFinders)
	id := jq.StrOf(readcommon.FieldOf(value, "id"))
	if jq.R(readcommon.FieldOf(value, fieldFinders)) != "2" {
		s.Gap = "execution requires two finders"
	}
	if id == "" || (s.ID != "" && s.ID != id) {
		s.Gap = "execution reference missing or inconsistent"
	}
	s.ID = id
}

func (s *Execution) referencesMatch(docs ...*jq.Obj) bool {
	for _, doc := range docs {
		value, _ := doc.Get("execution")
		if jq.StrOf(readcommon.FieldOf(value, "id")) != s.ID {
			return false
		}
	}
	return true
}

func readObject(dir, name string) (*jq.Obj, []byte) {
	raw, err := readArtifact(dir, name, maxReceiptBytes)
	if err != nil {
		return nil, nil
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, nil
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		return nil, nil
	}
	return obj, raw
}

func readArtifact(dir, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, bad("execution bundle unreadable")
	}
	defer func() { _ = root.Close() }()
	file, err := readcommon.OpenNoBlockIn(root, name)
	if err != nil {
		return nil, bad("execution artifact missing")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, bad("execution artifact is not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, bad("execution artifact exceeds read limit")
	}
	return raw, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Execution) checkFiles(dir string, review *jq.Obj, raw []byte) string {
	manifest, _ := readObject(dir, "launcher.json")
	plan, planRaw := readObject(dir, filepath.Join(executionVerification, "plan.json"))
	diff, err := readArtifact(dir, "full.diff", coreexec.FetchOutputCap)
	if manifest == nil || plan == nil || err != nil {
		return "execution manifest, plan or diff missing"
	}
	head := readcommon.OpenBundle(dir).Head()
	s.head = head
	bindings := map[string]string{
		executionIDKey: s.ID, executionHead: head, executionDiffSHA: digest(diff),
		executionReviewSHA: digest(raw), executionPlanSHA: digest(planRaw),
	}
	if gap := bindingGap(manifest, bindings); gap != "" {
		return "execution manifest " + gap
	}

	s.completedFinders(manifest)
	if gap := s.planExpectations(plan, Findings(review)); gap != "" {
		return gap
	}
	file, gap := s.boundReceipt(dir, head, bindings, Findings(review))
	if gap != "" {
		return gap
	}

	if gap = s.checkResults(plan, file, evidence.IndexDiff(diff), Findings(review)); gap != "" {
		return gap
	}
	s.blockingGap(Findings(review))
	s.specGap(review)
	if readcommon.FieldOf(manifest, fieldStatus) != "complete" {
		return "execution manifest is incomplete"
	}
	return ""
}

// boundReceipt validates the panel receipt against this check's bindings and
// keeps its digest, so a receipt removed or replaced before the stamp is
// published cannot carry the standing this check gave it.
func (s *Execution) boundReceipt(dir, head string, bindings map[string]string, findings []any) (*File, string) {
	receipt, receiptRaw := readObject(dir, filepath.Join(executionVerification, "verdicts.json"))
	if receipt == nil {
		return nil, "execution panel receipt missing"
	}
	if gap := bindingGap(receipt, bindings); gap != "" {
		return nil, "execution panel " + gap
	}

	file, warnings, parseErr := Parse(receiptRaw, head, findings)
	if parseErr != nil || len(warnings) > 0 {
		return nil, "execution panel receipt invalid"
	}
	s.receiptSHA = digest(receiptRaw)
	return file, ""
}

func bindingGap(doc *jq.Obj, bindings map[string]string) string {
	for _, key := range []string{executionIDKey, executionHead, executionDiffSHA, executionReviewSHA, executionPlanSHA} {
		want := bindings[key]
		if want == "" || readcommon.FieldOf(doc, key) != want {
			return "binding mismatch: " + key
		}
	}
	return ""
}

func (s *Execution) blockingGap(findings []any) {
	for _, finding := range findings {
		sig := jq.StrOf(readcommon.FieldOf(finding, fieldSig))
		status := readcommon.StatusOf(finding)
		blockingTier := core.TierRank(readcommon.FieldOf(finding, fieldSeverity)) <= core.TierRank("important")
		if (status == "" || status == readcommon.StatusOpen) && !s.Eligible[sig] && blockingTier {
			s.BlockingGap = "blocking cross check missing, unverified or contested"
		}
	}
}

func (s *Execution) checkResults(plan *jq.Obj, file *File, idx *evidence.DiffIndex, findings []any) string {
	expected, gap := expectedChecks(plan, findings)
	if gap != "" {
		return gap
	}

	if gap := s.initializeChecks(expected, findings); gap != "" {
		return gap
	}

	for _, result := range file.Results {
		if gap := s.acceptResult(expected, idx, &result); gap != "" {
			return gap
		}
	}

	s.missingChecks(expected)
	return ""
}

func (s *Execution) missingChecks(expected map[string]map[string]bool) {
	for sig, checks := range expected {
		if len(checks) > 0 {
			s.Eligible[sig] = false
		}
	}
}

func (s *Execution) acceptResult(expected map[string]map[string]bool, idx *evidence.DiffIndex, result *Result) string {
	checks := expected[result.Sig]
	if !checks[result.Checker] {
		return "execution received an unexpected checker or duplicate"
	}
	delete(checks, result.Checker)
	valid := checkResultQuote(idx, result)
	quoteStatus := "unverified"
	if valid {
		quoteStatus = "verified"
	}
	s.Checked[result.Sig] = append(s.Checked[result.Sig], jq.NewObj(fieldChecker, result.Checker,
		fieldVerdict, result.Verdict, "quote_status", quoteStatus, "evidence", result.Evidence, "quote", crossQuoteDocument(result.Quote)))
	if !valid || result.Verdict != confirmed {
		s.Eligible[result.Sig] = false
	}
	return ""
}

func (s *Execution) initializeChecks(expected map[string]map[string]bool, findings []any) string {
	if len(findings) > maxFindings {
		return "execution exceeds 100 findings"
	}
	for _, row := range findings {
		sig := jq.StrOf(readcommon.FieldOf(row, fieldSig))
		if len(expected[sig]) == 0 {
			return "execution plan omits a finding"
		}
		s.Eligible[sig] = true
		s.Expected[sig] = nil
		s.Origins[sig] = nil
		s.assignCheckers(sig, expected[sig])
		slices.Sort(s.Expected[sig])
		slices.Sort(s.Origins[sig])
	}
	return ""
}

func expectedChecks(plan *jq.Obj, findings []any) (map[string]map[string]bool, string) {
	expected := make(map[string]map[string]bool)
	rows, _ := plan.Get("checks")
	for _, row := range jq.Elements(rows) {
		sig := jq.StrOf(readcommon.FieldOf(row, fieldSig))
		origin := jq.StrOf(readcommon.FieldOf(row, "origin"))
		checker := jq.StrOf(readcommon.FieldOf(row, fieldChecker))
		if invalidPlannedCheck(origin, checker, sig, findings) {
			return nil, "execution plan contains an invalid or self check"
		}
		if expected[sig] == nil {
			expected[sig] = make(map[string]bool)
		}
		if expected[sig][checker] {
			return nil, "execution plan repeats a check"
		}
		expected[sig][checker] = true
	}
	return expected, ""
}

func invalidCheck(origin, checker string) bool {
	return !finder(origin) || !finder(checker) || origin == checker
}

func finder(id string) bool { return id == "finder-1" || id == "finder-2" }

func checkResultQuote(idx *evidence.DiffIndex, result *Result) bool {
	if result.Verdict == insufficient || result.Quote == nil {
		return false
	}
	q := result.Quote
	return evidence.MatchQuote(idx, evidence.QuoteRequest{Path: q.Path, Side: q.Side, Line: q.Line, Quote: q.Text}).Verified()
}

// Stamp discards agent-written cross standing. A captured two-finder
// execution always re-derives it from the bound artifacts, and a manual
// capture never keeps an automatic standing the store still carries.
func (s *Execution) Stamp(entries []any) {
	if !s.CrossRequired {
		clearCross(entries)
		return
	}
	if s.Active {
		s.refreshCapture()
	}
	for _, row := range entries {
		s.stampEntry(row)
	}
	s.blockingGap(entries)
	if s.BlockingGap != "" {
		s.SpecViolations = nil
	}
}

func (s *Execution) stampEntry(row any) {
	entry, ok := row.(*jq.Obj)
	if !ok {
		return
	}
	prior, _ := entry.Get(executionVerification)
	verification, ok := prior.(*jq.Obj)
	if !ok {
		verification = jq.NewObj("quote", "unchecked")
	}
	sig := jq.StrOf(readcommon.FieldOf(entry, fieldSig))
	checks := appendPriorChecks(s.Checked[sig], verification)
	if checks == nil {
		checks = []any{}
	}
	status := "pending"
	if s.Gap == "" && s.Eligible[sig] {
		status = "confirmed"
	}
	if hasRefutation(checks) {
		status = "contested"
	}
	cross := jq.NewObj(executionIDKey, s.ID, fieldStatus, status, "origins", checkerValues(s.Origins[sig]),
		"expected", checkerValues(s.Expected[sig]), "checks", checks)
	verification.Set("cross", cross)
	s.Eligible[sig] = EligibleCross(cross)
	entry.Set(executionVerification, verification)
}

// ApplyEvidence makes a broken execution insufficient even for an empty union.
func (s *Execution) ApplyEvidence(receipt *core.EvidenceReceipt) {
	if !s.CrossRequired || (s.Gap == "" && s.BlockingGap == "") || receipt == nil {
		return
	}
	receipt.Insufficient = true
	receipt.Gaps = append(receipt.Gaps, "execution: "+strings.TrimSpace(s.Gap+" "+s.BlockingGap))
}

// Problem is the bound execution failure exposed by readiness.
func (s *Execution) Problem() string {
	if !s.CrossRequired {
		return ""
	}
	if s.Gap != "" {
		return "execution: " + s.Gap
	}
	if s.BlockingGap != "" {
		return "execution: " + s.BlockingGap
	}
	return ""
}

// EligibleCross requires completion and every assigned checker.
func EligibleCross(cross any) bool {
	if cross == nil {
		return true
	}
	if !completedCross(cross) {
		return false
	}
	expected := jq.Elements(readcommon.FieldOf(cross, "expected"))
	checks := jq.Elements(readcommon.FieldOf(cross, "checks"))
	if len(expected) == 0 || len(checks) != len(expected) {
		return false
	}
	seen := make(map[string]bool)
	for _, check := range checks {
		checker := jq.StrOf(readcommon.FieldOf(check, fieldChecker))
		if invalidCompletedCheck(seen, expected, check, checker) {
			return false
		}
		seen[checker] = true
	}
	return true
}

func confirmedQuote(check any) bool {
	return readcommon.FieldOf(check, fieldVerdict) == confirmed && readcommon.FieldOf(check, "quote_status") == "verified"
}

func checkerValues(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func (s *Execution) specGap(review *jq.Obj) {
	rows, _ := review.Get("spec_checks")
	for _, row := range jq.Elements(rows) {
		sig := jq.StrOf(readcommon.FieldOf(row, "finding_sig"))
		if readcommon.FieldOf(row, fieldStatus) != "violated" {
			continue
		}
		if !s.Eligible[sig] {
			s.BlockingGap = "spec violation lacks an eligible cross-checked finding"
			continue
		}
		s.SpecViolations = append(s.SpecViolations, jq.StrOf(readcommon.FieldOf(row, "item")))
	}
}

func invalidCompletedCheck(seen map[string]bool, expected []any, check any, checker string) bool {
	return seen[checker] || !slices.Contains(expected, any(checker)) || !confirmedQuote(check)
}

func invalidPlannedCheck(origin, checker, sig string, findings []any) bool {
	return invalidCheck(origin, checker) || readcommon.ResolveSig(findings, sig) != sig
}

func aggregateVersion(review *jq.Obj) bool {
	return readcommon.FieldOf(review, "schema_version") == "2"
}

func appendPriorChecks(checks []any, verification *jq.Obj) []any {
	prior, _ := verification.Get("panel")
	for _, row := range jq.Elements(prior) {
		if readcommon.FieldOf(row, fieldVerdict) != refuted {
			continue
		}
		checks = append(checks, jq.NewObj(fieldChecker, readcommon.FieldOf(row, fieldChecker),
			fieldVerdict, refuted, "quote_status", "unverified", fieldEvidence, readcommon.FieldOf(row, fieldEvidence)))
	}
	return checks
}

func (s *Execution) seal(gap string) {
	s.Gap = gap
	if gap != "" {
		s.Eligible = make(map[string]bool)
		if gap != "execution manifest is incomplete" {
			s.Checked = nil
		}
		s.SpecViolations = nil
	}
}

func sameAggregate(root, review *jq.Obj) bool {
	return sameReview(root, review) && aggregateVersion(root)
}

func crossQuoteDocument(quote *CodeQuote) any {
	if quote == nil {
		return nil
	}
	return jq.NewObj("path", quote.Path, "side", quote.Side, "line", quote.Line, "text", quote.Text)
}

func (s *Execution) planExpectations(plan *jq.Obj, findings []any) string {
	expected, gap := expectedChecks(plan, findings)
	if gap != "" {
		return gap
	}
	return s.initializeChecks(expected, findings)
}

func (s *Execution) assignCheckers(sig string, expected map[string]bool) {
	for checker := range expected {
		s.Expected[sig] = append(s.Expected[sig], checker)
		origin := "finder-1"
		if checker == origin {
			origin = "finder-2"
		}
		s.Origins[sig] = append(s.Origins[sig], origin)
	}
}

func completedCross(cross any) bool {
	return jq.StrOf(readcommon.FieldOf(cross, executionIDKey)) != "" && readcommon.FieldOf(cross, fieldStatus) == confirmed
}

// clearCross removes the derived stamp from every entry: a manual capture
// never carries automatic standing, whatever the store it read held.
func clearCross(entries []any) {
	for _, entry := range entries {
		verification, ok := readcommon.FieldOf(entry, executionVerification).(*jq.Obj)
		if ok {
			verification.Delete("cross")
		}
	}
}

// readTopology settles the capture mode from the strongest statement present:
//
//  1. a live marker: launcher.json's execution id, or run.json's two-finder
//     count. Nothing below can downgrade it.
//  2. run.json's explicit one-finder count. `run --finders 1` writes it at
//     launch, so it is newer than any receipt it contradicts.
//  3. a receipt bound to the current head that carries a finder summary
//     (storedTwoFinders): evidence that two finders reviewed this head.
//  4. the captured config default (resolved.defaults.values.finders): policy
//     for future runs, never evidence about a past one.
//
// A one-finder decision at 2 or 4 also discards the review.json and panel
// receipt execution references a retired or re-fetched execution leaves; with
// no decision at all, those references arm the capture themselves.
func (s *Execution) readTopology(captured any) {
	count, _ := jq.IntOf(readcommon.FieldOf(captured, fieldFinders))
	switch {
	case count == twoFinders:
		s.Active = true
		s.captureFinders(count)
	case s.Active:
	case count == 1:
		s.oneFinder = true
		s.captureFinders(count)
	case storedTwoFinders(s.dir):
		s.captureFinders(twoFinders)
	case count == 0 && configuredFinders(captured) == 1:
		s.oneFinder = true
		s.captureFinders(1)
	default:
		s.captureFinders(count)
	}
}

// configuredFinders is the finder count the captured config resolved.
func configuredFinders(captured any) int {
	defaults := readcommon.FieldOf(readcommon.FieldOf(captured, "resolved"), "defaults")
	count, _ := jq.IntOf(readcommon.FieldOf(readcommon.FieldOf(defaults, "values"), fieldFinders))
	return count
}

func (s *Execution) refreshCapture() {
	if s.Gap != "" {
		return
	}
	if readcommon.OpenBundle(s.dir).Head() != s.head {
		s.seal("bundle head changed before applying cross checks")
		return
	}
	captured, _ := readObject(s.dir, "run.json")
	if jq.StrOf(readcommon.FieldOf(readcommon.FieldOf(captured, "execution"), "id")) != s.ID {
		s.seal("captured execution changed before applying cross checks")
		return
	}
	receipt, receiptRaw := readObject(s.dir, filepath.Join(executionVerification, "verdicts.json"))
	if receipt == nil || digest(receiptRaw) != s.receiptSHA {
		s.seal("execution panel receipt changed before applying cross checks")
	}
}
