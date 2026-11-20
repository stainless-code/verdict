// Package validation owns the trusted validator pass: the `validation.json`
// artifact, its strict reader, and the rigor policy. It never invokes a model:
// the invoker runs the pass and verdict reads the file.
package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// FileName is the bundle artifact this package reads.
const FileName = "validation.json"

const promptFile = "prompts/validate.md"

// MaxBytes caps the artifact.
const MaxBytes = 1 << 20

// Refusal codes the artifact and the policy raise.
const (
	CodeBad        = "bad-validation"
	CodeStale      = "validation-stale"
	CodeBadPolicy  = "bad-rigor-policy"
	CodeBelowFloor = "rigor-below-floor"
)

const (
	severityCritical      = "critical"
	severityImportant     = "important"
	severityInformational = "informational"

	minReasonLen = 10
	maxReasonLen = 500
)

var publishedChecks = []string{"hedge", "repro", "anchor", "rigor", "severity"}

// Policy is the effective publication floor: the mode and the rung each
// severity tier must reach.
type Policy struct {
	Floors core.RigorFloors
	Mode   string
}

// PolicyFromConfig resolves the typed base configuration over the built-ins,
// which are advisory with critical walked.
func PolicyFromConfig(cfg *core.Config) Policy {
	out := Policy{Mode: core.ValidationWarn, Floors: core.RigorFloors{
		Critical: core.RungWalked, Important: core.RungPointed, Informational: core.RungSaid,
	}}
	if cfg == nil || cfg.RigorPolicy == nil {
		return out
	}
	p := cfg.RigorPolicy
	if p.Mode != "" {
		out.Mode = p.Mode
	}
	for _, field := range []struct {
		dst   *string
		value string
	}{
		{dst: &out.Floors.Critical, value: p.Critical},
		{dst: &out.Floors.Important, value: p.Important},
		{dst: &out.Floors.Informational, value: p.Informational},
	} {
		if field.value != "" {
			*field.dst = field.value
		}
	}
	return out
}

// WithRequireRigor narrows the policy for one invocation: a named tier takes
// the flag's rung and the policy gates for it, whatever the base file said.
func (p *Policy) WithRequireRigor(flags []string) *core.RefusalError {
	for _, flag := range flags {
		tier, rung, ok := strings.Cut(flag, "=")
		dst := p.tierFloor(tier)
		if !ok || dst == nil || !slices.Contains(core.RungValues, rung) {
			return &core.RefusalError{Code: CodeBadPolicy, Message: fmt.Sprintf(
				"--require-rigor: want %s|%s|%s=<one of %s>, got %q",
				severityCritical, severityImportant, severityInformational,
				strings.Join(core.RungValues, "|"), flag)}
		}
		*dst = rung
		p.Mode = core.ValidationGate
	}
	return nil
}

func (p *Policy) tierFloor(tier string) *string {
	switch tier {
	case severityCritical:
		return &p.Floors.Critical
	case severityImportant:
		return &p.Floors.Important
	case severityInformational:
		return &p.Floors.Informational
	default:
		return nil
	}
}

// Floor returns the rung the severity tier must reach.
func (p *Policy) Floor(severity string) string {
	if floor := p.tierFloor(severity); floor != nil {
		return *floor
	}
	return ""
}

// Violation is one finding below its tier's floor.
type Violation struct {
	Sig      string
	Severity string
	Rigor    string
	Floor    string
}

// CandidateObj lifts one finding a pass or a floor owes a judgement, or nil.
func CandidateObj(entry any) *jq.Obj {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil || !Candidate(m) {
		return nil
	}
	return m
}

// Detail renders one violation as `short-sig (severity rigor < floor)`.
func (v Violation) Detail() string {
	return fmt.Sprintf("%s (%s %s < %s)", readcommon.ShortSHA(v.Sig), v.Severity, v.Rigor, v.Floor)
}

// Floors splits the posting findings below their tier's floor into the ones a
// gate downgrades and the rest it names.
func Floors(entries []any, p Policy) (downgrades, violations []Violation) {
	for _, entry := range entries {
		m := CandidateObj(entry)
		if m == nil {
			continue
		}
		if v, below := underFloor(m, &p); below {
			downgrades, violations = placeFloor(v, p, downgrades, violations)
		}
	}
	return downgrades, violations
}

func placeFloor(v Violation, p Policy, downgrades, violations []Violation) ([]Violation, []Violation) {
	if v.Severity == severityCritical && core.RungRank(v.Rigor) <= core.RungRank(p.Floors.Important) {
		return append(downgrades, v), violations
	}
	return downgrades, append(violations, v)
}

func underFloor(m *jq.Obj, p *Policy) (Violation, bool) {
	severity := jq.StrOf(readcommon.FieldOf(m, "severity"))
	floor := p.Floor(severity)
	if floor == "" || core.RungRank(readcommon.FieldOf(m, "rigor")) <= core.RungRank(floor) {
		return Violation{}, false
	}
	return Violation{
		Sig: jq.StrOf(readcommon.FieldOf(m, "sig")), Severity: severity,
		Rigor: core.RungOf(m), Floor: floor,
	}, true
}

// Tally counts one pass against the findings it judged, applying nothing.
func Tally(file *core.ValidationFile, findings []any) core.ValidationCounts {
	var counts core.ValidationCounts
	judged := make(map[string]bool, len(file.Results))
	for _, result := range file.Results {
		judged[result.Sig] = true
		switch result.Verdict {
		case core.ValidationConfirmed:
			counts.Confirmed++
		case core.ValidationRejected:
			counts.Rejected++
		case core.ValidationDowngraded:
			counts.Downgraded++
		}
	}
	for _, finding := range findings {
		if sig := jq.StrOf(readcommon.FieldOf(finding, "sig")); !judged[sig] && Candidate(finding) {
			counts.Unvalidated++
		}
	}
	return counts
}

