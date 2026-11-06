package config

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
)

// Inputs is the explicit resolver input: every layer stated, no hidden
// reads. Flags and Env are operator invocation choices; Repo is the
// trusted-base configuration (nil without base policy); Personal is the
// operator settings layer (nil when absent); BaseSHA labels provenance;
// Files lists the review paths with pre-rename paths where the diff
// renamed them; SettingsPath records the personal file read, if any.
// Legacy `.review/` knob files are never a layer: the typed contract
// ignores them under every name.
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
	Spend    SpendView        `json:"spend"`
	Plan     Plan             `json:"plan"`
	Files    []FileResolution `json:"files"`
	Privacy  PrivacyView      `json:"privacy"`
	Defaults ResolvedDefaults `json:"defaults"`
}

// SpendView separates the two evaluations: the ordinary estimate guard
// against the resolved budget_usd default, and the personal authorization
// limit the repository can never raise.
type SpendView struct {
	GuardUSD             *float64 `json:"guard_usd,omitempty"`
	PersonalLimit        *float64 `json:"personal_limit,omitempty"`
	RequireKnownEstimate bool     `json:"require_known_estimate"`
	EnforceCap           bool     `json:"enforce_cap"`
}

// PrivacyView is the exclusion constraint fetch enforces before any
// model-facing export is produced.
type PrivacyView struct {
	Exclusions []string `json:"exclusions"`
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
// defaults, requirements, and profile. ID is the stable identity every
// observation and refusal names; Label is the explanation label and never
// an identity.
type PlanGroup struct { //nolint:govet // field order is the captured wire order
	Profile   string   `json:"profile"`
	Depth     string   `json:"depth"`
	Checks    []string `json:"checks"`
	MaxLenses *int     `json:"max_lenses,omitempty"`
	Files     []string `json:"files"`
	Scopes    []string `json:"scopes"`
	ID        string   `json:"id"`
	Label     string   `json:"label"`
}

// Plan is the combined plan: one entry per group with its profile, depth,
// checks, lens budget, and file list. Status stays pending until
// execution evidence exists.
type Plan struct {
	EffortTier string      `json:"effort_tier"`
	Status     string      `json:"status"`
	Groups     []PlanGroup `json:"groups"`
}

// configSourceName is the tracked repository configuration path.
const configSourceName = ".verdict/config.json"

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
		partial := Partial{BudgetUSD: env.BudgetUSD, BudgetDisplay: env.BudgetDisplay}
		layers = append(layers, layerValue{layer: LayerEnv, source: "REVIEW_BUDGET_USD", partial: partial})
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
		// keys stays index-parallel to Groups: both grow only in the
		// new-group branch above, so one key names one group.
		plan.Groups[i].ID = groupIdentity(keys[i], plan.Groups[i].Files)
		plan.Groups[i].Label = groupLabel(plan.Groups[i].Profile, i)
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
// requirement content, and profile triple. Provenance is excluded so
// identical content groups together while per-file sources stay on the file
// resolutions; counts and length prefixes keep a path, mask, or profile byte
// from shifting a field boundary.
func groupKey(file *FileResolution, depth string, checks []string, maxLenses *int) string {
	w := &identityWriter{}
	w.text("defaults")
	w.defaults(&file.Defaults.Values)
	w.text("requirements")
	w.requirements(&file.Requirements)
	w.text("profile")
	w.profile(file.Profile, depth, checks, maxLenses)
	return w.String()
}

// identityWriter builds the deterministic, unambiguous encoding the group
// identity hashes: counts and length prefixes, never delimiters.
type identityWriter struct{ buf []byte }

// String returns the encoded bytes.
func (w *identityWriter) String() string { return string(w.buf) }

func (w *identityWriter) text(field string) {
	w.buf = strconv.AppendInt(w.buf, int64(len(field)), decimalBase)
	w.buf = append(w.buf, ':')
	w.buf = append(w.buf, field...)
}

func (w *identityWriter) list(items []string) {
	w.buf = strconv.AppendInt(w.buf, int64(len(items)), decimalBase)
	w.buf = append(w.buf, '[')
	for _, item := range items {
		w.text(item)
	}
}

func (w *identityWriter) truth(v bool) {
	if v {
		w.text("true")
		return
	}
	w.text("false")
}

func (w *identityWriter) defaults(defs *EffectiveDefaults) {
	w.text("panel")
	w.list(defs.Panel)
	w.text("budget")
	w.text(floatText(defs.BudgetUSD))
	w.text("max_bytes")
	w.text(strconv.FormatInt(defs.MaxBytes, decimalBase))
	w.text("chunk")
	w.truth(defs.Chunk)
	w.text("kb_min_judged")
	w.text(strconv.Itoa(defs.KBMinJudged))
	w.text("kb_max_fp_rate")
	w.text(strconv.FormatFloat(defs.KBMaxFPRate, 'g', -1, floatBits))
	w.text("effort")
	w.text(defs.Effort)
	w.text("display_min_severity")
	w.text(strText(defs.DisplayMinSeverity))
	w.text("post_min_severity")
	w.text(strText(defs.PostMinSeverity))
}

// requirements keeps the path and category masks in separate fields: a
// category name is not a path.
func (w *identityWriter) requirements(reqs *RequirementSet) {
	w.text("clauses")
	w.buf = strconv.AppendInt(w.buf, int64(len(reqs.Clauses)), decimalBase)
	w.buf = append(w.buf, '[')
	for i := range reqs.Clauses {
		clause := &reqs.Clauses[i].Clause
		w.text(clause.Name)
		w.text(clause.Threshold)
		w.text("categories")
		w.list(sortedClone(clause.Categories))
		w.text("paths")
		w.list(sortedClone(clause.Paths))
	}
	w.text("checks")
	w.list(reqs.Checks)
	w.text("bind")
	w.truth(reqs.RequireBind)
}

func (w *identityWriter) profile(profile, depth string, checks []string, maxLenses *int) {
	w.text("name")
	w.text(profile)
	w.text("depth")
	w.text(depth)
	w.text("planned")
	w.list(checks)
	w.text("lenses")
	w.text(intText(maxLenses))
}

// groupIdentity hashes the normalized group identity with the full SHA-256:
// the effective work together with the sorted path membership. Distinct
// inputs encode to distinct digest input, so two groups that differ in work
// or paths cannot alias through the encoding, even when they share a label.
func groupIdentity(work string, files []string) string {
	sorted := sortedClone(files)
	w := &identityWriter{}
	w.text("work")
	w.text(work)
	w.text("files")
	w.list(sorted)
	sum := sha256.Sum256(w.buf)
	return hex.EncodeToString(sum[:])
}

// groupLabel is the explanation label for one group: the resolved profile
// name when there is one, else its position. Labels are display-only; the
// identity is ID.
func groupLabel(profile string, index int) string {
	if profile != "" {
		return profile
	}
	return "group " + strconv.Itoa(index+1)
}

func sortedClone(items []string) []string {
	out := slices.Clone(items)
	slices.Sort(out)
	return out
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
