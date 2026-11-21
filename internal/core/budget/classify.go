// Package budget classifies one captured diff into a review depth before any
// agent launches.
//
// Classify is a pure function of the measurements a bundle already carries:
// the changed rows and their roles, the gate's file count, the diff's byte
// count, the resolved walkthrough thresholds, and the sensitive-path globs.
// The signature is the guarantee — there is no filesystem, clock, environment,
// command, or model to reach — so every rule is graded with literals instead of
// a test arguing that nothing else happened.
package budget

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
)

// The three tiers the operator sees. Three names map onto two launch shapes:
// Verdict has exactly one valid execution shape per finder count, and a third
// finder is pinned out of reach by the schema, so deep shares standard's
// dispatch while recording the risk signals that escalated it.
const (
	TierLight    = "light"
	TierStandard = "standard"
	TierDeep     = "deep"
)

// The cross-check modes a tier buys.
const (
	// CrossOff launches no cross-check. Only a single-finder run can do that:
	// every gated finding of a two-finder run needs its opposite finder's
	// verifying check to be approvable at all.
	CrossOff = "off"
	// CrossOpposite launches one directed check per origin, checked by the
	// opposite finder.
	CrossOpposite = "opposite-finder"
)

// RequestAuto asks the classifier to decide; the three tier names ask for a
// depth outright and consult no rule.
const RequestAuto = "auto"

const (
	// lightMaxFiles and lightMaxBytes bound the tiny-diff rule: under a sixth
	// of the built-in walkthrough threshold, and deliberately not configurable,
	// because a second knob set for "small" would drift from the gate's.
	lightMaxFiles = 3
	lightMaxBytes = 4096
)

// cheapRoles are the roles a diff can be made of and still be a cheap pass:
// documentation, tests, and generated output.
var cheapRoles = []string{"docs", "test", "generated"}

// The closed reason vocabulary recorded in provenance and --json. A rule
// appends one of these exact strings, so a later consumer never parses prose.
const (
	// ReasonDocsOnly says every changed row is documentation, a test, or
	// generated output.
	ReasonDocsOnly = "docs-or-test-only"
	// ReasonStandardSize says nothing special held: no sensitive path, no
	// docs-only tree, under the threshold, and not tiny.
	ReasonStandardSize = "standard-size"
)

// File is one changed row: the repository-relative path, the path it was
// renamed from when the bundle recorded one, and the role the bundle resolved
// for it (docs, test, generated, config, or source).
type File struct {
	Path         string
	PreviousPath string
	Role         string
}

// Inputs is everything the classifier may read: the bundle's measurements, the
// resolved walkthrough thresholds, and the built-in globs extended by the
// repository's configuration.
type Inputs struct {
	Files     []File
	Sensitive []string
	FileCount int
	DiffBytes int
	MaxFiles  int
	MaxBytes  int
}

// Decision is the resolved tier, the signals that produced it, and the finder
// count the tier buys. Reasons is non-nil on every path, so the wire shape
// never renders null, and the cross-check mode is derived from Finders instead
// of stored beside it: two records of one fact can disagree.
type Decision struct {
	Tier    string
	Reasons []string
	Finders int
}

// ValidRequest reports whether raw is auto or one of the three tier names. Both
// the flag and the configuration key accept exactly this vocabulary.
func ValidRequest(raw string) bool {
	switch raw {
	case RequestAuto, TierLight, TierStandard, TierDeep:
		return true
	default:
		return false
	}
}

// Dispatch returns the honest launch shape one tier buys: light runs one
// finder with no cross-check, while standard and deep run two finders with the
// opposite-finder cross-check.
func Dispatch(tier string) (finders int, cross string) {
	finders = 2
	if tier == TierLight {
		finders = 1
	}
	return finders, CrossForCount(finders)
}

// CrossForCount names the cross-check mode one effective finder count buys:
// a single finder cannot cross-check itself, and two finders always do.
func CrossForCount(finders int) string {
	if finders == 1 {
		return CrossOff
	}
	return CrossOpposite
}

// ForTier builds the decision an explicitly requested tier produces: the
// dispatch that tier buys and no rule reasons, because no rule decided it.
func ForTier(tier string) Decision {
	finders, _ := Dispatch(tier)
	return Decision{Tier: tier, Reasons: []string{}, Finders: finders}
}

