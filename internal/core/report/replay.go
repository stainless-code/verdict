package reportcore

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"

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
	Reason     string               `json:"reason,omitempty"`
	Dimensions []core.GateDimension `json:"dimensions,omitempty"`
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
		if move := r.moveOf(dim); move != nil {
			moves = append(moves, *move)
		}
	}
	for _, dim := range r.Candidate.Dimensions {
		if seen[dim.ID] {
			continue
		}
		moves = append(moves, DimensionMove{ID: dim.ID, Before: AbsentDimension, After: dim.Status})
	}
	return moves
}

// moveOf reports the move one captured dimension took, or nil when the
// candidate holds it at the captured status.
func (r *PolicyReplay) moveOf(dim core.GateDimension) *DimensionMove {
	after, ok := dimensionStatus(r.Candidate.Dimensions, dim.ID)
	if !ok {
		after = AbsentDimension
	}
	if after == dim.Status {
		return nil
	}
	return &DimensionMove{ID: dim.ID, Before: dim.Status, After: after}
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

// GateFlagsNamed reports whether the invocation names a flag a replay refuses:
// a gate flag would have its exit code swallowed, and --compare would be
// dropped. The adapter refuses this combination as a usage error and Replay
// refuses it as a typed one, so they share the predicate rather than the list —
// a gate flag added later cannot be honored on one boundary and refused on
// the other.
func GateFlagsNamed(f *Flags) bool {
	return f.Check != "" || f.Completeness || f.Coverage != "" || f.MaxRisk != "" || f.CompareSet
}

// replayGateRefusal is the core-side wording: the adapter refuses the same
// combination as a usage error naming the flag to drop, while a Go caller gets
// this typed refusal.
const replayGateRefusal = "a replay reports a comparison and exits 0: gate flags would have " +
	"their exit code swallowed and --compare would be ignored"

// validateReplayRequest refuses a replay that named no candidate file, and one
// that named a gate flag or a baseline: the adapter refuses the second as a
// usage error first, so a Go caller reading this typed refusal sees the same
// rule instead of a silently gated comparison.
func validateReplayRequest(req *Request) *core.RefusalError {
	if req.Flags.Policy == "" {
		return replayRefusal("--policy needs a policy file to replay")
	}
	if GateFlagsNamed(&req.Flags) {
		return replayRefusal(replayGateRefusal)
	}
	return nil
}

// Replay derives the bundle's decision under its captured policy and under the
// candidate policy named by Flags.Policy. The candidate is parsed and resolved
// by the same code that resolves a repository config, so a malformed file
// refuses with the parser's own codes; an unreadable file refuses as a bad
// flag value.
func Replay(ctx context.Context, req *Request) (*PolicyReplay, *core.RefusalError) {
	if ref := validateReplayRequest(req); ref != nil {
		return nil, ref
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
	candidate, candidateRoot, ref := resolveCandidatePolicy(req.Flags.Policy, st.policy.Resolved, st.policy.BaseSHA)
	if ref != nil {
		return nil, ref
	}
	return &PolicyReplay{
		Captured:  deriveDecision(st, st.policy.Resolved, &st.policy.Root),
		Candidate: deriveDecision(st, candidate, &candidateRoot),
	}, nil
}

// deriveDecision runs the compliance, approval, and gate derivations over a
// copy of the loaded state with one policy resolution substituted. The copy
// keeps every recorded input — findings, checks, receipts, spec failures — so
// the replay cannot quietly invent a different bundle.
func deriveDecision(st *state, resolved *config.Resolved, root *config.RequirementSet) Decision {
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
	// The root set is the candidate's own: the compliance dimension reads root
	// clauses and required checks from here, so a captured set left in place
	// would keep failing the candidate on requirements it dropped.
	policy.Root = *root
	// Group ids are digests of the effective defaults, so the same files under
	// a candidate resolved without the captured personal layer carry different
	// ids; leaving the captured ids in place would read a completed check as
	// pending.
	policy.Outcomes = candidateOutcomes(st.policy.Resolved, resolved, st.policy.Outcomes)
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
func resolveCandidatePolicy(
	path string, captured *config.Resolved, baseSHA string,
) (*config.Resolved, config.RequirementSet, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, config.RequirementSet{}, replayRefusal("cannot read candidate policy " + path)
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return nil, config.RequirementSet{}, ref
	}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  cfg,
		Files: capturedFiles(captured),
	})
	if ref != nil {
		return nil, config.RequirementSet{}, ref
	}
	// Privacy exclusions are personal-only — repository scopes and flags can
	// never narrow or remove them — and the content they withheld is still
	// withheld at replay time. A candidate resolved without them would erase
	// every conflict the capture recorded, so it inherits the captured set and
	// lets its own clauses decide whether a conflict still applies.
	resolved.Privacy = captured.Privacy
	return resolved, config.RootSet(cfg, baseSHA), nil
}

