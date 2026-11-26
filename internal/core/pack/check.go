package pack

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// CodeBadPath refuses a check path that is not a repository-relative slash
// path.
const CodeBadPath = "bad-path"

// ConfigSourceNone is the check row's config source when no trusted base
// configuration was read.
const ConfigSourceNone = "none"

// CheckRequest wires one pack check: the resolved layers, the trusted-base
// pack configuration, and the repository-relative path to resolve.
type CheckRequest struct {
	Path string
	Request
}

// CheckPack is one pack that participates in the path's resolution, in
// precedence order, with the lower packs its manifest may replace.
type CheckPack struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Layer   string   `enum:"builtin|org|repo" json:"layer"`
	Source  string   `json:"source"`
	Hash    string   `json:"hash"`
	Replace []string `json:"replace"`
}

// CheckLens is one lens offered for the path, tagged with the pack that won
// its id and the lower packs that same id shadowed.
type CheckLens struct {
	ID       string   `json:"id"`
	Pack     string   `json:"pack"`
	Layer    string   `enum:"builtin|org|repo" json:"layer"`
	Hash     string   `json:"hash"`
	Replaced []string `json:"replaced"`
}

// CheckRule is one rule whose globs match the path, tagged with the pack that
// won its id and the lower packs that same id shadowed.
type CheckRule struct {
	ID       string   `json:"id"`
	Severity string   `enum:"critical|important|informational" json:"severity"`
	Pack     string   `json:"pack"`
	Layer    string   `enum:"builtin|org|repo"                 json:"layer"`
	Globs    []string `json:"globs"`
	Replaced []string `json:"replaced"`
}

// CheckData is the pack check verb's --json data profile: the resolution of
// one path through the same layers, merge, and globs fetch uses.
type CheckData struct {
	Path     string      `json:"path"`
	Config   string      `json:"config"`
	Disabled []string    `json:"disabled"`
	Packs    []CheckPack `json:"packs"`
	Lenses   []CheckLens `json:"lenses"`
	Rules    []CheckRule `json:"rules"`
}

// Wire renders the data profile in published wire order.
func (d *CheckData) Wire() *jq.Obj {
	if d == nil {
		return jq.NewObj("path", "", "config", ConfigSourceNone, "disabled", []any{},
			"packs", []any{}, "lenses", []any{}, "rules", []any{})
	}
	return jq.NewObj(
		"path", d.Path, "config", d.Config, "disabled", core.WireStrings(d.Disabled),
		"packs", core.Wire(d.Packs), "lenses", core.Wire(d.Lenses), "rules", core.Wire(d.Rules),
	)
}

// CheckResponse carries the envelope inputs and the human rows.
//
//nolint:govet // the envelope fields read in render order
type CheckResponse struct {
	At       string
	Next     string
	Warnings []string
	Human    []string
	Data     *CheckData
}

// RunCheck resolves one repository-relative path through the built-in, org,
// and repo layers, the base-owned pack toggles, and the same globs a review
// dispatches on. It writes nothing.
func RunCheck(ctx context.Context, req *CheckRequest) (*CheckResponse, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: canceledCode, Message: "canceled before pack check completed"}
	}
	checked, ref := normalizeCheckPath(req.Path)
	if ref != nil {
		return nil, ref
	}
	origins, ref := loadLayers(ctx, &req.Request)
	if ref != nil {
		return nil, ref
	}
	set, ref := Merge(origins.Origins)
	if ref != nil {
		ref.Warnings = origins.Warnings
		return nil, ref
	}
	for _, warning := range origins.Warnings {
		req.Warn(warning)
	}
	data := checkData(set, origins, checked, req.ConfigSource)
	return &CheckResponse{
		At: req.TimeUTC(),
		Next: "adjust packs.disable_packs, packs.disable_lenses, or packs.disable_rules " +
			"in the trusted-base .verdict/config.json to change this resolution",
		Warnings: origins.Warnings,
		Human:    checkRows(data),
		Data:     data,
	}, nil
}

