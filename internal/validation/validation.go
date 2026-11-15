// Package validation owns the trusted validator pass: the `validation.json`
// artifact a second prompt run writes, its strict reader, and the rigor
// policy that floors each severity tier. It never invokes a model, fetches,
// or writes: the invoker runs the pass and verdict reads the file.
//
// The policy lives in the typed configuration (`rigor_policy`) so the trusted
// base snapshot is its only source; a head edit cannot move the gate. The
// CLI's `--require-rigor` narrows it per invocation and implies gate mode.
package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// FileName is the bundle artifact this package reads.
const FileName = "validation.json"

// MaxBytes caps the artifact: a validation pass writes one bounded object.
const MaxBytes = 1 << 20

// Refusal codes the artifact and the policy raise.
const (
	CodeBad        = "bad-validation"
	CodeStale      = "validation-stale"
	CodeBadPolicy  = "bad-rigor-policy"
	CodeBelowFloor = "rigor-below-floor"
)

// publishedChecks is the closed check vocabulary the pass may name; an
// unknown name is dropped with a warning, never refused.
var publishedChecks = []string{"hedge", "repro", "anchor", "rigor", "severity"}

// Policy is the effective publication floor: the mode and the rung each
// severity tier must reach.
type Policy struct {
	Floors core.RigorFloors
	Mode   string
}

// PolicyFromConfig resolves the typed base configuration over the built-in
// policy. Nil reads the built-ins: advisory, critical walked.
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

// WithRequireRigor applies the repeatable `--require-rigor <tier>=<rung>`
// flags over the policy: a named tier takes the flag's rung and the policy
// gates for it, whatever the base file said.
func (p Policy) WithRequireRigor(flags []string) (Policy, *core.RefusalError) {
	for _, flag := range flags {
		tier, rung, ok := strings.Cut(flag, "=")
		dst := p.floorFor(tier)
		if !ok || dst == nil || !slices.Contains(core.RungValues, rung) {
			return p, &core.RefusalError{Code: CodeBadPolicy, Message: fmt.Sprintf(
				"--require-rigor: want critical|important|informational=<one of %s>, got %q",
				strings.Join(core.RungValues, "|"), flag)}
		}
		*dst = rung
		p.Mode = core.ValidationGate
	}
	return p, nil
}

func (p *Policy) floorFor(tier string) *string {
	switch tier {
	case "critical":
		return &p.Floors.Critical
	case "important":
		return &p.Floors.Important
	case "informational":
		return &p.Floors.Informational
	default:
		return nil
	}
}

// Floor returns the rung the severity tier must reach.
func (p Policy) Floor(severity string) string {
	switch severity {
	case "critical":
		return p.Floors.Critical
	case "important":
		return p.Floors.Important
	case "informational":
		return p.Floors.Informational
	default:
		return ""
	}
}

// Violation is one finding below its tier's floor: the finding the gate names
// and the pair a reader needs to fix it.
type Violation struct {
	Sig      string
	Severity string
	Rigor    string
	Floor    string
}

// Detail renders one violation for a refusal or a warning: the short sig
// plus `severity rigor < floor`.
func (v Violation) Detail() string {
	return fmt.Sprintf("%s (%s %s < %s)", readcommon.ShortSHA(v.Sig), v.Severity, v.Rigor, v.Floor)
}

// Floors splits the posting findings that miss their tier's floor into the
// critical ones that downgrade to important (their rung already clears the
// important floor) and the rest the gate names. Withheld findings never reach
// it, and an absent rigor reads as `said`, so a defaulted rung cannot satisfy
// a `walked` floor.
func Floors(entries []any, p Policy) (downgrades, violations []Violation) {
	for _, entry := range entries {
		m, ok := entry.(*jq.Obj)
		if !ok || m == nil || readcommon.WithheldSource(m) != "" {
			continue
		}
		severity := jq.StrOf(readcommon.FieldOf(m, "severity"))
		floor := p.Floor(severity)
		if floor == "" {
			continue
		}
		rigor, _ := m.Get("rigor")
		if core.RungRank(rigor) <= core.RungRank(floor) {
			continue
		}
		v := Violation{
			Sig: jq.StrOf(readcommon.FieldOf(m, "sig")), Severity: severity,
			Rigor: core.RungOf(m), Floor: floor,
		}
		if severity == "critical" && core.RungRank(rigor) <= core.RungRank(p.Floors.Important) {
			downgrades = append(downgrades, v)
			continue
		}
		violations = append(violations, v)
	}
	return downgrades, violations
}

// BelowFloor refuses a gate-mode post: one line per finding under its tier's
// floor, each naming the short sig and the pair to fix.
func BelowFloor(violations []Violation) *core.RefusalError {
	details := make([]string, 0, len(violations))
	for _, violation := range violations {
		details = append(details, violation.Detail())
	}
	return &core.RefusalError{Code: CodeBelowFloor, Message: fmt.Sprintf(
		"%d finding(s) below the required evidence floor — %s", len(details), strings.Join(details, ", "))}
}

// Load reads the optional artifact at dir/validation.json and returns its
// exact-bytes hash. Absent reads (nil, "", nil); present reads through Parse.
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

// Hash is the SHA-256 of the artifact's exact bytes.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Parse strictly decodes one artifact: unknown keys, an unpublished verdict,
// a reason outside 10–500 characters, a downgrade that is not exactly one
// rank, a duplicate sig, and a sig no finding resolves all refuse
// bad-validation; a head that is not the bundle head refuses validation-stale.
func Parse(raw []byte, head string, findings []any) (*core.ValidationFile, *core.RefusalError) {
	var file core.ValidationFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, bad("validation.json: %v", err)
	}
	if file.SchemaVersion != "1" {
		return nil, bad("validation.json needs schema_version 1")
	}
	if file.HeadSHA != head {
		return nil, &core.RefusalError{Code: CodeStale, Message: fmt.Sprintf(
			"validation.json was written for head %s, not the bundle head %s",
			readcommon.ShortSHA(file.HeadSHA), readcommon.ShortSHA(head))}
	}
	seen := make(map[string]bool, len(file.Results))
	for i := range file.Results {
		if ref := checkResult(&file.Results[i], findings); ref != nil {
			return nil, ref
		}
		if seen[file.Results[i].Sig] {
			return nil, bad("validation.json carries two results for sig %s", file.Results[i].Sig)
		}
		seen[file.Results[i].Sig] = true
	}
	return &file, nil
}

// checkResult normalizes one result: the sig resolves to its full value, the
// verdict and downgrade pair are legal, and unknown checks are dropped.
func checkResult(r *core.ValidationResult, findings []any) *core.RefusalError {
	if !slices.Contains([]string{core.ValidationConfirmed, core.ValidationRejected, core.ValidationDowngraded}, r.Verdict) {
		return bad("validation.json verdict %q is not CONFIRMED, REJECTED, or DOWNGRADED", r.Verdict)
	}
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < 10 || len(reason) > 500 {
		return bad("validation.json result for %s needs a reason of 10–500 characters", r.Sig)
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

// knownChecks keeps the published check names in order and drops the rest;
// an empty result stays a non-nil list.
func knownChecks(in []string) []string {
	out := make([]string, 0, len(in))
	for _, check := range in {
		if slices.Contains(publishedChecks, check) {
			out = append(out, check)
		}
	}
	return out
}

// checkDowngrade enforces exactly one rank down: critical→important and
// important→informational only, informational never downgrades.
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
