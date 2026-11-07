package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
)

// LensUsage is the evaluated, group-attributed lens evidence from one run
// bundle. Every field is agent self-report: the CLI never observed the
// dispatches, so a clean verdict is attestation, not tool proof.
type LensUsage struct {
	State string `json:"state"`
	// Problem is the actionable sentence behind a refused record: which
	// field or version, and what fixes it. It is empty when the record was
	// refused for a reason that names no single cause.
	Problem   string           `json:"problem,omitempty"`
	Groups    []LensGroupUsage `json:"groups"`
	Unknown   []string         `json:"unknown,omitempty"`
	Ambiguous []string         `json:"ambiguous,omitempty"`
}

// LensUsageState distinguishes absence, unreadable bytes, and a recorded
// attestation: absence and unreadable usage are unknown, never zero.
const (
	// LensUsageAbsent means cost.json or its lenses key was not recorded.
	LensUsageAbsent = "absent"
	// LensUsageMalformed means the record exists but cannot be read as
	// structured group entries; entries are reported, never guessed.
	LensUsageMalformed = "malformed"
	// LensUsageRecorded means the record carried structured entries.
	LensUsageRecorded = "recorded"
	// LensUsageUnbound means cost.json holds no schema-v2 record bound to
	// this capture: another run's history never certifies this run's work.
	LensUsageUnbound = "unbound"
	// LensUsageVersion means a record binds this capture by run and inputs
	// hash but declares a cost-schema version this build does not certify:
	// evidence from another version is never re-read as the current one.
	LensUsageVersion = "version-unsupported"
)

// LensGroupUsage is one plan group's counted usage. Known reports whether
// the record addressed the group at all: a capped group with Known false
// cannot be certified from silence.
type LensGroupUsage struct {
	Cap     *int     `json:"cap,omitempty"`
	GroupID string   `json:"group"`
	Label   string   `json:"label"`
	Lenses  []string `json:"lenses"`
	Count   int      `json:"count"`
	Known   bool     `json:"known"`
}