// Candidate reports a finding a pass would judge: still open, not already
// withheld. A run that skipped the pass counts the same set unvalidated, so
// the tally, the skipped count, and the merge cannot disagree.
func Candidate(finding any) bool {
	return jq.StrOf(readcommon.FieldOf(finding, "status")) == "open" &&
		readcommon.WithheldSource(finding) == ""
}

// BelowFloor refuses a gate-mode post, naming every finding under its floor.
func BelowFloor(violations []Violation) *core.RefusalError {
	details := make([]string, 0, len(violations))
	for _, violation := range violations {
		details = append(details, violation.Detail())
	}
	return &core.RefusalError{Code: CodeBelowFloor, Message: fmt.Sprintf(
		"%d finding(s) below the required evidence floor — %s", len(details), strings.Join(details, ", "))}
}

// Load reads the optional artifact and its exact-bytes hash; absent is not an
// error.
func Load(dir, head string, findings []any) (*core.ValidationFile, string, *core.RefusalError) {
	path := filepath.Join(dir, FileName)
	raw, err := jq.CappedFile(path, MaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", &core.RefusalError{Code: CodeBad, Message: FileName + ": " + err.Error()}
	}
	file, ref := Parse(raw, head, findings)
	if ref != nil {
		return nil, "", ref
	}
	return file, Hash(raw), nil
}

// PromptDrift names the prompt digest a pass recorded when it differs from the
// validate prompt this binary ships, so the operator learns the pass judged
// against another rubric. It is empty when the pass names no digest or the two
// agree.
func PromptDrift(file *core.ValidationFile) string {
	if file.Validator == nil || strings.TrimSpace(file.Validator.PromptSHA256) == "" {
		return ""
	}
	shipped, err := skillfs.Read(promptFile)
	if err != nil {
		return ""
	}
	recorded, current := strings.TrimSpace(file.Validator.PromptSHA256), Hash(shipped)
	if strings.EqualFold(recorded, current) {
		return ""
	}
	return fmt.Sprintf("validation.json was produced with %s sha256 %s, but this build ships %s: the pass may have judged against a different prompt",
		promptFile, readcommon.ShortSHA(recorded), readcommon.ShortSHA(current))
}

// Hash is the SHA-256 of the artifact's exact bytes.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Parse strictly decodes one artifact; every shape defect refuses
// bad-validation, and a head that is not the bundle head refuses
// validation-stale.
func Parse(raw []byte, head string, findings []any) (*core.ValidationFile, *core.RefusalError) {
	var file core.ValidationFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, bad("validation.json: %v", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, bad("validation.json: trailing data after the object")
	}
	if file.SchemaVersion != "1" {
		return nil, bad("validation.json needs schema_version 1")
	}
	if ref := checkHead(file.HeadSHA, head); ref != nil {
		return nil, ref
	}
	if ref := checkResults(file.Results, findings); ref != nil {
		return nil, ref
	}
	return &file, nil
}

func checkHead(written, head string) *core.RefusalError {
	if written == head {
		return nil
	}
	return &core.RefusalError{Code: CodeStale, Message: fmt.Sprintf(
		"validation.json was written for head %s, not the bundle head %s",
		readcommon.ShortSHA(written), readcommon.ShortSHA(head))}
}

func checkResults(results []core.ValidationResult, findings []any) *core.RefusalError {
	seen := make(map[string]bool, len(results))
	for i := range results {
		if ref := checkResult(&results[i], findings); ref != nil {
			return ref
		}
		if seen[results[i].Sig] {
			return bad("validation.json carries two results for sig %s", results[i].Sig)
		}
		seen[results[i].Sig] = true
	}
	return nil
}

func checkResult(r *core.ValidationResult, findings []any) *core.RefusalError {
	if !slices.Contains([]string{core.ValidationConfirmed, core.ValidationRejected, core.ValidationDowngraded}, r.Verdict) {
		return bad("validation.json verdict %q is not CONFIRMED, REJECTED, or DOWNGRADED", r.Verdict)
	}
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < minReasonLen || len(reason) > maxReasonLen {
		return bad("validation.json result for %s needs a reason of %d–%d characters",
			r.Sig, minReasonLen, maxReasonLen)
	}
	if ref := checkDowngrade(r); ref != nil {
		return ref
	}
	resolved := readcommon.ResolveSig(findings, r.Sig)
	if resolved == "NONE" || resolved == "MANY" {
		return bad("validation.json sig %q matches no single finding", r.Sig)
	}
	r.Sig = resolved
	r.Checks = knownChecks(r.Checks)
	return nil
}

func knownChecks(in []string) []string {
	out := make([]string, 0, len(in))
	for _, check := range in {
		if slices.Contains(publishedChecks, check) {
			out = append(out, check)
		}
	}
	return out
}

func checkDowngrade(r *core.ValidationResult) *core.RefusalError {
	if r.Verdict != core.ValidationDowngraded {
		if r.FromSeverity != "" || r.ToSeverity != "" {
			return bad("validation.json result for %s carries a severity pair on a %s verdict", r.Sig, r.Verdict)
		}
		return nil
	}
	from, to := config.SeverityRank(r.FromSeverity), config.SeverityRank(r.ToSeverity)
	if from == 0 || to == 0 || from-to != 1 {
		return bad("validation.json DOWNGRADED for %s needs exactly one rank: critical→important or important→informational", r.Sig)
	}
	return nil
}

func bad(format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: CodeBad, Message: fmt.Sprintf(format, args...)}
}
