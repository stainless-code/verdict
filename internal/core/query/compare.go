package query

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/shellquote"
)

// Compare composes one bundle's comparison against a baseline bundle: the
// report service's data under the compare flags, so `data` is byte-identical
// to `report --compare --json`'s data. A baseline is resolved to a bundle
// directory inside the same served root before anything reads it, and a bad
// baseline is a typed refusal, never a half-empty comparison.
func (l *Layer) Compare(ctx context.Context, rootID, bundle, baseline string) (*View, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return nil, ErrNotFound
	}
	// One listing answers both names: the target bundle and the baseline are
	// resolved against the same discovered set.
	names, _, _ := l.listBundles(root)
	set := bundleSet(names)
	dir, ok, ref := resolveAmong(root, bundle, set)
	if ref != nil {
		return nil, ref
	}
	if !ok {
		return nil, ErrNotFound
	}
	baseDir, ref := baselineAmong(root, baseline, set)
	if ref != nil {
		return nil, ref
	}
	flags := l.detailFlags()
	// The composition receives the resolved directory, never the query value:
	// the report service resolves a path spelling against the process working
	// directory, which is not a tree this process serves.
	flags.Compare = filepath.ToSlash(baseDir)
	flags.CompareSet = true
	resp, refusal, inv := l.cached(ctx, root, bundle, dir, baseDir, &flags)
	if refusal != nil {
		return nil, refusal
	}
	view := &View{
		At:       resp.At,
		Next:     reportCommand(bundleRepo(dir), bundle, filepath.Base(baseDir), l.check),
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		Warnings: detailWarnings(resp.Warnings, bundle, inv),
		Data:     normalizeReportData(root.Path, resp.Data),
		Sources:  inv.sources,
	}
	return view, nil
}

// baselineAmong resolves one compare baseline onto a bundle directory inside
// the same served root. The value arrives in a query string, so it gets the
// bundle-name guard every verb shares — an absolute path, a `..` segment, and a
// separator all refuse — and the same discovery the reported bundle goes
// through, so no composition reads a directory this root does not list. A name
// this root does not hold reads missing, not malformed, and a directory whose
// physical path leaves the root refuses: a symlinked state home must not widen
// what one served root exposes.
func baselineAmong(root Root, baseline string, names map[string]bool) (string, *core.RefusalError) {
	if _, ref := config.BundleDir(root.Path, baseline); ref != nil {
		return "", badBaseline(baseline, ref.Message)
	}
	dir, ok, ref := resolveAmong(root, baseline, names)
	switch {
	case ref != nil:
		return "", badBaseline(baseline, ref.Message)
	case !ok:
		return "", &core.RefusalError{
			Code: "missing-compare-bundle",
			Message: fmt.Sprintf("serve compare: baseline %q is not a bundle in this served root "+
				"(fetch, record, or post that bundle first)", baseline),
		}
	case !insideRoot(root.Path, dir):
		return "", badBaseline(baseline, "it resolves outside this served root")
	}
	return dir, nil
}

// badBaseline renders the one refusal a baseline value that cannot name a
// bundle in the served root receives.
func badBaseline(baseline, detail string) *core.RefusalError {
	return &core.RefusalError{
		Code: "bad-baseline",
		Message: fmt.Sprintf("serve compare: baseline %q must name a bundle in this served root: %s",
			baseline, detail),
	}
}

// insideRoot reports whether dir resolves to a path physically inside root: a
// symlinked component that leaves the root resolves outside it, so the answer
// is everything on disk and not just the spelling.
func insideRoot(root, dir string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realDir, err := filepath.EvalSymlinks(filepath.FromSlash(dir))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != parentDir && !strings.HasPrefix(rel, parentDir+string(filepath.Separator))
}

// reportCommand renders the one pastable invocation whose data the served
// comparison mirrors: the reported bundle, the baseline bundle, and the flags
// the layer composes with (`--cost`, the gate tier, the offline live posture).
// Every positional is shell-quoted, so a bundle name carrying a space still
// pastes back as one word.
func reportCommand(repo, bundle, baseline, check string) string {
	words := []string{
		"verdict", "report", repoArg(repo), shellquote.Arg(bundle),
		"--compare", shellquote.Arg(baseline), "--cost",
		"--check", shellquote.Arg(check), "--live", "skip", "--json",
	}
	return strings.Join(words, " ")
}

