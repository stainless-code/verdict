package query

import (
	"context"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/shellquote"
)

// nextCompare names the CLI invocation the served comparison mirrors.
const nextCompare = "run verdict report --compare <baseline> --json to compare against another bundle"

// Compare composes one bundle's comparison against a baseline bundle: the
// report service's data under the compare flags, so `data` is byte-identical
// to `report --compare --json`'s data. A bad baseline is the report service's
// own typed refusal, never a half-empty comparison.
func (l *Layer) Compare(ctx context.Context, rootID, bundle, baseline string) (*View, error) {
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
	flags := l.detailFlags()
	flags.Compare = baseline
	flags.CompareSet = true
	resp, refusal, inv := l.cached(ctx, root, bundle, dir, &flags)
	if refusal != nil {
		return nil, refusal
	}
	view := &View{
		At:       resp.At,
		Next:     nextCompare,
		Repo:     bundleRepo(dir),
		Pr:       bundle,
		Warnings: detailWarnings(resp.Warnings, bundle, inv),
		Data:     normalizeReportData(root.Path, resp.Data),
		Sources:  inv.sources,
	}
	return view, nil
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
	if resp.Item == nil {
		return nil, ErrNotFound
	}
	rec := resp.Item.Raw
	return &View{
		At:   resp.At,
		Repo: resp.Repo,
		Pr:   resp.Pr,
		Data: jq.NewObj(
			"sig", sig,
			"prompt", reviewdoc.RenderFixPrompt(rec),
			"triage", triageCommand(bundleDir(bundle), sig, rec),
		),
	}, nil
}

// triageCommand renders the one pastable triage invocation for a finding:
// `verdict triage <dir> --sig <sig> --status addressed --rationale <claim>`.
// Every value is shell-quoted, so an untrusted claim pastes back safe. A
// finding with no claim text still pastes: the rationale names the action.
func triageCommand(dir, sig string, rec *jq.Obj) string {
	rationale := firstText(rec, "claim", "body")
	if rationale == "" {
		rationale = "reviewed and addressed"
	}
	words := []string{
		"verdict", "triage",
		shellquote.Arg(dir), "--sig", shellquote.Arg(sig),
		"--status", "addressed",
		"--rationale", shellquote.Arg(rationale),
	}
	return strings.Join(words, " ")
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
