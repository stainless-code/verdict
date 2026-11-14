package mcp

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
)

const (
	schemaType                 = "type"
	schemaProperties           = "properties"
	schemaRequired             = "required"
	schemaAdditionalProperties = "additionalProperties"
	schemaItems                = "items"
	schemaDescription          = "description"
	schemaEnum                 = "enum"
	schemaMinimum              = "minimum"
	schemaMaximum              = "maximum"
	tagEnum                    = "enum"
	tagMin                     = "min"
	tagMax                     = "max"
	typeObject                 = "object"
	typeArray                  = "array"
	typeString                 = "string"
	typeInteger                = "integer"
	typeNumber                 = "number"
	typeBoolean                = "boolean"
	enumSeparator              = "|"
	floatBits                  = 64
)

var internalFieldNames = map[string]bool{
	"Runtime":     true,
	"Root":        true,
	"RepoRoot":    true,
	"Forces":      true,
	"Effective":   true,
	"Warnings":    true,
	"Human":       true,
	"Diag":        true,
	"ReadAnswer":  true,
	"Environ":     true,
	"JSONMode":    true,
	"Interactive": true,
}

type schemaSource = reflect.Type

func schemaOf[Req any]() schemaSource { return reflect.TypeFor[Req]() }

var readRequestType = reflect.TypeFor[core.ReadRequest]()

var effectiveType = reflect.TypeFor[core.Effective]()

type visibleField struct { //nolint:govet // field grouping documents the walk; alignment is irrelevant
	sf     reflect.StructField
	index  []int
	nested bool
}

//
//nolint:revive // the field-policy walk is one flat loop
func visibleFields(t reflect.Type, omit map[string]bool) []visibleField {
	fields := make([]visibleField, 0, t.NumField())
	for i := range t.NumField() {
		sf := t.Field(i)
		if skipField(&sf, omit) {
			continue
		}
		if sf.Anonymous && sf.Type == readRequestType {
			inner := visibleFields(sf.Type, omit)
			for j := range inner {
				inner[j].index = append([]int{i}, inner[j].index...)
				fields = append(fields, inner[j])
			}
			continue
		}
		nested := sf.Type.Kind() == reflect.Struct
		if nested && !hasVisibleFields(sf.Type, omit) {
			continue
		}
		fields = append(fields, visibleField{sf: sf, index: []int{i}, nested: nested})
	}
	return fields
}

func hasVisibleFields(t reflect.Type, omit map[string]bool) bool {
	return len(visibleFields(t, omit)) > 0
}

func skipField(sf *reflect.StructField, omit map[string]bool) bool {
	if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
		return true
	}
	if omit[sf.Name] || internalFieldNames[sf.Name] {
		return true
	}
	if sf.Type == effectiveType || sf.Type == reflect.TypeFor[*core.Runtime]() ||
		sf.Type == reflect.TypeFor[*core.GlobalForces]() {
		return true
	}
	return sf.Type.Kind() == reflect.Interface || sf.Type.Kind() == reflect.Func
}

func argName(sf *reflect.StructField) string {
	if tag := sf.Tag.Get("json"); tag != "" {
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return lowerCamel(sf.Name)
}

func lowerCamel(name string) string {
	if name == "" {
		return ""
	}
	i := 0
	for i < len(name) && name[i] >= 'A' && name[i] <= 'Z' {
		i++
	}
	switch {
	case i == 1:
		return strings.ToLower(name[:1]) + name[1:]
	case i == len(name):
		return strings.ToLower(name)
	case i == 0:
		return name
	default:
		return strings.ToLower(name[:i-1]) + name[i-1:]
	}
}

func fieldSchema(toolName string, sf *reflect.StructField, omit map[string]bool) (map[string]any, bool) {
	node, ok := typeSchema(sf.Type, omit)
	if !ok {
		return nil, false
	}
	if desc := fieldDescription(toolName, sf.Name); desc != "" {
		node[schemaDescription] = desc
	}
	applyEnum(node, sf)
	applyFieldEnum(node, toolName, sf.Name)
	applyBound(node, schemaMinimum, sf.Tag.Get(tagMin))
	applyBound(node, schemaMaximum, sf.Tag.Get(tagMax))
	return node, true
}

// fieldEnums publishes a tool-specific value list for a field whose Go type
// carries none, so the transport advertises exactly what it serves.
var fieldEnums = map[string][]string{
	"index.Query": indexcore.MCPQueries,
}

// applyFieldEnum overrides a struct-tag enum with the tool's own list.
func applyFieldEnum(node map[string]any, toolName, field string) {
	values, ok := fieldEnums[toolName+"."+field]
	if !ok {
		return
	}
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	node[schemaEnum] = out
}

func applyEnum(node map[string]any, sf *reflect.StructField) {
	spec := sf.Tag.Get(tagEnum)
	if spec == "" {
		return
	}
	parts := strings.Split(spec, enumSeparator)
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		values = append(values, part)
	}
	node[schemaEnum] = values
}

