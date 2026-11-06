package config

import (
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Inputs is the explicit resolver input: every layer stated, no hidden
// reads. Flags and Env are operator invocation choices; Repo is the
// trusted-base configuration (nil without base policy); Personal is the
// operator settings layer (nil when absent); BaseSHA labels provenance;
// Files lists the review paths with pre-rename paths where the diff
// renamed them; SettingsPath records the personal file read, if any.
type Inputs struct {
	Repo         *core.Config
	Personal     *PersonalSettings
	BaseSHA      string
	SettingsPath string
	Files        []FileInput
	Flags        Partial
	Env          Partial
}

// Resolved is the effective configuration: merged defaults, per-file
// resolutions with clause provenance, the combined plan, and the spend
// and privacy constraints.
type Resolved struct {
	Spend    SpendView
	Plan     Plan
	Files    []FileResolution
	Privacy  PrivacyView
	Defaults ResolvedDefaults
}

// SpendView separates the two evaluations: the ordinary estimate guard
// against the resolved budget_usd default, and the personal authorization
// limit the repository can never raise.
type SpendView struct {
	GuardUSD             *float64
	PersonalLimit        *float64
	RequireKnownEstimate bool
	EnforceCap           bool
}

// PrivacyView is the exclusion constraint fetch enforces before any
// model-facing export is produced.
type PrivacyView struct {
	Exclusions []string
}

// Excluded reports whether the repository-relative path is withheld from
// model-facing exports.
func (p PrivacyView) Excluded(path string) bool {
	for _, glob := range p.Exclusions {
		if Match(glob, path) {
			return true
		}
	}
	return false
}

// FilterPaths omits excluded paths, preserving order.
func (p PrivacyView) FilterPaths(paths []string) []string {
	var out []string
	for _, path := range paths {
		if !p.Excluded(path) {
			out = append(out, path)
		}
	}
	return out
}

// PlanGroup is one execution group: paths sharing identical effective
// defaults, requirements, and profile.
type PlanGroup struct {
	Profile   string
	Depth     string
	Checks    []string
	MaxLenses *int
	Files     []string
	Scopes    []string
}

// Plan is the combined plan: one entry per group with its profile, depth,
// checks, lens budget, and file list. Status stays pending until
// execution evidence exists.
type Plan struct {
	EffortTier string
	Status     string
	Groups     []PlanGroup
}

// configSourceName is the tracked repository configuration path.
const configSourceName = ".verdict/config.json"

// listSep joins grouping-key lists.
const listSep = ","

// Resolve maps stated inputs to effective values, origins, shadowed
// values, scopes, clauses, and the pending plan. It is pure: no
// filesystem, process, or network access.
func Resolve(in *Inputs) (*Resolved, *core.RefusalError) {
	personal := in.Personal
	if personal == nil {
		personal = &PersonalSettings{}
	}
	repo, review := repoDefaults(in.Repo), personal.Review
	global := mergePartials(globalLayers(&in.Flags, &in.Env, &repo, &review))
	root := rootRequirements(requirementsOf(in.Repo), in.BaseSHA)
	out := &Resolved{Defaults: global}
	for _, file := range in.Files {
		resolution := resolveFile(in, &root, file)
		out.Files = append(out.Files, resolution)
	}
	plan, ref := buildPlan(in, &global, out.Files)
	if ref != nil {
		return nil, ref
	}
	out.Plan = plan
	out.Spend = SpendView{
		GuardUSD:             global.Values.BudgetUSD,
		PersonalLimit:        personal.Spend.BudgetUSD,
		RequireKnownEstimate: personal.Spend.RequireKnownEstimate,
		EnforceCap:           personal.Spend.EnforceCap,
	}
	out.Privacy = PrivacyView{Exclusions: slices.Clone(personal.Privacy.Exclusions)}
	return out, nil
}

// globalLayers orders the invocation and shared layers: flags, then each
// set environment field with its variable name, then the repository
// shared defaults, the personal review defaults, and the built-ins.
func globalLayers(flags, env, repo, personal *Partial) []layerValue {
	layers := []layerValue{{partial: *flags, layer: LayerFlag, source: "command line"}}
	layers = append(layers, envLayers(env)...)
	layers = append(layers,
		layerValue{partial: *repo, layer: LayerRepo, source: configSourceName},
		layerValue{partial: *personal, layer: LayerPersonal, source: "operator settings"},
		layerValue{partial: Builtins(), layer: LayerBuiltIn, source: LayerBuiltIn},
	)
	return layers
}

// envLayers splits the environment partial so each field carries the
// variable that supplied it. Only consumed names appear; the resolver
// never sees the whole environment.
func envLayers(env *Partial) []layerValue {
	var layers []layerValue
	if env.PanelSet {
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_PANEL", partial: Partial{Panel: env.Panel, PanelSet: true}})
	}
	if env.BudgetUSD != nil {
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_BUDGET_USD", partial: Partial{BudgetUSD: env.BudgetUSD}})
	}
	if env.MaxBytes != nil {
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_MAX_BYTES", partial: Partial{MaxBytes: env.MaxBytes}})
	}
	if env.KBPolicySet && env.KBMinJudged != nil {
		partial := Partial{KBMinJudged: env.KBMinJudged, KBPolicySet: true}
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_KB_MIN_JUDGED", partial: partial})
	}
	if env.KBPolicySet && env.KBMaxFPRate != nil {
		partial := Partial{KBMaxFPRate: env.KBMaxFPRate, KBPolicySet: true}
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_KB_MAX_FP_RATE", partial: partial})
	}
	return layers
}

// repoDefaults lifts the shared defaults out of the trusted configuration.
func repoDefaults(cfg *core.Config) Partial {
	if cfg == nil || cfg.Defaults == nil {
		return Partial{}
	}
	return partialFromDefaults(cfg.Defaults)
}

// requirementsOf lifts the shared requirements out of the trusted
// configuration.
func requirementsOf(cfg *core.Config) *core.ConfigRequirements {
	if cfg == nil {
		return nil
	}
	return cfg.Requirements
}

// resolveFile resolves one path: the matching scopes in file order refine
// defaults field-by-field and accumulate requirements, renames match both
// paths, and the profile resolves to the last scope naming one.
func resolveFile(in *Inputs, root *RequirementSet, file FileInput) FileResolution {
	var scopes []core.ConfigScope
	if in.Repo != nil {
		scopes = in.Repo.Scopes
	}
	matched, oldOnly := matchingScopes(scopes, file)
	layers := fileLayers(in, matched)
	reqs := cloneSet(root)
	names := make([]string, 0, len(matched))
	profile := ""
	for i := range matched {
		names = append(names, matched[i].Name)
		source := scopeSource(matched[i].Name, file.OldPath, oldOnly[i])
		reqs.accumulate(matched[i].Requirements, source)
		if matched[i].Profile != nil {
			profile = *matched[i].Profile
		}
	}
	if profile == "" && in.Repo != nil && in.Repo.DefaultProfile != "" {
		profile = in.Repo.DefaultProfile
	}
	return FileResolution{
		Path:         file.Path,
		OldPath:      file.OldPath,
		Scopes:       names,
		Defaults:     mergePartials(layers),
		Requirements: reqs,
		Profile:      profile,
	}
}

// fileLayers orders one file's layers: invocation first, then the
// matching scopes with the later scope winning, then the shared,
// personal, and built-in floors.
func fileLayers(in *Inputs, matched []core.ConfigScope) []layerValue {
	layers := []layerValue{{partial: in.Flags, layer: LayerFlag, source: "command line"}}
	layers = append(layers, envLayers(&in.Env)...)
	for _, scope := range slices.Backward(matched) {
		if scope.Defaults == nil {
			continue
		}
		name := scope.Name
		layers = append(layers, layerValue{partial: partialFromDefaults(scope.Defaults), layer: LayerScope, source: "scope " + name})
	}
	repo, review := repoDefaults(in.Repo), personalReview(in.Personal)
	layers = append(layers,
		layerValue{partial: repo, layer: LayerRepo, source: configSourceName},
		layerValue{partial: review, layer: LayerPersonal, source: "operator settings"},
		layerValue{partial: Builtins(), layer: LayerBuiltIn, source: LayerBuiltIn},
	)
	return layers
}

// personalReview lifts the personal review defaults, tolerating absence.
func personalReview(personal *PersonalSettings) Partial {
	if personal == nil {
		return Partial{}
	}
	return personal.Review
}

// effectiveProfile applies the friction derivation: requirements always
// add mandatory work to the planned checks, unioning the profile's checks
// with the accumulated required checks instead of refusing when a
// requirement-only scope adds another check. Depth defaults to standard;
// max_lenses stays unset when unconfigured, meaning no configured cap.
func effectiveProfile(prof core.ConfigProfile, required []string) (string, []string, *int) {
	depth := BuiltinDepth
	if prof.Depth != nil {
		depth = *prof.Depth
	}
	planned := slices.Clone(prof.Checks)
	for _, check := range required {
		if !slices.Contains(planned, check) {
			planned = append(planned, check)
		}
	}
	var maxLenses *int
	if prof.MaxLenses != nil {
		copied := *prof.MaxLenses
		maxLenses = &copied
	}
	return depth, planned, maxLenses
}

// cloneSet deep-copies a requirement set so per-file accumulation never
// shares backing arrays with the root set.
func cloneSet(set *RequirementSet) RequirementSet {
	out := RequirementSet{
		RequireBind: set.RequireBind,
		BindSources: slices.Clone(set.BindSources),
		Checks:      slices.Clone(set.Checks),
	}
	if set.CheckOrigin != nil {
		out.CheckOrigin = make(map[string][]string, len(set.CheckOrigin))
		for check, sources := range set.CheckOrigin {
			out.CheckOrigin[check] = slices.Clone(sources)
		}
	}
	out.Clauses = make([]ClauseProvenance, 0, len(set.Clauses))
	for i := range set.Clauses {
		out.Clauses = append(out.Clauses, ClauseProvenance{Clause: set.Clauses[i].Clause, Sources: slices.Clone(set.Clauses[i].Sources)})
	}
	return out
}

// buildPlan groups files with identical effective defaults, requirements,
// and profile, then validates each group: a group whose accumulated
// checks include no-open-above-block needs at least one clause applying
// to its paths, else resolution refuses bad-requirement naming the group.
// The whole-review effort tier reads the shared global resolution.
func buildPlan(in *Inputs, global *ResolvedDefaults, files []FileResolution) (Plan, *core.RefusalError) {
	plan := Plan{EffortTier: global.Values.Effort, Status: PendingStatus}
	keys := make([]string, 0, len(files))
	for i := range files {
		depth, checks, maxLenses := plannedProfile(in, &files[i])
		key := groupKey(&files[i], depth, checks, maxLenses)
		at := slices.Index(keys, key)
		if at < 0 {
			keys = append(keys, key)
			plan.Groups = append(plan.Groups, PlanGroup{
				Profile:   files[i].Profile,
				Depth:     depth,
				Checks:    checks,
				MaxLenses: maxLenses,
				Files:     []string{files[i].Path},
				Scopes:    slices.Clone(files[i].Scopes),
			})
			continue
		}
		plan.Groups[at].Files = append(plan.Groups[at].Files, files[i].Path)
		plan.Groups[at].Scopes = unionOrdered(plan.Groups[at].Scopes, files[i].Scopes)
	}
	for i := range plan.Groups {
		if ref := validateGroup(&plan.Groups[i], files, i+1); ref != nil {
			return Plan{}, ref
		}
	}
	return plan, nil
}

// plannedProfile recomputes the effective profile triple for grouping.
func plannedProfile(in *Inputs, file *FileResolution) (string, []string, *int) {
	if in.Repo != nil && file.Profile != "" {
		if prof, ok := in.Repo.Profiles[file.Profile]; ok {
			return effectiveProfile(prof, file.Requirements.Checks)
		}
	}
	return effectiveProfile(core.ConfigProfile{}, file.Requirements.Checks)
}

// groupKey serializes the grouping identity: effective defaults,
// requirement content, and profile triple. Provenance is deliberately
// excluded: identical content groups together while per-file sources stay
// on the file resolutions.
func groupKey(file *FileResolution, depth string, checks []string, maxLenses *int) string {
	var sb strings.Builder
	writeDefaultsKey(&sb, &file.Defaults.Values)
	writeRequirementsKey(&sb, &file.Requirements)
	writeProfileKey(&sb, file.Profile, depth, checks, maxLenses)
	return sb.String()
}

// writeDefaultsKey serializes the merged defaults into the grouping key.
func writeDefaultsKey(sb *strings.Builder, defs *EffectiveDefaults) {
	sb.WriteString("panel=")
	sb.WriteString(strings.Join(defs.Panel, listSep))
	sb.WriteString("|budget=")
	sb.WriteString(floatText(defs.BudgetUSD))
	sb.WriteString("|maxbytes=")
	sb.WriteString(strconv.FormatInt(defs.MaxBytes, decimalBase))
	sb.WriteString("|chunk=")
	sb.WriteString(strconv.FormatBool(defs.Chunk))
	sb.WriteString("|kb=")
	sb.WriteString(strconv.Itoa(defs.KBMinJudged))
	sb.WriteString("/")
	sb.WriteString(strconv.FormatFloat(defs.KBMaxFPRate, 'g', -1, floatBits))
	sb.WriteString("|effort=")
	sb.WriteString(defs.Effort)
	sb.WriteString("|display=")
	sb.WriteString(strText(defs.DisplayMinSeverity))
	sb.WriteString("|post=")
	sb.WriteString(strText(defs.PostMinSeverity))
	sb.WriteString("|")
}

// writeRequirementsKey serializes the requirement content (not its
// provenance) into the grouping key.
func writeRequirementsKey(sb *strings.Builder, reqs *RequirementSet) {
	for i := range reqs.Clauses {
		clause := &reqs.Clauses[i].Clause
		sb.WriteString("clause=")
		sb.WriteString(clause.Name)
		sb.WriteString("/")
		sb.WriteString(clause.Threshold)
		sb.WriteString("/")
		sb.WriteString(strings.Join(sortedJoin(clause.Categories, clause.Paths), listSep))
		sb.WriteString("|")
	}
	sb.WriteString("checks=")
	sb.WriteString(strings.Join(reqs.Checks, listSep))
	sb.WriteString("|bind=")
	sb.WriteString(strconv.FormatBool(reqs.RequireBind))
	sb.WriteString("|")
}

// writeProfileKey serializes the profile triple into the grouping key.
func writeProfileKey(sb *strings.Builder, profile, depth string, checks []string, maxLenses *int) {
	sb.WriteString("profile=")
	sb.WriteString(profile)
	sb.WriteString("|depth=")
	sb.WriteString(depth)
	sb.WriteString("|planned=")
	sb.WriteString(strings.Join(checks, listSep))
	sb.WriteString("|lenses=")
	sb.WriteString(intText(maxLenses))
}

// sortedJoin sorts category and path masks together for key stability.
func sortedJoin(categories, paths []string) []string {
	joined := make([]string, 0, len(categories)+len(paths))
	joined = append(joined, categories...)
	joined = append(joined, paths...)
	slices.Sort(joined)
	return joined
}

// validateGroup enforces the per-group clause rule: the findings check is
// meaningless without a clause applying to the group's paths.
func validateGroup(group *PlanGroup, files []FileResolution, number int) *core.RefusalError {
	if !slices.Contains(group.Checks, CheckNoOpenAboveBlock) {
		return nil
	}
	if groupHasClause(group, files) {
		return nil
	}
	return refuse(CodeBadRequirement, "plan group %d (profile %q): require_checks includes %q but no clause applies to the group's paths",
		number, group.Profile, CheckNoOpenAboveBlock)
}

// groupHasClause reports whether any clause in the group's requirement
// set applies to one of the group's file paths, old or new.
func groupHasClause(group *PlanGroup, files []FileResolution) bool {
	for i := range files {
		if fileCoversGroup(group, &files[i]) {
			return true
		}
	}
	return false
}

// fileCoversGroup reports whether one file belongs to the group and
// carries an applicable clause.
func fileCoversGroup(group *PlanGroup, file *FileResolution) bool {
	if !slices.Contains(group.Files, file.Path) {
		return false
	}
	member := FileInput{Path: file.Path, OldPath: file.OldPath}
	for j := range file.Requirements.Clauses {
		if clauseApplies(&file.Requirements.Clauses[j].Clause, member) {
			return true
		}
	}
	return false
}

// unionOrdered appends unseen names preserving order.
func unionOrdered(into, names []string) []string {
	for _, name := range names {
		if !slices.Contains(into, name) {
			into = append(into, name)
		}
	}
	return into
}

// floatText renders an optional float for group keys.
func floatText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, floatBits)
}

// strText renders an optional string for group keys.
func strText(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// intText renders an optional int for group keys.
func intText(v *int) string {
	if v == nil {
		return ""
	}
	return itoa(*v)
}