// capturedFiles rebuilds the resolver's file inputs from the capture, keeping
// each recorded path and its rename source.
func capturedFiles(captured *config.Resolved) []config.FileInput {
	out := make([]config.FileInput, 0, len(captured.Files))
	for i := range captured.Files {
		out = append(out, config.FileInput{Path: captured.Files[i].Path, OldPath: captured.Files[i].OldPath})
	}
	return out
}

// candidateOutcomes re-keys the captured check observations onto the
// candidate's scopes. Group ids are digests of the effective defaults, so the
// same files resolved without the captured personal layer carry a different
// id, and a completed check would read as pending. An observation whose file
// set the candidate groups differently keeps its captured id and stays
// pending: the work it covered genuinely changed.
func candidateOutcomes(captured, candidate *config.Resolved, outcomes []config.CheckObservation) []config.CheckObservation {
	if captured == nil || candidate == nil || len(outcomes) == 0 {
		return outcomes
	}
	capturedSets := groupFileSets(captured)
	candidateIDs := groupIDs(candidate)
	out := make([]config.CheckObservation, len(outcomes))
	for i := range outcomes {
		out[i] = outcomes[i]
		out[i].Group = rekey(capturedSets, candidateIDs, outcomes[i].Group)
	}
	return out
}

// rekey returns the candidate id of a scope with the same file set, or the
// captured id when the candidate groups those files differently.
func rekey(capturedSets, candidateIDs map[string]string, group string) string {
	key, ok := capturedSets[group]
	if !ok {
		return group
	}
	if id, ok := candidateIDs[key]; ok {
		return id
	}
	return group
}

// groupFileSets maps each plan group id to its file-set key.
func groupFileSets(resolved *config.Resolved) map[string]string {
	out := make(map[string]string, len(resolved.Plan.Groups))
	for i := range resolved.Plan.Groups {
		group := &resolved.Plan.Groups[i]
		out[group.ID] = groupFilesKey(group.Files)
	}
	return out
}

// groupIDs maps each plan group's file-set key to its id.
func groupIDs(resolved *config.Resolved) map[string]string {
	out := make(map[string]string, len(resolved.Plan.Groups))
	for i := range resolved.Plan.Groups {
		group := &resolved.Plan.Groups[i]
		out[groupFilesKey(group.Files)] = group.ID
	}
	return out
}

// groupFilesKey names one scope by its sorted file set, length-prefixed so two
// different sets cannot encode alike.
func groupFilesKey(files []string) string {
	sorted := slices.Clone(files)
	slices.Sort(sorted)
	var b strings.Builder
	for _, file := range sorted {
		b.WriteString(strconv.Itoa(len(file)))
		b.WriteByte(':')
		b.WriteString(file)
	}
	return b.String()
}

// replayRefusal is the typed refusal for a policy that cannot be read at all;
// the config parser supplies its own codes for a policy that parses but does
// not resolve.
func replayRefusal(message string) *core.RefusalError {
	return &core.RefusalError{Code: codeBadFlags, Message: message}
}