func applyBound(node map[string]any, key, raw string) {
	if raw == "" {
		return
	}
	if n, err := strconv.Atoi(raw); err == nil {
		node[key] = n
		return
	}
	if f, err := strconv.ParseFloat(raw, floatBits); err == nil {
		node[key] = f
	}
}

func typeSchema(t reflect.Type, omit map[string]bool) (map[string]any, bool) {
	if t.Kind() == reflect.Pointer {
		return typeSchema(t.Elem(), omit)
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{schemaType: typeString}, true
	case reflect.Bool:
		return map[string]any{schemaType: typeBoolean}, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{schemaType: typeInteger}, true
	case reflect.Float32, reflect.Float64:
		return map[string]any{schemaType: typeNumber}, true
	case reflect.Slice:
		items, ok := typeSchema(t.Elem(), omit)
		if !ok {
			return map[string]any{schemaType: typeArray}, true
		}
		return map[string]any{schemaType: typeArray, schemaItems: items}, true
	case reflect.Struct:
		return objectSchema(t, omit), true
	default:
		return nil, false
	}
}

func objectSchema(t reflect.Type, omit map[string]bool) map[string]any {
	props := make(map[string]any)
	fields := visibleFields(t, omit)
	for i := range fields {
		f := &fields[i]
		if f.nested {
			props[argName(&f.sf)] = objectSchema(f.sf.Type, omit)
			continue
		}
		node, ok := fieldSchema("", &f.sf, omit)
		if !ok {
			continue
		}
		props[argName(&f.sf)] = node
	}
	return map[string]any{
		schemaType:                 typeObject,
		schemaProperties:           props,
		schemaAdditionalProperties: false,
	}
}

//
//nolint:revive // schema assembly is one flat field loop
func inputSchema(t *tool) map[string]any {
	props := make(map[string]any)
	fields := visibleFields(t.request, t.omit)
	for i := range fields {
		f := &fields[i]
		key := argName(&f.sf)
		if f.nested {
			node := objectSchema(f.sf.Type, t.omit)
			if desc := fieldDescription(t.name, f.sf.Name); desc != "" {
				node[schemaDescription] = desc
			}
			props[key] = node
			continue
		}
		node, ok := fieldSchema(t.name, &f.sf, t.omit)
		if !ok {
			continue
		}
		props[key] = node
	}
	return map[string]any{
		schemaType:                 typeObject,
		schemaProperties:           props,
		schemaRequired:             append([]string{}, t.required...),
		schemaAdditionalProperties: false,
	}
}

