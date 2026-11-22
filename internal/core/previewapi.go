package core

import "github.com/stainless-code/verdict/internal/jq"

// PreviewFile is one changed file a preview would send for review: the
// repository-relative path, the role the bundle resolved for it, and the bytes
// its diff sections carry.
type PreviewFile struct {
	Path  string `json:"path"`
	Role  string `enum:"source|test|docs|config|generated" json:"role"`
	Bytes int    `json:"bytes"                             min:"0"`
}

// PreviewBudget is the advisory review depth the deterministic classifier
// resolves for the previewed diff: the tier, the reasons that selected it, the
// finder count the tier buys, and the cross-check mode derived from it.
type PreviewBudget struct { //nolint:govet // published property order
	Tier       string   `enum:"light|standard|deep" json:"tier"`
	Reasons    []string `json:"reasons"`
	Finders    int      `json:"finders"             min:"0"`
	CrossCheck string   `json:"cross_check"`
}

// PreviewData is the fetch/fetch-local `--preview` payload: the exact reviewed
// file list, every withheld section with its rule, the byte totals, and the
// resolved review budget. A preview writes no bundle and runs no model, so the
// payload carries no bundle path.
type PreviewData struct { //nolint:govet // published property order
	SchemaVersion string         `const:"1"             json:"schema_version"`
	Included      []PreviewFile  `json:"included"`
	Excluded      []ExcludedPath `json:"excluded"`
	IncludedBytes int            `json:"included_bytes" min:"0"`
	ExcludedBytes int            `json:"excluded_bytes" min:"0"`
	Files         int            `json:"files"          min:"0"`
	Budget        PreviewBudget  `json:"budget"`
}

// Wire renders the preview payload in published order. Both lists are non-nil,
// so the wire shape never renders null.
func (p *PreviewData) Wire() *jq.Obj {
	included := make([]any, 0, len(p.Included))
	for _, f := range p.Included {
		included = append(included, jq.NewObj(
			"path", f.Path, "role", f.Role, "bytes", float64(f.Bytes)))
	}
	excluded := make([]any, 0, len(p.Excluded))
	for _, e := range p.Excluded {
		o := jq.NewObj(
			"path", e.Path, "rule_id", e.RuleID, "pattern", e.Pattern, "reason", e.Reason,
			"bytes", float64(e.Bytes))
		if e.OldPath != "" {
			o.Set("old_path", e.OldPath)
		}
		excluded = append(excluded, o)
	}
	reasons := make([]any, 0, len(p.Budget.Reasons))
	for _, r := range p.Budget.Reasons {
		reasons = append(reasons, r)
	}
	return jq.NewObj(
		"schema_version", p.SchemaVersion,
		"included", included,
		"excluded", excluded,
		"included_bytes", float64(p.IncludedBytes),
		"excluded_bytes", float64(p.ExcludedBytes),
		"files", float64(p.Files),
		"budget", jq.NewObj(
			"tier", p.Budget.Tier,
			"reasons", reasons,
			"finders", float64(p.Budget.Finders),
			"cross_check", p.Budget.CrossCheck,
		),
	)
}
