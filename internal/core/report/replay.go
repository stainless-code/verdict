package reportcore

import (
	"context"
	"os"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
)

// PolicyReplay is one stored bundle decided twice: once under the policy the
// run captured, and once under a candidate policy file. Both decisions are
// derived here from the same stored findings, checks, and receipts, so every
// difference between them is attributable to the policy alone. It is a read:
// no bundle artifact is rewritten, no model is called, and no remote thread is
// re-checked — a live dimension the run recorded is not compared.
type PolicyReplay struct {
	Captured  Decision `json:"captured"`
	Candidate Decision `json:"candidate"`
}

// Decision is the gate and approval one policy produces over a stored bundle.
type Decision struct {
	Dimensions []core.GateDimension `json:"dimensions,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	GatePassed bool                 `json:"gate_passed"`
	Approved   bool                 `json:"approved"`
}

// DimensionMove is one gate dimension whose status differs between the captured
// and candidate derivations.
type DimensionMove struct {
	ID     string `json:"id"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// AbsentDimension names a side that carries no such dimension at all: a policy
// that adds a requirement turns compliance from nothing into a dimension, and
// skipping it would print "nothing changed" over a gate that flipped.
const AbsentDimension = "absent"

// Moves lists every dimension whose status differs, captured order first and
// then candidate-only dimensions, so a consumer reads the comparison in gate
// order and never misses the dimension a policy added or removed.
func (r *PolicyReplay) Moves() []DimensionMove {
	moves := make([]DimensionMove, 0, len(r.Captured.Dimensions))
	seen := make(map[string]bool, len(r.Captured.Dimensions))
	for _, dim := range r.Captured.Dimensions {
		seen[dim.ID] = true
		after, ok := dimensionStatus(r.Candidate.Dimensions, dim.ID)
		if !ok {
			after = AbsentDimension
		}
		if after == dim.Status {
			continue
		}
		moves = append(moves, DimensionMove{ID: dim.ID, Before: dim.Status, After: after})
	}
	for _, dim := range r.Candidate.Dimensions {
		if seen[dim.ID] {
			continue
		}
		moves = append(moves, DimensionMove{ID: dim.ID, Before: AbsentDimension, After: dim.Status})
	}
	return moves
}

// dimensionStatus finds one dimension's status by wire ID.
func dimensionStatus(dims []core.GateDimension, id string) (string, bool) {
	for _, dim := range dims {
		if dim.ID == id {
			return dim.Status, true
		}
	}
	return "", false
}

// Replay derives the bundle's decision under its captured policy and under the
// candidate policy named by Flags.Policy. The candidate is parsed and resolved
// by the same code that resolves a repository config, so a malformed file
// refuses with the parser's own codes; an unreadable file refuses as a bad
// flag value.
func Replay(ctx context.Context, req *Request) (*PolicyReplay, *core.RefusalError) {
	if req.Flags.Policy == "" {
		return nil, replayRefusal("--policy needs a policy file to replay")
	}
	flags := normalizeFlags(req)
	if ref := validateReportFlags(&flags); ref != nil {
		return nil, ref
	}
	st := &state{
		req:      req,
		flags:    flags,
		warnings: append([]string{}, req.Warnings...),
	}
	if ref := loadState(ctx, st); ref != nil {
		return nil, ref
	}
	if ref := loadRunPolicy(st); ref != nil {
		return nil, ref
	}
	if st.policy == nil || st.policy.Resolved == nil {
		return nil, replayRefusal("run bundle carries no typed policy to replay against — re-fetch or re-run the bundle")
	}
	st.specFailures = specFailuresWithExecution(st)
	candidate, ref := resolveCandidatePolicy(req.Flags.Policy, st.policy.Resolved)
	if ref != nil {
		return nil, ref
	}
	return &PolicyReplay{
		Captured:  deriveDecision(st, st.policy.Resolved),
		Candidate: deriveDecision(st, candidate),
	}, nil
}

// deriveDecision runs the compliance, approval, and gate derivations over a
// copy of the loaded state with one policy resolution substituted. The copy
// keeps every recorded input — findings, checks, receipts, spec failures — so
// the replay cannot quietly invent a different bundle.
func deriveDecision(st *state, resolved *config.Resolved) Decision {
	derived := *st
	derived.warnings = nil
	// Silence the operator's observer on the copy: a derivation that warns
	// would otherwise print each warning twice, once per policy, and leave the
	// adapter's own warning list holding duplicates.
	req := *st.req
	req.Progress = nil
	derived.req = &req
	derived.compliance = nil
	derived.decisionCompliance = nil
	derived.requiredGaps = nil
	policy := *st.policy
	policy.Resolved = resolved
	derived.policy = &policy
	evaluateCompliance(&derived)
	derived.approval = approval.Evaluate(derived.entries, st.flags.Check, st.flags.Category,
		derived.decisionCompliance, st.specFailures, st.evidenceInput())
	failed, _, gate := gateTail(&derived)
	return Decision{
		Dimensions: gate.Dimensions,
		Reason:     derived.approval.Reason,
		GatePassed: !failed,
		Approved:   derived.approval.Approved,
	}
}

// resolveCandidatePolicy parses and resolves a candidate policy file against
// the file set the captured run recorded, so path-scoped defaults and clauses
// land on the same paths they would have on the original run.
func resolveCandidatePolicy(path string, captured *config.Resolved) (*config.Resolved, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, replayRefusal("cannot read candidate policy " + path)
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return nil, ref
	}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  cfg,
		Files: capturedFiles(captured),
	})
	if ref != nil {
		return nil, ref
	}
	return resolved, nil
}

// capturedFiles rebuilds the resolver's file inputs from the capture, keeping
// each recorded path and its rename source.
func capturedFiles(captured *config.Resolved) []config.FileInput {
	out := make([]config.FileInput, 0, len(captured.Files))
	for _, file := range captured.Files {
		out = append(out, config.FileInput{Path: file.Path, OldPath: file.OldPath})
	}
	return out
}

// replayRefusal is the typed refusal for a policy that cannot be read at all;
// the config parser supplies its own codes for a policy that parses but does
// not resolve.
func replayRefusal(message string) *core.RefusalError {
	return &core.RefusalError{Code: codeBadFlags, Message: message}
}
