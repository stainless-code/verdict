// Package risk implements STA-102's recomputable advisory risk assessment:
// parsing stored risk claims from post.json/record.json, computing the
// deterministic risk floor from findings, coverage, and head freshness, and
// resolving reported = max(claimed, floor) so missing evidence never lets
// risk silently drop to low.
package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Published risk levels in ascending severity order. LevelUnknown marks an
// absent or unassessed claim and never aliases low.
const (
	LevelUnknown  = "unknown"
	LevelLow      = "low"
	LevelMedium   = "medium"
	LevelHigh     = "high"
	LevelCritical = "critical"

	ReasonMissingClaim   = "missing-claim"
	ReasonMissingCov     = "missing-coverage"
	ReasonUnreviewedFile = "unreviewed-files"
	ReasonStaleHead      = "stale-head"
	GateReasonExceeded   = "risk-exceeded"
	GateReasonUnknown    = "risk-unknown"
)

const (
	postSuffix     = "/post.json"
	recordSuffix   = "/record.json"
	chunksManifest = "/chunks/manifest.json"
	fullDiffSuffix = "/full.diff"
)

var (
	validLevels = []string{LevelLow, LevelMedium, LevelHigh, LevelCritical}
	bandOrder   = []string{LevelLow, LevelMedium, LevelHigh, LevelCritical, LevelUnknown}
)

// Levels returns the four assessed risk levels in ascending order.
func Levels() []string { return slices.Clone(validLevels) }

// Bands returns all five reporting bands (low through critical, then unknown).
func Bands() []string { return slices.Clone(bandOrder) }

// ValidLevel reports whether s is one of the four assessed risk levels.
func ValidLevel(s string) bool { return slices.Contains(validLevels, s) }

// Rank returns the ordinal rank of a risk level (unknown=0, low=1..critical=4).
func Rank(level string) int {
	return slices.Index(validLevels, level) + 1
}

// MaxLevel returns the higher-ranked of two risk levels.
func MaxLevel(a, b string) string {
	if Rank(b) > Rank(a) {
		return b
	}
	return a
}

// CoverageInput holds stored diff-coverage completeness for floor checks.
type CoverageInput struct {
	Known           bool
	UnreviewedFiles int
}

// Receipt holds the risk claim and coverage attestation read from post.json
// and/or record.json.
type Receipt struct {
	Claim         *core.RiskClaim
	CoverageHead  string
	Coverage      CoverageInput
	CoverageFound bool
}

// ReadReceipt loads post.json then record.json for dir, extracting the latest
// stored risk claim and the coverage attestation matching coverageKey.
func ReadReceipt(dir, coverageKey string) Receipt {
	var out Receipt
	isChunked := isRegular(dir + chunksManifest)
	for _, suffix := range []string{postSuffix, recordSuffix} {
		m, ok := readReceiptMap(dir + suffix)
		if !ok {
			continue
		}
		if out.Claim == nil {
			out.Claim = claimFromMap(m)
		}
		head := receiptIdentity(m)
		mergeCoverage(&out, m, head, coverageKey, isChunked)
	}
	return out
}

// CoverageIdentity resolves the head or diff digest key used to match stored
// coverage receipts in dir, mirroring gate coverage identity.
func CoverageIdentity(dir, headSHA string) string {
	if isConcreteHead(headSHA) {
		return headSHA
	}
	return worktreeDiffIdentity(dir)
}

func worktreeDiffIdentity(dir string) string {
	source := dir + "/source.json"
	if readcommon.JSONStr(source, "kind") != "worktree" || readcommon.JSONRaw(source, "dirty") != "true" {
		return ""
	}
	expected := readcommon.JSONStr(source, "diff_sha256")
	if expected == "" || fileSHA256Hex(dir+fullDiffSuffix) != expected {
		return ""
	}
	return "diff:" + expected
}