// Path rejection bounds: a reviewed path is printable, so a control rune (NUL
// included) is never a Git spelling and is refused rather than handed to the
// glob matcher or a log line.
const (
	printableFloor = 0x20
	deleteRune     = 0x7f
)

// normalizeCheckPath refuses a path that cannot be a repository-relative
// member of the reviewed tree, and returns the cleaned form otherwise.
func normalizeCheckPath(raw string) (string, *core.RefusalError) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", badPath("pack check needs a repository-relative path, for example internal/core/pack/pack.go")
	}
	if strings.IndexFunc(trimmed, isControlRune) >= 0 {
		return "", badPath(fmt.Sprintf("path %q carries a control character; "+
			"pass a printable repository-relative slash path", raw))
	}
	if strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, `\:`) {
		return "", badPath(fmt.Sprintf("path %q is absolute or carries a separator outside "+
			"the Git spelling; pass a repository-relative slash path", raw))
	}
	cleaned := path.Clean(trimmed)
	if cleaned == rootSelf || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", badPath(fmt.Sprintf("path %q escapes the repository root; "+
			"pass a repository-relative slash path", raw))
	}
	return cleaned, nil
}

// isControlRune reports an ASCII control rune or DEL: neither can appear in a
// Git path spelling, and both would corrupt any line a renderer prints.
func isControlRune(r rune) bool {
	return r < printableFloor || r == deleteRune
}

// checkData assembles the ordered resolution for one path.
func checkData(set *Set, layers *loadedLayers, checked, source string) *CheckData {
	if source == "" {
		source = ConfigSourceNone
	}
	disabled := layers.Applied
	if disabled == nil {
		disabled = []string{}
	}
	prior := priorOwners(layers.Origins)
	return &CheckData{
		Path: checked, Config: source, Disabled: disabled,
		Packs:  checkPacks(layers.Origins),
		Lenses: checkLenses(set, []string{checked}, prior),
		Rules:  checkRules(set, []string{checked}, prior),
	}
}

// priorOwners records, per lens and rule id, the pack names that declared it
// before the merge winner, so a row shows what it shadowed.
func priorOwners(origins []Origin) map[string][]string {
	sorted := slices.Clone(origins)
	slices.SortStableFunc(sorted, func(a, b Origin) int { return a.Layer.Rank() - b.Layer.Rank() })
	out := map[string][]string{}
	for _, origin := range sorted {
		if origin.Pack == nil {
			continue
		}
		recordPrior(out, "lens:", origin.Pack.Lenses, func(lens Lens) string { return lens.ID }, origin.Pack.Name)
		recordPrior(out, "rule:", origin.Pack.Rules, func(rule Rule) string { return rule.ID }, origin.Pack.Name)
	}
	return out
}

// recordPrior appends one pack's declarations for one id namespace.
func recordPrior[T any](out map[string][]string, prefix string, rows []T, id func(T) string, packName string) {
	for _, row := range rows {
		key := prefix + id(row)
		owners := out[key]
		if len(owners) == 0 || owners[len(owners)-1] != packName {
			out[key] = append(owners, packName)
		}
	}
}

// checkPacks renders one row per loaded pack in precedence order.
func checkPacks(origins []Origin) []CheckPack {
	out := make([]CheckPack, 0, len(origins))
	for _, origin := range origins {
		if origin.Pack == nil {
			continue
		}
		replace := origin.Pack.Replace
		if replace == nil {
			replace = []string{}
		}
		out = append(out, CheckPack{
			Name: origin.Pack.Name, Version: origin.Pack.Version, Layer: string(origin.Layer),
			Source: displaySource(origin.Source), Hash: origin.Pack.Hash, Replace: replace,
		})
	}
	return out
}

