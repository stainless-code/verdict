package mcp

import (
	"reflect"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// JSON Schema keywords and JSON type names used by the reflector.
const (
	schemaType                 = "type"
	schemaProperties           = "properties"
	schemaRequired             = "required"
	schemaAdditionalProperties = "additionalProperties"
	schemaItems                = "items"
	schemaDescription          = "description"
	typeObject                 = "object"
	typeArray                  = "array"
	typeString                 = "string"
	typeInteger                = "integer"
	typeNumber                 = "number"
	typeBoolean                = "boolean"
)

// internalFieldNames are request fields the transport owns: the injected
// runtime, presentation prefs, adapter streams, and CLI-only switches. They
// never appear in a tool schema or accept an argument.
var internalFieldNames = map[string]bool{
	"Runtime":        true,
	"Root":           true,
	"Forces":         true,
	"Effective":      true,
	"Warnings":       true,
	"Human":          true,
	"Diag":           true,
	"ReadAnswer":     true,
	"Environ":        true,
	"JSONMode":       true,
	"Interactive":    true,
	"HasBase":        true,
	"HasProvider":    true,
	"HasStash":       true,
	"ReportWarnings": true,
}

// readRequestType is core's embedded transport-neutral invocation.
var readRequestType = reflect.TypeFor[core.ReadRequest]()

// effectiveType is core's resolved presentation configuration.
var effectiveType = reflect.TypeFor[core.Effective]()

// visibleField is one request field the transport exposes.
type visibleField struct { //nolint:govet // field grouping documents the walk; alignment is irrelevant
	sf     reflect.StructField
	index  []int
	nested bool
}

// visibleFields lists the request fields a tool exposes. Anonymous
// core.ReadRequest embeddings inline (the transport speaks repo/pr
// directly); named structs such as the verb Flags stay nested objects.
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

// hasVisibleFields reports whether t carries at least one exposed field.
func hasVisibleFields(t reflect.Type, omit map[string]bool) bool {
	return len(visibleFields(t, omit)) > 0
}

// skipField applies the transport's field policy: unexported or json:"-"
// fields, the adapter-owned names above, and non-data kinds.
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

// argName maps a Go field name to the tool argument key: an explicit json
// tag wins, otherwise lowerCamelCase (BaseSHA -> baseSHA, ID -> id).
func argName(sf *reflect.StructField) string {
	if tag := sf.Tag.Get("json"); tag != "" {
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return lowerCamel(sf.Name)
}

// lowerCamel lowercases a Go field name without mangling initialisms: a
// leading uppercase run becomes lowercase, except its last letter when a
// lowercase letter follows (BaseSHA -> baseSHA, URLPath -> urlPath).
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

// fieldSchema builds one property schema from the field's Go type plus the
// shared description table.
func fieldSchema(toolName string, sf *reflect.StructField, omit map[string]bool) (map[string]any, bool) {
	node, ok := typeSchema(sf.Type, omit)
	if !ok {
		return nil, false
	}
	if desc := fieldDescription(toolName, sf.Name); desc != "" {
		node[schemaDescription] = desc
	}
	return node, true
}

// typeSchema maps a Go type to a JSON Schema node. It is intentionally a
// small subset: strings, booleans, integers, numbers, string arrays, and
// nested request structs.
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

// objectSchema renders one nested request struct as a closed object.
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

// inputSchema builds a tool's top-level input schema from its request
// struct. There is no hand-maintained duplicate: the schema is the struct.
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

// fieldDocs carries the request fields' meaning. Structure is reflected
// from the Go types; this table only adds prose (tool descriptions carry
// the verb-level contract).
var fieldDocs = map[string]string{
	"Repo":       "Repository as owner/name.",
	"Pr":         "Pull-request number or bundle slug (local-<name> for local bundles).",
	"Sig":        "Finding signature or unique prefix.",
	"Status":     "Target status (open, addressed, wont-fix, false-positive, deferred).",
	"Expect":     "Comma-separated allowlist of paths that may be dirty.",
	"From":       "Local bundle slug to adopt findings from.",
	"Check":      "Gate tier (critical, important, informational); empty disables the gate.",
	"Live":       "Live-thread posture (auto, strict, skip).",
	"Output":     "Write the rendered report markdown to this path.",
	"Sarif":      "Write a SARIF report to this path.",
	"Overlap":    "Join overlapping findings across bundles.",
	"Probe":      "Grade the offline probe instead of joining live state.",
	"Specs":      "Judge specs to grade (probe mode).",
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
	"Rationale":  "Explanation for the status change.",
	"Force":      "Override the refusal that would otherwise block the change.",
	"DryRun":     "Report what would change without writing.",
}

// fieldDescription resolves tool-specific prose first, then the shared
// field table.
func fieldDescription(toolName, field string) string {
	if doc, ok := fieldDocs[toolName+"."+field]; ok {
		return doc
	}
	return fieldDocs[field]
}