func fileSHA256Hex(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// EvaluateBundle computes the full advisory risk report for a bundle from a
// stored receipt, head SHA, stored findings, and --max-risk threshold.
func EvaluateBundle(dir, headSHA, covKey string, entries []any, maxRisk string) *core.ReportRisk {
	receipt := ReadReceipt(dir, covKey)
	return Evaluate(receipt.Claim, headSHA, entries, receipt.Coverage, maxRisk)
}

// BundleBand returns the risk band a bundle classifies into for trend rollups:
// bundles without a stored risk claim stay "unknown" (never "low"), while
// claimed bundles resolve to max(claimed, floor).
func BundleBand(dir, headSHA string, allEntries []any) string {
	covKey := CoverageIdentity(dir, headSHA)
	receipt := ReadReceipt(dir, covKey)
	if receipt.Claim == nil || !ValidLevel(receipt.Claim.Level) {
		return LevelUnknown
	}
	res := Evaluate(receipt.Claim, headSHA, allEntries, receipt.Coverage, "")
	return res.Reported
}

// ComputeFloor deterministically computes the minimum risk floor and receipt
// reasons from store findings, coverage completeness, and head freshness.
func ComputeFloor(entries []any, cov CoverageInput, staleHead bool) (string, core.RiskInputs, []string) {
	inputs := collectInputs(entries, cov, staleHead)
	floor := findingsFloor(inputs)
	evFloor, reasons := evidenceFloor(inputs)
	return MaxLevel(floor, evFloor), inputs, reasons
}

func findingsFloor(inputs core.RiskInputs) string {
	switch {
	case inputs.OpenCritical > 0:
		return LevelCritical
	case inputs.OpenImportant > 0:
		return LevelHigh
	case inputs.Deferred > 0 || inputs.Unanchored > 0:
		return LevelMedium
	default:
		return LevelLow
	}
}

func evidenceFloor(inputs core.RiskInputs) (string, []string) {
	floor := LevelLow
	var reasons []string
	if inputs.MissingCoverage {
		floor = LevelMedium
		reasons = append(reasons, ReasonMissingCov)
	} else if inputs.UnreviewedFiles != nil && *inputs.UnreviewedFiles > 0 {
		floor = LevelMedium
		reasons = append(reasons, ReasonUnreviewedFile)
	}
	if inputs.StaleHead {
		floor = LevelMedium
		reasons = append(reasons, ReasonStaleHead)
	}
	return floor, reasons
}

// Evaluate combines an optional stored risk claim with the computed store
// floor. Missing claims read as "unknown" (never "low") and raise reported to
// the floor whenever the floor is above low; when floor is low and no claim
// exists, reported stays "unknown" so an unassessed bundle never poses as low.
func Evaluate(claim *core.RiskClaim, bundleHead string, entries []any, cov CoverageInput, maxRisk string) *core.ReportRisk {
	staleHead := isClaimStale(claim, bundleHead)
	floor, inputs, reasons := ComputeFloor(entries, cov, staleHead)

	claimed := LevelUnknown
	var assessedHead *string
	var rationale *string
	if claim != nil && ValidLevel(claim.Level) {
		claimed = claim.Level
		assessedHead = &claim.AssessedHead
		rationale = &claim.Rationale
	} else {
		reasons = append([]string{ReasonMissingClaim}, reasons...)
	}

	reported := resolveReported(claimed, floor)
	out := &core.ReportRisk{
		Claimed:        claimed,
		Floor:          floor,
		Reported:       reported,
		AssessedHead:   assessedHead,
		Rationale:      rationale,
		Inputs:         inputs,
		ReceiptReasons: reasons,
	}
	applyMaxRisk(out, maxRisk)
	return out
}

func resolveReported(claimed, floor string) string {
	if claimed == LevelUnknown {
		if Rank(floor) > Rank(LevelLow) {
			return floor
		}
		return LevelUnknown
	}
	return MaxLevel(claimed, floor)
}

func applyMaxRisk(out *core.ReportRisk, maxRisk string) {
	if maxRisk == "" {
		return
	}
	out.MaxRisk = &maxRisk
	if out.Reported == LevelUnknown {
		out.Exceeded = true
		reason := GateReasonUnknown
		out.GateReason = &reason
		return
	}
	if Rank(out.Reported) > Rank(maxRisk) {
		out.Exceeded = true
		reason := GateReasonExceeded
		out.GateReason = &reason
	}
}

func isClaimStale(claim *core.RiskClaim, bundleHead string) bool {
	if claim == nil {
		return false
	}
	if claim.Stale {
		return true
	}
	return isConcreteHead(bundleHead) && claim.AssessedHead != bundleHead
}

func isConcreteHead(head string) bool {
	return head != "" && head != "dirty" && head != "unknown"
}

func collectInputs(entries []any, cov CoverageInput, staleHead bool) core.RiskInputs {
	var in core.RiskInputs
	in.StaleHead = staleHead
	in.MissingCoverage = !cov.Known
	if cov.Known {
		n := max(0, cov.UnreviewedFiles)
		in.UnreviewedFiles = &n
	}
	for _, e := range entries {
		accumulateEntry(&in, e)
	}
	return in
}

func accumulateEntry(in *core.RiskInputs, e any) {
	switch readcommon.StatusOf(e) {
	case "open":
		countOpenSeverity(in, e)
		if isUnanchored(e) {
			in.Unanchored++
		}
	case "deferred":
		in.Deferred++
		if isUnanchored(e) {
			in.Unanchored++
		}
	}
}

func countOpenSeverity(in *core.RiskInputs, e any) {
	switch readcommon.FieldOf(e, "severity") {
	case LevelCritical:
		in.OpenCritical++
	case "important":
		in.OpenImportant++
	}
}

func isUnanchored(e any) bool {
	if readcommon.KindOf(e) != "inline" {
		return false
	}
	b, ok := readcommon.FieldOf(e, "anchored").(bool)
	return !ok || !b
}

func readReceiptMap(path string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	return jq.AsMap(doc)
}

func claimFromMap(m map[string]any) *core.RiskClaim {
	rm, ok := jq.AsMap(m["risk"])
	if !ok {
		return nil
	}
	level := jq.Str(rm["level"])
	if !ValidLevel(level) {
		return nil
	}
	stale := rm["stale"] == true
	return &core.RiskClaim{
		Level:        level,
		Rationale:    jq.Str(rm["rationale"]),
		AssessedHead: jq.Str(rm["assessed_head"]),
		Stale:        stale,
	}
}

func receiptIdentity(m map[string]any) string {
	head := jq.Str(m["head"])
	if isConcreteHead(head) {
		return head
	}
	if diff := jq.Str(m["diff_sha256"]); diff != "" {
		return "diff:" + diff
	}
	return head
}

func mergeCoverage(out *Receipt, m map[string]any, head, covKey string, isChunked bool) {
	if out.Coverage.Known {
		return
	}
	if covKey != "" && head != covKey {
		out.CoverageHead = head
		return
	}
	out.CoverageHead = head
	out.CoverageFound = true
	if isChunked {
		if cm, ok := jq.AsMap(m["chunk_coverage"]); ok {
			att := intAt(cm, "attested")
			tot := intAt(cm, "total")
			out.Coverage = CoverageInput{Known: true, UnreviewedFiles: max(0, tot-att)}
		}
		return
	}
	if cm, ok := jq.AsMap(m["coverage"]); ok {
		out.Coverage = CoverageInput{Known: true, UnreviewedFiles: intAt(cm, "gaps")}
	}
}

func intAt(m map[string]any, key string) int {
	f, ok := readcommon.NumVal(m[key])
	if !ok {
		return 0
	}
	return int(f)
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