var fieldDocs = map[string]string{
	"pr_wait.Until":    "Condition to await; review-changed compares with this call's first observation.",
	"pr_wait.Timeout":  "Foreground wait duration, 1s–50s; defaults to 50s.",
	"pr_wait.Interval": "Initial poll interval, at least 1s and at most timeout; defaults to 1s.",
	"pr_wait.Once":     "Observe once without sleeping; an unmet condition returns condition-unmet data.",
	"pr_wait.Provider": "Host override; defaults to saved provider or github. GitLab and Forgejo refuse explicitly.",
	"ready.WaitFor":    "Wait for build completion before reporting readiness; timeout returns not-ready data.",
	"ready.Timeout":    "Build wait duration, 1s–50s; defaults to 50s when wait_for is build.",

	"Repo":       "Repository as owner/name.",
	"Pr":         "Pull-request number or bundle slug (local-<name> for local bundles).",
	"Sig":        "Finding signature or unique prefix.",
	"Status":     "Status filter (all, open, addressed, wont-fix, false-positive, deferred).",
	"Kind":       "Kind filter (inline, section, all).",
	"Category":   "Finding category filter; empty disables the filter.",
	"Expect":     "Comma-separated allowlist of paths that may be dirty.",
	"From":       "Local bundle slug to adopt findings from.",
	"Check":      "Gate tier (critical, important, informational); empty disables the gate.",
	"Live":       "Live-thread posture (auto, strict, skip).",
	"Output":     "Write the rendered report markdown to this path.",
	"Sarif":      "Write a SARIF report to this path.",
	"Overlap":    "Join overlapping findings across bundles.",
	"Probe":      "Grade the offline probe instead of joining live state.",
	"Specs":      "Judge specs to grade (probe mode).",
	"Summary":    "PR-level summary comment mode: comment (create or update in place) or off (default).",
	"JudgeFile":  "Path to a judge spec file (probe mode).",
	"Rule":       "Rule text to append (learn).",
	"Scope":      "Rule scope (repo, path, or language).",
	"ID":         "Explicit rule id.",
	"Provenance": "Rule provenance label.",
	"Rigor":      "Rule rigor level.",
	"Reason":     "Retirement reason (30+ characters).",
	"Retire":     "Rule id to retire.",
	"Restore":    "Rule id to restore.",
	"Preset":     "Scaffold preset (chill, standard, strict).",
	"Yes":        "Confirm overwriting existing control files.",
	"TimeoutRaw": "Verification timeout in seconds (positive integer).",
	"Group":      "Deep validation: stable plan group id from run.json. Requires command.",
	"Command":    "Deep validation: explicit command to run at the reviewed root. Requires group.",
	"Rationale":  "Explanation for the status change.",
	"Force":      "Override the refusal that would otherwise block the change.",
	"DryRun":     "Report what would change without writing.",
	"AllowSecretValues": "Publish review text that carries a secret-shaped value; " +
		"the post-secret-shaped refusal names this override.",
	"doctor.Provider": "Code host to preflight (github, gitlab, gitcafe, forgejo, bitbucket, azure); default github.",
	"learn.Status":    "List rules with counters, precision, exposure and acceptance (boolean list switch; learn sets no status).",
	"next.Status":     "Status filter for the top finding (all, open, addressed, wont-fix, false-positive, deferred); empty means open.",
	"triage.Status":   "Status to flip the finding to (open, addressed, wont-fix, false-positive, deferred).",
	"index.Query":     "Subcommand: callers, status, tests-for, co-changed, owners, or blast-radius; build and prune are CLI-only.",
	"index.Symbol":    "Caller query symbol (bare Target or package-qualified app.Target), or the tests-for --symbol input.",
	"index.At":        "Tree or ref to query; default HEAD.",
	"index.From":      "Checkout directory to read the index from; default the repository root.",
	"index.Path":      "Repository path for tests-for, co-changed, or owners.",
	"index.Paths":     "Repository paths for blast-radius: every declaration in each file is a changed symbol; no bundle or host call.",
	"index.Pr": "Fetched PR bundle whose full.diff names blast-radius's changed paths and lines; " +
		"its tree is queried when the index holds it, otherwise HEAD with a warning.",
	"index.Since":    "Co-change window start: a duration (90d, 12w, 18mo, 2y) or a commit ref.",
	"index.Commits":  "Co-change window size in commits (1-100000; default 2000).",
	"index.MinCount": "Minimum co-change count a partner needs (1-100000; default 2).",
	"index.Limit":    "Maximum rows to return (1-200; default 50 for callers and tests-for, 20 for co-changed).",
	"index.Depth":    "Import hops to walk for transitive candidate tests (1-8; default 3; 1 turns the walk off).",
	"Index": "Explicit index hand-off: a ref or a directory holding an index store. The fetch " +
		"verb writes impact.json and a capped ## impact section on toc.txt; omitted means no " +
		"impact section.",
	"Sandbox": "Run under bwrap (Linux only) with no network, hidden credential dirs, " +
		"a writable repo, and a private /tmp; refuses sandbox-unavailable when it cannot be enforced.",
	"EnvAllowlist": "Additional parent environment variable names to pass through to the verification child process, " +
		"overriding default-deny filtering and secret-name dropping.",
}

func fieldDescription(toolName, field string) string {
	if doc, ok := fieldDocs[toolName+"."+field]; ok {
		return doc
	}
	return fieldDocs[field]
}