// Copy assembles one finding's copy payloads: the deterministic `Prompt for
// AI agent` block and the ready-to-run `verdict triage` command. The prompt
// is empty when the finding holds no brief; the triage command is always
// present. Read-only: nothing here writes or executes.
func (l *Layer) Copy(ctx context.Context, rootID, bundle, sig string) (*View, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return nil, ErrNotFound
	}
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		return nil, ref
	}
	if !ok {
		return nil, ErrNotFound
	}
	read := core.ReadRequest{
		Runtime:  l.runtimeFor(),
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		RepoRoot: root.Path,
	}
	resp, refusal := reportcore.RunShow(ctx, &core.ShowRequest{ReadRequest: read, Sig: sig})
	if refusal != nil {
		if refusal.Code == "unknown-sig" {
			// A copy of a finding the store does not hold is not found,
			// not a product refusal.
			return nil, ErrNotFound
		}
		//nolint:wrapcheck // the typed refusal is the product error the transport renders.
		return nil, refusal
	}
	rec, err := copyableRecord(resp)
	if err != nil {
		return nil, err
	}
	named := filepath.Base(filepath.FromSlash(dir))
	return &View{
		At:   resp.At,
		Repo: resp.Repo,
		Pr:   resp.Pr,
		Data: jq.NewObj(
			"sig", sig,
			"prompt", reviewdoc.RenderFixPrompt(rec),
			"triage", triageCommand(bundleRepo(dir), named, sig, rec),
		),
	}, nil
}

// copyableRecord reads the one store record a copy may publish. An absent
// finding and a policy-withheld one both answer not-found: the posted body
// never carries a withheld finding, so a payload here would publish review
// prose the policy withheld.
func copyableRecord(resp *core.ShowResponse) (*jq.Obj, error) {
	if resp.Item == nil {
		return nil, ErrNotFound
	}
	rec := resp.Item.Raw
	if readcommon.WithheldSource(rec) != "" {
		return nil, ErrNotFound
	}
	return rec, nil
}

// triageRationaleFloor is the triage service's rationale floor (rationaleMin
// in internal/core/triage): a copied command carrying fewer runes refuses
// rationale-required unless --force is present.
const triageRationaleFloor = 30

// defaultTriageRationale is the rationale a copied command carries when the
// finding's own claim text is absent or below the floor: 45 runes, above the
// floor, so the pasted command runs as printed without --force.
const defaultTriageRationale = "Finding reviewed and addressed in repository."

// triageRationale makes a finding's claim text safe to paste as triage's
// --rationale: triage needs at least triageRationaleFloor runes unless
// --force is passed, so a short or absent claim reads as the default instead
// of emitting a command that refuses on paste.
func triageRationale(rec *jq.Obj) string {
	text := firstText(rec, "claim", "body")
	if utf8.RuneCountInString(text) < triageRationaleFloor {
		return defaultTriageRationale
	}
	return text
}

// triageCommand renders the one pastable triage invocation for a finding:
// `verdict triage <repo> <pr> --sig <sig> --status addressed --rationale
// <claim>`. The verb takes the repo and pr positionals, so the copied line runs
// as printed from the repository root. Every value is shell-quoted, so an
// untrusted claim pastes back safe. The rationale always meets the service
// floor (see triageRationale), so the pasted command runs without --force.
func triageCommand(repo, bundle, sig string, rec *jq.Obj) string {
	words := []string{
		"verdict", "triage", repoArg(repo), shellquote.Arg(bundle),
		"--sig", shellquote.Arg(sig),
		"--status", "addressed",
		"--rationale", shellquote.Arg(triageRationale(rec)),
	}
	return strings.Join(words, " ")
}

// repoArg renders one command's repo positional: the bundle's own project when
// it names one, else the current checkout. Both verbs read the repo positional
// as a positional, so the empty word would shift the bundle into its place.
func repoArg(repo string) string {
	if repo == "" {
		return "."
	}
	return shellquote.Arg(repo)
}

func firstText(m *jq.Obj, keys ...string) string {
	for _, key := range keys {
		if value, ok := m.Get(key); ok {
			if text, ok := value.(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}
