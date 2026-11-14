package core

// RecipePlan is the resolved review pipeline a run records: the published
// `data.recipe` object and the `recipe` key of `launcher.json`. It lives in the
// contract root rather than in the recipe service because the envelope has to
// name it, and the service imports the root for its refusals — the wire shape
// is the part both sides share.
//
// Warnings never reach the wire — they ride the run's warnings[] and one stderr
// line each — so the plan object itself stays exactly the published shape.
type RecipePlan struct { //nolint:govet // field order is the published data.recipe order
	Name      string        `json:"name"`
	Version   string        `json:"version"`
	Source    string        `enum:"builtin|repo" json:"source"`
	SHA256    string        `json:"sha256"`
	BudgetUSD float64       `exclusiveMin:"0"    json:"budget_usd,omitempty"`
	Stages    []RecipeStage `json:"stages"       maxItems:"4"                minItems:"4"`
	Warnings  []string      `json:"-"`
}

// RecipeStage is one resolved stage. Only the lenses stage carries a source and
// lens rows; only the synthesize stage carries a model hint.
type RecipeStage struct { //nolint:govet // field order is the published stage order
	Kind       string       `enum:"lenses|synthesize|dedupe|rank" json:"kind"`
	LensSource string       `enum:"declared|bundle"               json:"lens_source,omitempty"`
	Lenses     []RecipeLens `json:"lenses,omitempty"`
	Model      string       `json:"model,omitempty"`
}

// RecipeLens is one lens the lenses stage names, with the hint and caps the
// recipe declared for it.
type RecipeLens struct {
	ID          string `json:"id"`
	Model       string `json:"model,omitempty"`
	MaxFiles    int    `json:"max_files,omitempty"`
	MaxFindings int    `json:"max_findings,omitempty"`
}