// LensViolation is one reason a profile's lens evidence is incomplete or
// over its configured cap. Reason is a stable token, Detail the human
// sentence the consumers print.
type LensViolation struct {
	GroupID string `json:"group,omitempty"`
	Label   string `json:"label,omitempty"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

// Reasons a group's lens evidence fails.
const (
	// LensOverCap means the group recorded more unique labels than its
	// configured max_lenses.
	LensOverCap = "over-cap"
	// LensUsageUnknown means a capped group has no usable recorded
	// usage: absent or unreadable is unknown, never zero.
	LensUsageUnknown = "usage-unknown"
	// LensUnbound means an entry names no plan group, so it cannot
	// attribute work to the group whose cap constrains it.
	LensUnbound = "unbound-group"
	// LensAmbiguous means the same group appears in more than one entry,
	// so its dispatch list is not a single readable attestation.
	LensAmbiguous = "ambiguous-group"
)

// EvaluateLensUsage counts unique labels for each group entry against the
// captured plan. One group entry with an empty labels list is an explicit
// zero attestation for that group, so a review can record work in one group
// and known zero in another. A group the record omits stays unknown, and an
// empty or unknown group entry satisfies no group.
func EvaluateLensUsage(plan *Plan, state string, entries []core.LensUsage) LensUsage {
	usage, groupAt := usageGroups(plan, state)
	seen := map[string]int{}
	ambiguous := map[string]bool{}
	for _, entry := range entries {
		applyLensEntry(&usage, groupAt, entry, seen, ambiguous)
	}
	usage.Ambiguous = ambiguousGroups(ambiguous)
	finaliseUsage(&usage, state, len(entries), seen, ambiguous)
	return usage
}

// planGroups lists the plan's groups in plan order with their caps and
// indexes them by id, so one recorded entry can be attributed to the
// group whose cap constrains it. The usage state is carried through.
func usageGroups(plan *Plan, state string) (LensUsage, map[string]int) {
	usage := LensUsage{State: state}
	groupAt := map[string]int{}
	if plan == nil {
		return usage, groupAt
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		groupAt[group.ID] = len(usage.Groups)
		usage.Groups = append(usage.Groups, LensGroupUsage{
			GroupID: group.ID,
			Label:   group.Label,
			Cap:     group.MaxLenses,
		})
	}
	return usage, groupAt
}

// applyLensEntry attributes one recorded entry: an entry naming no plan
// group is collected as unknown, a group attested twice is marked
// ambiguous without merging its lists, and a first entry contributes its
// unique labels.
func applyLensEntry(usage *LensUsage, groupAt map[string]int, entry core.LensUsage,
	seen map[string]int, ambiguous map[string]bool,
) {
	at, ok := groupAt[entry.Group]
	if !ok || entry.Group == "" {
		if !slices.Contains(usage.Unknown, entry.Group) {
			usage.Unknown = append(usage.Unknown, entry.Group)
		}
		return
	}
	seen[entry.Group]++
	if seen[entry.Group] > 1 {
		ambiguous[entry.Group] = true
		return
	}
	usage.Groups[at].Lenses = uniqueLabels(entry.Labels)
}

// uniqueLabels drops empty and repeated labels, then sorts them: a group's
// usage counts unique labels, never repeated dispatches.
func uniqueLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if label == "" || slices.Contains(out, label) {
			continue
		}
		out = append(out, label)
	}
	slices.Sort(out)
	return out
}

// ambiguousGroups returns the ambiguous group ids in sorted order, nil
// when none: the report order never depends on map iteration.
func ambiguousGroups(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// finaliseUsage records each group's unique-label count and whether its
// usage is known. Known needs a recorded state, no ambiguity, and either
// an entry naming the group or an explicit empty record attesting zero for
// every group: an omitted group stays unknown, never zero.
func finaliseUsage(usage *LensUsage, state string, entries int, seen map[string]int, ambiguous map[string]bool) {
	explicitZero := state == LensUsageRecorded && entries == 0
	for i := range usage.Groups {
		group := &usage.Groups[i]
		group.Count = len(group.Lenses)
		group.Known = state == LensUsageRecorded && !ambiguous[group.GroupID] &&
			(explicitZero || seen[group.GroupID] > 0)
	}
}

// Violations reports every way the recorded usage fails the plan: an
// over-cap group, a capped group whose usage is unknown, a dispatch entry
// that names no group, and a group attested twice. Unset caps never violate
// — unconfigured is uncapped.
func (u *LensUsage) Violations() []LensViolation {
	var out []LensViolation
	for i := range u.Groups {
		if violation, bad := u.groupViolation(&u.Groups[i]); bad {
			out = append(out, violation)
		}
	}
	for _, group := range u.Unknown {
		out = append(out, LensViolation{
			GroupID: group, Reason: LensUnbound,
			Detail: "lens usage names group " + strconv.Quote(group) + ", which is not in the plan — it satisfies no group",
		})
	}
	return out
}

// groupViolation reports one group's failure. An unset cap never violates
// — unconfigured is uncapped; a known count over the cap is over-cap; a
// capped group with no usable attestation is unknown or ambiguous.
func (u *LensUsage) groupViolation(group *LensGroupUsage) (LensViolation, bool) {
	if group.Cap == nil {
		return LensViolation{}, false
	}
	if !group.Known {
		return u.unknownViolation(group), true
	}
	if group.Count > *group.Cap {
		return LensViolation{
			GroupID: group.GroupID, Label: group.Label,
			Reason: LensOverCap, Detail: overCapDetail(group),
		}, true
	}
	return LensViolation{}, false
}

// unknownViolation picks the reason a capped group cannot be certified:
// an ambiguous attestation is named as such, everything else is unknown.
func (u *LensUsage) unknownViolation(group *LensGroupUsage) LensViolation {
	if slices.Contains(u.Ambiguous, group.GroupID) {
		return LensViolation{
			GroupID: group.GroupID, Label: group.Label,
			Reason: LensAmbiguous, Detail: ambiguousDetail(group),
		}
	}
	return LensViolation{
		GroupID: group.GroupID, Label: group.Label,
		Reason: LensUsageUnknown, Detail: u.unknownDetail(group),
	}
}

// detail prefixes shared by the violation sentences.
const (
	profilePrefix = "profile "
	capSentence   = " caps lenses at "
)

// unknownDetail explains why a capped group has no certifiable usage: the
// record's own problem sentence when it named one, otherwise the state.
func (u *LensUsage) unknownDetail(group *LensGroupUsage) string {
	limit := strconv.Itoa(*group.Cap)
	switch u.State {
	case LensUsageAbsent:
		return profilePrefix + group.Label + capSentence + limit + " but cost.json recorded no lens usage — unknown, not zero"
	case LensUsageMalformed, LensUsageVersion:
		if u.Problem != "" {
			return profilePrefix + group.Label + capSentence + limit + " but " + u.Problem + " — unknown, not zero"
		}
		return profilePrefix + group.Label + capSentence + limit +
			" but cost.json lens usage is unreadable — unknown, not zero"
	case LensUsageUnbound:
		return profilePrefix + group.Label + capSentence + limit + " but cost.json holds no record bound to this run — unknown, not zero"
	default:
		return profilePrefix + group.Label + capSentence + limit + " but no recorded entry named its group — unknown, not zero"
	}
}

// ambiguousDetail explains a group attested more than once.
func ambiguousDetail(group *LensGroupUsage) string {
	return profilePrefix + group.Label + " appears in more than one lens entry — its dispatch list is ambiguous, not a readable attestation"
}

// overCapDetail names the cap and the observed count.
func overCapDetail(group *LensGroupUsage) string {
	return profilePrefix + group.Label + capSentence + strconv.Itoa(*group.Cap) +
		" but recorded " + strconv.Itoa(group.Count) + " unique labels"
}

// ReadLensUsage loads a bundle's lens attestation and evaluates it against
// the verified plan. A missing cost.json is absent; bytes that do not decode
// strictly are malformed; a record not bound to this capture is unbound; a
// record bound by run and inputs but declaring an unknown version is
// version-unsupported; and a bound record without a lenses list recorded
// nothing for this run. Every one of those is unknown, never zero. The
// entries are agent attestation, not observed dispatch proof.
func ReadLensUsage(rs *RunState, dir string) LensUsage {
	var plan *Plan
	if rs != nil && rs.Resolved != nil {
		plan = &rs.Resolved.Plan
	}
	entries, state, problem := readLensEntries(rs, filepath.Join(dir, "cost.json"))
	usage := EvaluateLensUsage(plan, state, entries)
	usage.Problem = problem
	return usage
}

// lensSchemaVersion is the only cost-record version whose lens entries carry
// the run binding this reader can certify.
const lensSchemaVersion = "2"

// lensRecordKeysByVersion maps every certifiable cost-record version to the
// exact top-level shape that version defines. Certification is version
// scoped: a field a version does not define refuses under that version and
// names both, instead of being read as if it were evidence. A new field that
// affects lens evidence requires a new version, not a silent addition.
var lensRecordKeysByVersion = map[string]map[string]bool{
	lensSchemaVersion: {
		"schema_version": true, "effort": true, "models": true, "est_usd": true,
		"wall_seconds": true, "diff_bytes": true, "lenses": true,
		"run": true, "inputs_sha256": true, "group_costs": true,
	},
}

// groupCostKeys is the complete per-group cost shape: an unknown field is an
// unsupported claim, never silently ignored into a priced group.
var groupCostKeys = map[string]bool{"group": true, "effort": true, "est_usd": true}

// lensEntryKeys is the complete lens-entry shape: an unknown field is an
// unsupported attestation, never silently ignored into a pass.
var lensEntryKeys = map[string]bool{"group": true, "labels": true}

// readLensEntries reads the attestation bound to the current capture. Only
// the last schema-v2 record whose run name and inputs hash both equal the
// verified bundle's can certify: earlier history is billing evidence, and an
// incomplete bound record never falls back to an earlier success. The problem
// sentence names the refused field or version when there is one to name.
func readLensEntries(rs *RunState, path string) ([]core.LensUsage, string, string) {
	raw, missing := readCostFile(path)
	if missing != "" {
		return nil, missing, ""
	}
	bound, state, problem := certificateRecord(rs, raw)
	if state != "" {
		return nil, state, problem
	}
	return certifiedEntries(bound)
}

// certificateRecord decodes the cost file and names the record that certifies
// this capture, or the state and sentence that refuse it.
func certificateRecord(rs *RunState, raw []byte) (map[string]any, string, string) {
	doc, ref := decodeStrict(raw)
	if ref != nil {
		return nil, LensUsageMalformed, ""
	}
	records, ok := lensRecords(doc)
	if !ok {
		return nil, LensUsageMalformed, ""
	}
	if bound, boundOK := lastBoundRecord(records, rs); boundOK {
		return bound, "", ""
	}
	if version := foreignVersion(records, rs); version != "" {
		return nil, LensUsageVersion, foreignVersionProblem(version)
	}
	return nil, LensUsageUnbound, ""
}

// certifiedEntries reads the lens list of the record that certifies the run.
func certifiedEntries(bound map[string]any) ([]core.LensUsage, string, string) {
	entries, present, ok, problem := lensRecordEntries(bound)
	if !ok {
		return nil, LensUsageMalformed, problem
	}
	if !present {
		return nil, LensUsageAbsent, ""
	}
	return entries, LensUsageRecorded, ""
}

// lensRecords splits a cost document into its records: one object, or the
// history array a re-reviewed bundle appends to.
func lensRecords(doc any) ([]map[string]any, bool) {
	switch value := doc.(type) {
	case map[string]any:
		return []map[string]any{value}, true
	case []any:
		records := make([]map[string]any, 0, len(value))
		for _, item := range value {
			record, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			records = append(records, record)
		}
		return records, true
	default:
		return nil, false
	}
}

// lastBoundRecord picks the newest record bound to the current capture: a
// schema-v2 record whose run and inputs_sha256 both match the verified
// bundle. Without a verified name and inputs hash nothing can be bound, so
// no record can certify.
func lastBoundRecord(records []map[string]any, rs *RunState) (map[string]any, bool) {
	if rs == nil || rs.Name == "" || rs.InputsSHA256 == "" {
		return nil, false
	}
	for _, record := range slices.Backward(records) {
		if boundTo(record, rs) {
			return record, true
		}
	}
	return nil, false
}

// boundTo reports whether one record names this capture: schema-v2 evidence
// whose run and inputs hash both equal the verified bundle's.
func boundTo(record map[string]any, rs *RunState) bool {
	return schemaV2(record) && bindsCapture(record, rs)
}

// schemaV2 reports the record version that carries the run binding. Both the
// string and the number spelling are accepted; any other version, including
// an absent one, cannot certify.
func schemaV2(record map[string]any) bool {
	return recordVersion(record) == lensSchemaVersion
}

// recordVersion reads the declared cost-schema version in either spelling,
// empty when absent or not a string or number.
func recordVersion(record map[string]any) string {
	switch version := record["schema_version"].(type) {
	case string:
		return version
	case json.Number:
		return version.String()
	default:
		return ""
	}
}

// certifies reports whether this build can certify a record of that version.
func certifies(version string) bool {
	_, ok := lensRecordKeysByVersion[version]
	return ok
}

// foreignVersion names the version of the newest record that binds this
// capture by run and inputs but declares a version this build cannot certify,
// so the refusal says which version arrived instead of "unbound": evidence
// from a newer writer is a version problem, not a missing record.
func foreignVersion(records []map[string]any, rs *RunState) string {
	if !bindable(rs) {
		return ""
	}
	for _, record := range slices.Backward(records) {
		if version := foreignCaptureVersion(record, rs); version != "" {
			return version
		}
	}
	return ""
}

// foreignCaptureVersion names the version of a record that binds this capture
// and declares one this build cannot certify, empty otherwise. A binding that
// declares no version is skipped: the newest binding that does declare an
// unknown version is the diagnosis, not the newest binding overall.
func foreignCaptureVersion(record map[string]any, rs *RunState) string {
	if !bindsCapture(record, rs) {
		return ""
	}
	version := recordVersion(record)
	if version == "" || certifies(version) {
		return ""
	}
	return version
}

// bindable reports a verified capture name and inputs hash: without both, no
// record can be bound to the run.
func bindable(rs *RunState) bool {
	return rs != nil && rs.Name != "" && rs.InputsSHA256 != ""
}

// bindsCapture reports whether one record names this capture by run and inputs
// hash, whatever version it declares: a newer writer's evidence is still this
// run's evidence, refused by version rather than reported as missing.
func bindsCapture(record map[string]any, rs *RunState) bool {
	return recordString(record, "run") == rs.Name &&
		recordString(record, "inputs_sha256") == rs.InputsSHA256
}

// unregisteredFieldProblem names the top-level field a record declares beyond
// its own version's shape, and the two ways to fix it: drop the field, or
// declare a version that defines it.
func unregisteredFieldProblem(version, field string) string {
	return "cost.json declares schema_version " + version + " but carries top-level field " +
		strconv.Quote(field) + ", which that version does not define — remove the field, or " +
		"declare a version this build certifies"
}

// foreignVersionProblem names the version that arrived and the one this build
// certifies: a newer record is refused by version, never re-read as v2.
func foreignVersionProblem(version string) string {
	return "cost.json binds this run at schema_version " + version +
		", but this build certifies schema_version " + lensSchemaVersion + " — upgrade verdict"
}

// unregisteredField returns the first top-level field obj declares beyond the
// version's shape, in sorted order so the sentence never depends on map
// iteration.
func unregisteredField(obj map[string]any, allowed map[string]bool) string {
	extra := make([]string, 0, len(obj))
	for key := range obj {
		if !allowed[key] {
			extra = append(extra, key)
		}
	}
	slices.Sort(extra)
	if len(extra) == 0 {
		return ""
	}
	return extra[0]
}

// recordString reads one string field, empty when absent or not a string.
func recordString(record map[string]any, key string) string {
	value, ok := record[key].(string)
	if !ok {
		return ""
	}
	return value
}

// lensRecordEntries reads the chosen record's lens list. present reports a
// non-null list, so an empty list attests explicit zero for every group; ok
// is false for an unreadable entry, and problem carries the sentence when the
// refusal can name a field or a version.
func lensRecordEntries(record map[string]any) (entries []core.LensUsage, present, ok bool, problem string) {
	version := recordVersion(record)
	keys, certified := lensRecordKeysByVersion[version]
	if !certified {
		return nil, false, false, foreignVersionProblem(version)
	}
	if field := unregisteredField(record, keys); field != "" {
		return nil, false, false, unregisteredFieldProblem(version, field)
	}
	return lensListEntries(record)
}

// lensListEntries reads the lenses list of a certified record. present reports
// a non-null list, so an empty list attests explicit zero for every group; ok
// is false for an unreadable entry.
func lensListEntries(record map[string]any) (entries []core.LensUsage, present, ok bool, problem string) {
	raw, present := record["lenses"]
	if !present || raw == nil {
		return nil, false, true, ""
	}
	list, listOK := raw.([]any)
	if !listOK {
		return nil, false, false, ""
	}
	entries = make([]core.LensUsage, 0, len(list))
	for _, item := range list {
		parsed, itemOK := lensListItem(item)
		if !itemOK {
			return nil, true, false, ""
		}
		entries = append(entries, parsed)
	}
	return entries, true, true, ""
}

// lensListItem reads one list item as a shaped group entry.
func lensListItem(item any) (core.LensUsage, bool) {
	entry, ok := item.(map[string]any)
	if !ok || !knownKeys(entry, lensEntryKeys) {
		return core.LensUsage{}, false
	}
	return lensEntry(entry)
}

// lensEntry reads one group entry: a string group and a labels list, which
// is required so an absent or null labels value stays unreadable rather than
// an implied zero.
func lensEntry(entry map[string]any) (core.LensUsage, bool) {
	group, ok := entry["group"].(string)
	if !ok {
		return core.LensUsage{}, false
	}
	raw, present := entry["labels"]
	if !present || raw == nil {
		return core.LensUsage{}, false
	}
	list, ok := raw.([]any)
	if !ok {
		return core.LensUsage{}, false
	}
	labels := make([]string, 0, len(list))
	for _, item := range list {
		label, ok := item.(string)
		if !ok {
			return core.LensUsage{}, false
		}
		labels = append(labels, label)
	}
	return core.LensUsage{Group: group, Labels: labels}, true
}

// knownKeys reports whether every key of obj is in allowed: an unknown field
// is an unsupported shape, not something to normalize away.
func knownKeys(obj map[string]any, allowed map[string]bool) bool {
	for key := range obj {
		if !allowed[key] {
			return false
		}
	}
	return true
}

// closeRecord releases the record handle; a close error cannot change an
// already-parsed attestation.
func closeRecord(file *os.File) {
	if err := file.Close(); err != nil {
		return
	}
}

// maxCostRecordBytes caps the record read: a cost record is a small JSON
// document, so oversized bytes are malformed rather than decoded.
const maxCostRecordBytes = 1 << 20

// readCostFile reads the record under a hard size cap. A file that cannot
// be found is absent; other open failures and oversized files are malformed,
// never treated as zero usage.
func readCostFile(path string) ([]byte, string) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, LensUsageAbsent
	}
	if err != nil {
		return nil, LensUsageMalformed
	}
	defer closeRecord(file)
	raw, err := io.ReadAll(io.LimitReader(file, maxCostRecordBytes+1))
	if err != nil || len(raw) > maxCostRecordBytes {
		return nil, LensUsageMalformed
	}
	return raw, ""
}
