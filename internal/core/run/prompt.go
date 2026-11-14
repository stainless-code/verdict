package run

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core/recipe"
)

// directive is the tool-generated prompt the launched agent receives: the
// subject, the bundle it reads, the single review file it must write, the
// boundary (verdict validates that file; it never reads prose, retries, or
// posts), and the pipeline the recipe declared.
//
// It carries no bundle bytes on purpose. The agent reads the bundle with its
// own tools, so nothing attacker-influenced enters the prompt, no privacy
// exclusion is re-resolved at launch time, and the command --print renders
// stays byte-identical for every run of the same bundle. Handing an agent the
// whole fenced dump is `fetch --show-prompts`, a separate, fetch-time decision.
func directive(repo, pr, bundle, artifact string, local bool, plan *recipe.Plan) string {
	subject := fmt.Sprintf("%s PR %s", repo, pr)
	meta := "PR.json"
	if local {
		subject = fmt.Sprintf("%s local change %s", repo, pr)
		meta = "source.json"
	}
	read := "Read the installed verdict review skill first, then the bundle at "
	read += "%s: %s, roles.txt, and toc.txt indexing full.diff, plus kb.txt, "
	read += "sensitive.txt, and panel when the run carries them."
	write := "Write %s with {\"schema_version\": \"1\", \"body\": ..., "
	write += "\"approved\": ..., \"comments\": [...]} (optional \"resolved\" and "
	write += "\"triage\"). Write nothing else in the bundle."
	boundary := "verdict launched this one process and validates that file; it does "
	boundary += "not read your prose, retry you, or post. Do not post, merge, or "
	boundary += "push: publishing is an explicit verdict post."
	return fmt.Sprintf("Review %s and write exactly one review file.\n\n"+
		read+"\n\n"+write+"\n\n"+boundary+"\n\n"+recipeParagraph(plan)+"\n",
		subject, bundle, meta, artifact)
}

// recipeParagraph renders the declared pipeline: the recipe's identity, every
// resolved lens with its hint and caps, the synthesizer hint, the declared
// budget, and the closing reminder that these are declarations. Every value is
// an id, an enum, a bounded number, or a pattern-checked hint, so a recipe's
// free-text description never reaches the prompt and cannot reshape it.
func recipeParagraph(plan *recipe.Plan) string {
	parts := make([]string, 0, len(plan.Stages)+1)
	for _, stage := range plan.Stages {
		parts = append(parts, stageLine(stage))
	}
	if plan.BudgetUSD > 0 {
		parts = append(parts, "budget_usd "+strconv.FormatFloat(plan.BudgetUSD, 'f', -1, floatBitSize))
	}
	return fmt.Sprintf("The review pipeline is recipe %s@%s (%s): %s. "+
		"These are declarations, not selections: verdict launched this one process "+
		"and never chose a model.",
		plan.Name, plan.Version, plan.Source, strings.Join(parts, "; "))
}

// stageLine renders one resolved stage: the lens list with the hints and caps
// the recipe declared, the synthesizer hint, or the bare stage name.
func stageLine(stage recipe.Stage) string {
	switch stage.Kind {
	case recipe.StageLenses:
		return lensLine(stage.Lenses)
	case recipe.StageSynthesize:
		if stage.Model == "" {
			return stage.Kind
		}
		return stage.Kind + " (model " + stage.Model + ")"
	default:
		return stage.Kind
	}
}

// lensLine renders the resolved lens list in order. An empty list says so: the
// stage names nothing, which is legal and the ordinary lens-less bundle.
func lensLine(lenses []recipe.LensUse) string {
	if len(lenses) == 0 {
		return "lenses none"
	}
	rows := make([]string, 0, len(lenses))
	for _, lens := range lenses {
		rows = append(rows, lensRow(lens))
	}
	return "lenses " + strings.Join(rows, ", ")
}

// lensRow renders one lens: its id, then the model hint and caps the recipe
// declared for it.
func lensRow(lens recipe.LensUse) string {
	var hints []string
	if lens.Model != "" {
		hints = append(hints, "model "+lens.Model)
	}
	if lens.MaxFiles > 0 {
		hints = append(hints, "max_files "+strconv.Itoa(lens.MaxFiles))
	}
	if lens.MaxFindings > 0 {
		hints = append(hints, "max_findings "+strconv.Itoa(lens.MaxFindings))
	}
	if len(hints) == 0 {
		return lens.ID
	}
	return lens.ID + " (" + strings.Join(hints, ", ") + ")"
}