// Classify applies the six ordered rules and returns the first match. Rules:
//
//	R1  a changed path matches a sensitive glob      -> deep
//	R2  docs/test/generated only, under threshold    -> light
//	R3  docs/test/generated only, over threshold     -> standard
//	R4  over the walkthrough threshold               -> deep
//	R5  a tiny diff with readable rows                -> light
//	R6  otherwise                                     -> standard
//
// R1 outranks everything: a one-file match is still deep, which is the point
// of a sensitive glob. R3 exists so a lockfile or documentation avalanche is
// not escalated on size alone. Empty file rows never read as light: a bundle
// that measured nothing is missing evidence, and missing evidence never drops
// risk.
func Classify(in *Inputs) Decision {
	if glob, ok := sensitiveHit(in); ok {
		return decision(TierDeep, SensitiveLine(glob))
	}
	docsOnly := len(in.Files) > 0 && cheapTree(in.Files)
	over := overThreshold(in)
	switch {
	case docsOnly && !over:
		return decision(TierLight, ReasonDocsOnly)
	case docsOnly:
		return decision(TierStandard, ReasonDocsOnly, thresholdReason("large-docs-only", in))
	case over:
		return decision(TierDeep, thresholdReason("walkthrough-threshold", in))
	case len(in.Files) > 0 && in.DiffBytes > 0 && in.FileCount <= lightMaxFiles && in.DiffBytes <= lightMaxBytes:
		return decision(TierLight, fmt.Sprintf("tiny-diff: %d/%d", in.FileCount, in.DiffBytes))
	default:
		return decision(TierStandard, ReasonStandardSize)
	}
}

// decision pairs a tier with the reasons that selected it and the dispatch the
// tier buys.
func decision(tier string, reasons ...string) Decision {
	finders, _ := Dispatch(tier)
	return Decision{Tier: tier, Reasons: reasons, Finders: finders}
}

// overThreshold reports whether the diff crosses the walkthrough gate.
func overThreshold(in *Inputs) bool {
	return in.FileCount > in.MaxFiles || in.DiffBytes > in.MaxBytes
}

// thresholdReason names the gate a threshold rule crossed: the resolved files
// and bytes the diff exceeded, not the diff's own measurements, which the
// decision records beside it.
func thresholdReason(prefix string, in *Inputs) string {
	return fmt.Sprintf("%s: %d/%d", prefix, in.MaxFiles, in.MaxBytes)
}

// cheapTree reports whether every changed row is documentation, a test, or
// generated output.
func cheapTree(files []File) bool {
	for i := range files {
		if !slices.Contains(cheapRoles, files[i].Role) {
			return false
		}
	}
	return true
}

// sensitiveHit returns the first sensitive glob that matches a changed path,
// scanning paths in sorted order and globs in configuration order, so the
// reported glob is deterministic whatever order the bundle listed its rows in.
// A rename is probed at both ends: a file moved out of a sensitive directory
// still touched that directory's code, so the previous path matches too.
func sensitiveHit(in *Inputs) (string, bool) {
	paths := sensitivePaths(in.Files)
	for _, path := range paths {
		for _, glob := range in.Sensitive {
			if config.Match(glob, path) {
				return glob, true
			}
		}
	}
	return "", false
}

// sensitivePaths collects the paths one diff touched, sorted: each row's own
// path plus, for a rename, the path it came from, so a file moved out of a
// sensitive directory still matches that directory's glob.
func sensitivePaths(files []File) []string {
	paths := make([]string, 0, len(files))
	for i := range files {
		paths = append(paths, files[i].Path)
		if files[i].PreviousPath != "" {
			paths = append(paths, files[i].PreviousPath)
		}
	}
	slices.Sort(paths)
	return paths
}

// SensitiveLine renders the reason one glob produced, so the recorded string
// and the classifier's own vocabulary cannot drift.
func SensitiveLine(glob string) string { return "sensitive-path: " + glob }

// Vocabulary renders every accepted request value in documentation order, so a
// refusal message and the usage line cannot drift from what ValidRequest takes.
func Vocabulary() string {
	return strings.Join([]string{RequestAuto, TierLight, TierStandard, TierDeep}, "|")
}