// checkLenses renders the lenses offered for the path.
func checkLenses(set *Set, files []string, prior map[string][]string) []CheckLens {
	offered := set.Offered(files)
	out := make([]CheckLens, 0, len(offered))
	for i := range offered {
		lens := &offered[i]
		out = append(out, CheckLens{
			ID: lens.ID, Pack: lens.Pack, Layer: layerOf(set, lens.Pack), Hash: lens.Hash8,
			Replaced: shadowed(prior, "lens:"+lens.ID, lens.Pack),
		})
	}
	return out
}

// checkRules renders the rules whose globs match the path.
func checkRules(set *Set, files []string, prior map[string][]string) []CheckRule {
	matched := set.RulesFor(files)
	out := make([]CheckRule, 0, len(matched))
	for i := range matched {
		rule := &matched[i]
		out = append(out, CheckRule{
			ID: rule.ID, Severity: rule.Level, Pack: rule.Pack, Layer: layerOf(set, rule.Pack),
			Globs: rule.Globs, Replaced: shadowed(prior, "rule:"+rule.ID, rule.Pack),
		})
	}
	return out
}

// shadowed renders the packs one winner displaced, empty when it displaced
// none.
func shadowed(prior map[string][]string, key, winner string) []string {
	out := make([]string, 0, len(prior[key]))
	for _, name := range prior[key] {
		if name != winner {
			out = append(out, name)
		}
	}
	return out
}

// layerOf names the layer a winning pack was loaded from.
func layerOf(set *Set, packName string) string {
	for _, origin := range set.Packs {
		if origin.Pack != nil && origin.Pack.Name == packName {
			return string(origin.Layer)
		}
	}
	return ""
}

// checkRows renders the human report: the path, the config source, then one
// row per pack, offered lens, and matching rule.
func checkRows(data *CheckData) []string {
	rows := []string{
		"path     " + data.Path,
		"config   " + data.Config,
	}
	if len(data.Disabled) > 0 {
		rows = append(rows, "disabled "+strings.Join(data.Disabled, listSep))
	}
	rows = append(rows, "packs:")
	for i := range data.Packs {
		row := data.Packs[i]
		line := fmt.Sprintf("  %-7s  %s@%s  source=%s  sha256:%s", row.Layer, row.Name,
			row.Version, row.Source, shortHash(row.Hash))
		if len(row.Replace) > 0 {
			line += "  replaces=" + strings.Join(row.Replace, listSep)
		}
		rows = append(rows, line)
	}
	rows = append(rows, "lenses:")
	rows = append(rows, checkLensRows(data.Lenses)...)
	rows = append(rows, "rules:")
	return append(rows, checkRuleRows(data.Rules)...)
}

// checkLensRows renders one line per offered lens, or the none row.
func checkLensRows(lenses []CheckLens) []string {
	if len(lenses) == 0 {
		return []string{"  no lens is offered for this path"}
	}
	rows := make([]string, 0, len(lenses))
	for i := range lenses {
		lens := &lenses[i]
		line := fmt.Sprintf("  %s  pack=%s  layer=%s  sha256:%s", lens.ID, lens.Pack, lens.Layer, lens.Hash)
		if len(lens.Replaced) > 0 {
			line += "  replaced=" + strings.Join(lens.Replaced, listSep)
		}
		rows = append(rows, line)
	}
	return rows
}

// checkRuleRows renders one line per matching rule, or the none row.
func checkRuleRows(rules []CheckRule) []string {
	if len(rules) == 0 {
		return []string{"  no rule's globs match this path"}
	}
	rows := make([]string, 0, len(rules))
	for i := range rules {
		rule := &rules[i]
		line := fmt.Sprintf("  %-32s  severity=%s  pack=%s  layer=%s  globs=%s",
			rule.ID, rule.Severity, rule.Pack, rule.Layer, strings.Join(rule.Globs, listSep))
		if len(rule.Replaced) > 0 {
			line += "  replaced=" + strings.Join(rule.Replaced, listSep)
		}
		rows = append(rows, line)
	}
	return rows
}

// badPath builds the bad-path refusal.
func badPath(reason string) *core.RefusalError {
	return &core.RefusalError{Code: CodeBadPath, Message: core.Msgf("%s", reason)}
}
