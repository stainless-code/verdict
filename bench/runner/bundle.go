package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// writeBundle lays out one corpus bundle: the manifest the agent never sees,
// the meta and diff it does, its review, its spend record, and the controls
// snapshot record re-verifies.
func (r *runner) writeBundle(ctx context.Context, cr *caseRun, bundle string, source []byte) error {
	if err := os.MkdirAll(bundle, dirPerm); err != nil {
		return jq.NewErrf("bundle: %s", err)
	}
	review, err := os.ReadFile(filepath.Join(cr.dir, reviewJSONName))
	if err != nil {
		return jq.NewErrf("bundle: %s", err)
	}
	spend, err := r.spendRecord(cr)
	if err != nil {
		return err
	}
	files := []bundleFile{
		{fullDiffName, cr.corpus.diff},
		{hunksTSVName, []byte(hunks.Snapshot(string(cr.corpus.diff)))},
		{corpusJSONName, cr.corpus.manifest},
		{sourceJSONName, source},
		{panelName, []byte(strings.Join(r.cfg.Panel, "\n") + "\n")},
		{reviewJSONName, review},
		{costJSONName, spend},
	}
	if err := writeFiles(bundle, files); err != nil {
		return err
	}
	base, err := sourceBase(source)
	if err != nil {
		return err
	}
	if _, ref := controls.Resolve(ctx, nil, &controls.Request{BundleDir: bundle, BaseSHA: base, AllowHead: true}); ref != nil {
		return jq.NewErrf("controls snapshot: %s", ref)
	}
	return nil
}

// bundleFile is one artifact written into a bundle dir.
type bundleFile struct {
	name string
	body []byte
}

func writeFiles(bundle string, files []bundleFile) error {
	for _, f := range files {
		if err := atomic.WriteFile(filepath.Join(bundle, f.name), f.body); err != nil {
			return jq.NewErrf("bundle %s: %s", f.name, err)
		}
	}
	return nil
}

// spendRecord is the bundle's cost.json: the agent's own report with this
// run's model and wall clock pinned. record takes the model from models[0], so
// a report that forgot the name would leave the bundle unattributable.
func (r *runner) spendRecord(cr *caseRun) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(cr.dir, costJSONName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, jq.NewErrf("cost.json: %s", err)
	}
	return normalizeCost(raw, r.cfg.Model, cr.wall)
}

func normalizeCost(raw []byte, model string, wall float64) ([]byte, error) {
	rec := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, jq.NewErrf("cost.json: want one spend record object: %s", err)
		}
	}
	rec["schema_version"] = "1"
	if _, ok := rec["effort"]; !ok {
		rec["effort"] = "standard"
	}
	rec["models"] = withModel(rec["models"], model)
	rec["wall_seconds"] = roundMillis(wall)
	doc, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, jq.NewErrf("cost.json: %s", err)
	}
	return append(doc, '\n'), nil
}

// withModel puts this run's model first in the record's model list, keeping
// whatever the agent reported for it.
func withModel(existing any, model string) []any {
	models, ok := existing.([]any)
	if !ok {
		models = nil
	}
	if len(models) > 0 {
		if first, ok := models[0].(map[string]any); ok && first["name"] == model {
			return models
		}
	}
	return append([]any{map[string]any{"name": model}}, models...)
}

func sourceBase(source []byte) (string, error) {
	var meta struct {
		BaseSHA string `json:"base_sha"`
	}
	if err := json.Unmarshal(source, &meta); err != nil {
		return "", jq.NewErrf("source.json: %s", err)
	}
	if meta.BaseSHA == "" {
		return "", jq.NewErr("source.json: no base_sha")
	}
	return meta.BaseSHA, nil
}

// recordStamp is the provenance record.json carries: the prompts the run was
// recorded under and the model the bundle is attributed to.
type recordStamp struct {
	Model       string `json:"model"`
	PromptsHash string `json:"prompts_hash"`
}

func readRecordStamp(path string) (recordStamp, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return recordStamp{}, jq.NewErrf("record.json: %s", err)
	}
	var stamp recordStamp
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return recordStamp{}, jq.NewErrf("record.json: %s", err)
	}
	return stamp, nil
}

// repoState reports the corpus commit and whether the corpus or the prompt set
// is dirty. Published paths only: the results file itself never counts as a
// corpus change.
func repoState(ctx context.Context, root string) (string, bool) {
	commit, err := gitOut(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", false
	}
	published := []string{
		filepath.Join("bench", "corpus"),
		filepath.Join(prefixPrompts, "SKILL.md"),
		filepath.Join(prefixPrompts, "prompts"),
	}
	status, err := gitOut(ctx, root, append([]string{"status", "--porcelain", "--"}, published...)...)
	return commit, err == nil && strings.TrimSpace(status) != ""
}

func gitOut(ctx context.Context, root string, argv ...string) (string, error) {
	res, err := coreexec.Runner{}.Run(ctx, core.Command{Argv: append([]string{"git", "-C", root}, argv...), Env: toolEnv()})
	if err != nil {
		return "", jq.NewErrf("git %s: %s", argv[0], err)
	}
	if res.ExitCode != 0 {
		return "", jq.NewErrf("git %s: exit %d", argv[0], res.ExitCode)
	}
	return strings.TrimSpace(res.Stdout), nil
}
