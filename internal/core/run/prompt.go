package run

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core/recipe"
)

// paragraphGap separates the directive's paragraphs, so every one of them reads
// as its own instruction block for the agent.
const paragraphGap = "\n\n"

// directive is the tool-generated prompt the launched agent receives: the
// subject, the bundle it reads, the single review file it must write, the
// boundary (verdict validates that file; it never reads prose, retries, or
// posts), and the pipeline the recipe declared.
//
// Every paragraph is formatted with its own fixed verbs and the paragraphs are
// concatenated: the bundle path reaches readLine as a value, so a checkout
// directory named "%s" stays a directory name instead of consuming the write
// instruction's artifact argument.
//
// It carries no bundle bytes on purpose. The agent reads the bundle with its
// own tools, so nothing attacker-influenced enters the prompt, no privacy
// exclusion is re-resolved at launch time, and the command --print renders
// stays byte-identical for every run of the same bundle. Handing an agent the
// whole fenced dump is `fetch --show-prompts`, a separate, fetch-time decision.
func directive(repo, pr, bundle, artifact string, local bool, plan *recipe.Plan) string {
	subject, meta := subjectAndMeta(repo, pr, local)
	head := fmt.Sprintf("Review %s and write exactly one review file.", subject)
	read := readLine(bundle, meta, "kb.txt, sensitive.txt, and panel when the run carries them.")
	write := fmt.Sprintf("Write %s with {\"schema_version\": \"1\", \"body\": ..., "+
		"\"approved\": ..., \"comments\": [...]} (optional \"resolved\" and "+
		"\"triage\"). Write nothing else in the bundle.", artifact)
	boundary := "verdict launched this one process and validates that file; it does "
	boundary += "not read your prose, retry you, or post. Do not post, merge, or "
	boundary += "push: publishing is an explicit verdict post."
	return head + paragraphGap + read + paragraphGap + write + paragraphGap + boundary +
		paragraphGap + recipeParagraph(plan) + "\n"
}

// subjectAndMeta names the review target and the metadata file the bundle
// carries: PR.json for a fetched PR, source.json for a local change.
func subjectAndMeta(repo, pr string, local bool) (subject, meta string) {
	if local {
		return fmt.Sprintf("%s local change %s", repo, pr), "source.json"
	}
	return fmt.Sprintf("%s PR %s", repo, pr), "PR.json"
}

// readLine is the one sentence both directives share about reading the bundle;
// rest names the files beyond the three every bundle carries, which is where a
// stage render and the whole pipeline differ.
func readLine(bundle, meta, rest string) string {
	return fmt.Sprintf("Read the installed verdict review skill first, then the bundle at "+
		"%s: %s, roles.txt, and toc.txt indexing full.diff, plus %s", bundle, meta, rest)
}

// stageDirective is the prompt a --print --stage render composes: the same
// subject and bundle contract, one stage's job instead of the whole pipeline,
// and the artifact the next stage reads. It names the review file as the
// previous stage's output so the stages chain through the one artifact record
// and post already consume.
func stageDirective(repo, pr, bundle, artifact string, local bool, plan *recipe.Plan, at *staged) string {
	subject, meta := subjectAndMeta(repo, pr, local)
	head := fmt.Sprintf("Review %s — stage %d of %d (%s) of recipe %s@%s (%s).",
		subject, at.index, len(plan.Stages), at.stage.Kind, plan.Name, plan.Version, plan.Source)
	read := readLine(bundle, meta, "kb.txt and sensitive.txt when the run carries them.")
	if at.index > 1 {
		read += fmt.Sprintf(" Read %s, the review the previous stage wrote.", artifact)
	}
	write := fmt.Sprintf("Write %s with {\"schema_version\": \"1\", \"body\": ..., "+
		"\"approved\": ..., \"comments\": [...]}. Write nothing else in the bundle.", artifact)
	boundary := "verdict rendered this one command and launches nothing for you: it does " +
		"not read your prose, retry you, post, or chain the stages. The later stages " +
		"read the file you write, so keep every value the earlier stages declared."
	return head + paragraphGap + read + paragraphGap + stageTask(at) + paragraphGap + write + paragraphGap + boundary + "\n"
}

// stageTask states the one job the rendered stage owns, with the hints and caps
// the recipe declared for it. Every value comes from the resolved plan, so a
// declaration no stage can carry renders nothing.
func stageTask(at *staged) string {
	switch at.stage.Kind {
	case recipe.StageLenses:
		return "Run the declared lenses as independent sub-agents, each honoring the " +
			"hint and caps listed for it: " + lensLine(at.stage.Lenses) +
			". Record their findings as comments; the later stages synthesize, dedupe, and rank."
	case recipe.StageSynthesize:
		hint := ""
		if at.stage.Model != "" {
			hint = " (model hint " + at.stage.Model + ")"
		}
		return "Synthesize one review from those findings: a single body whose first line " +
			"carries the verdict marker, the combined approved flag, and every comment kept " +
			"on its anchor" + hint + "."
	case recipe.StageDedupe:
		return "Merge duplicate findings: keep the strongest wording and one anchor per " +
			"finding, drop the redundant comments, and leave any count the body states consistent."
	}
	return "Rank the review: order comments by severity and blast radius, tag the body " +
		"sections, and set confidence. This file is the artifact verdict records or posts."
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
