package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestSchemaSelfCheck makes the reflected input schema prove its own
// completeness: every request field that is not explicitly transport-owned
// has a schema node, every schema property names a unique request field, every
// enum/bound tag reaches the schema, and no field-doc or internal-field entry
// is stale.
//
//nolint:revive // the schema audit reads as one assertion block
func TestSchemaSelfCheck(t *testing.T) {
	t.Parallel()
	tools := allTools(false, true)
	allFields := map[string]bool{}
	topFields := map[string]map[string]bool{}
	for _, tool := range tools {
		topFields[tool.name] = fieldNames(tool.request)
		collectFieldNames(tool.request, allFields, map[reflect.Type]bool{})
		schemaAudit(t, tool, tool.request, tool.omit, "", inputSchema(tool))
	}
	for name := range internalFieldNames {
		if !allFields[name] {
			t.Errorf("internalFieldNames has stale entry %q", name)
		}
	}
	for key := range fieldDocs {
		head, tail, ok := strings.Cut(key, ".")
		if !ok {
			if !allFields[head] {
				t.Errorf("fieldDocs entry %q names no request field", key)
			}
			continue
		}
		if !topFields[head][tail] {
			t.Errorf("fieldDocs entry %q names no top-level field of that tool", key)
		}
	}
}

// schemaAudit walks one request type's visible fields and checks each one
// against the schema node the tool will serve at that level.
//
//nolint:revive // the audit is one branch per field shape
func schemaAudit(t *testing.T, tool *tool, typ reflect.Type, omit map[string]bool, path string, node map[string]any) {
	t.Helper()
	props, ok := node[schemaProperties].(map[string]any)
	if !ok {
		t.Fatalf("%s: %s%v has no properties", tool.name, path, typ)
	}
	seen := map[string]string{}
	fields := visibleFields(typ, omit)
	for i := range fields {
		f := &fields[i]
		key := argName(&f.sf)
		if prev, ok := seen[key]; ok {
			t.Errorf("%s: fields %s and %s both decode as %q", tool.name, prev, f.sf.Name, key)
		}
		seen[key] = f.sf.Name
		child, ok := props[key].(map[string]any)
		if !ok {
			t.Errorf("%s: field %s%s has no schema property %q", tool.name, path, f.sf.Name, key)
			continue
		}
		if f.nested {
			schemaAudit(t, tool, f.sf.Type, omit, path+key+".", child)
			continue
		}
		if _, ok := fieldSchema(tool.name, &f.sf, omit); !ok {
			t.Errorf("%s: field %s%s has no schema mapping (unsupported type %s)",
				tool.name, path, f.sf.Name, f.sf.Type)
			continue
		}
		checkFieldTags(t, tool.name, path+f.sf.Name, &f.sf, child)
	}
	for prop := range props {
		if _, ok := seen[prop]; !ok {
			t.Errorf("%s: schema property %s%q matches no request field", tool.name, path, prop)
		}
	}
}

// checkFieldTags proves the struct tags the schema promises actually landed.
func checkFieldTags(t *testing.T, tool, field string, sf *reflect.StructField, node map[string]any) {
	t.Helper()
	checkEnumTag(t, tool, field, sf, node)
	checkBoundsTag(t, tool, field, schemaMinimum, tagMin, sf, node)
	checkBoundsTag(t, tool, field, schemaMaximum, tagMax, sf, node)
}

// checkEnumTag proves the enum tag became the schema's closed vocabulary.
func checkEnumTag(t *testing.T, tool, field string, sf *reflect.StructField, node map[string]any) {
	t.Helper()
	spec := sf.Tag.Get(tagEnum)
	if spec == "" {
		return
	}
	want := make([]any, 0)
	for part := range strings.SplitSeq(spec, enumSeparator) {
		want = append(want, part)
	}
	if !reflect.DeepEqual(node[schemaEnum], want) {
		t.Errorf("%s.%s enum = %v, want %v", tool, field, node[schemaEnum], want)
	}
}

// checkBoundsTag proves one min/max tag became its schema keyword.
func checkBoundsTag(t *testing.T, tool, field, keyword, tag string, sf *reflect.StructField, node map[string]any) {
	t.Helper()
	if raw := sf.Tag.Get(tag); raw != "" {
		if _, ok := node[keyword]; !ok {
			t.Errorf("%s.%s lost its %s tag %q", tool, field, tag, raw)
		}
	}
}

// fieldNames lists the top-level visible field names of one request type.
func fieldNames(typ reflect.Type) map[string]bool {
	names := map[string]bool{}
	fields := visibleFields(typ, nil)
	for i := range fields {
		names[fields[i].sf.Name] = true
	}
	return names
}

// collectFieldNames records every exported field name reachable from a request
// type, including fields the transport hides.
func collectFieldNames(typ reflect.Type, into map[string]bool, visited map[reflect.Type]bool) {
	if visited[typ] {
		return
	}
	visited[typ] = true
	for sf := range typ.Fields() {
		if sf.PkgPath != "" {
			continue
		}
		into[sf.Name] = true
		if sf.Type.Kind() == reflect.Struct {
			collectFieldNames(sf.Type, into, visited)
		}
	}
}

// TestDecodeCoversSchema proves the explicit per-request decoder accepts every
// argument the generated schema advertises. A field added to a request struct
// but forgotten in its decoder fails here as an unknown argument.
//
//nolint:revive // the coverage matrix reads as one assertion block
func TestDecodeCoversSchema(t *testing.T) {
	t.Parallel()
	for _, tool := range allTools(false, true) {
		t.Run(tool.name, func(t *testing.T) {
			t.Parallel()
			if tool.decode == nil {
				t.Fatal("tool has no decoder")
			}
			args := schemaValue(inputSchema(tool))
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal synthetic args: %v", err)
			}
			if _, err := tool.decode(raw); err != nil {
				t.Fatalf("decoder rejects schema-advertised arguments: %v", err)
			}
		})
	}
}

// schemaValue builds a type-correct argument value for a schema node.
func schemaValue(node map[string]any) any {
	switch node[schemaType] {
	case typeString:
		return "x"
	case typeBoolean:
		return true
	case typeInteger:
		return 1
	case typeNumber:
		return 1.5
	case typeArray:
		items, ok := node[schemaItems].(map[string]any)
		if !ok {
			return []any{}
		}
		return []any{schemaValue(items)}
	case typeObject:
		return schemaObjectValue(node)
	default:
		return nil
	}
}

// schemaObjectValue fills one nested object schema with synthetic values.
func schemaObjectValue(node map[string]any) map[string]any {
	props, ok := node[schemaProperties].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, raw := range props {
		if child, ok := raw.(map[string]any); ok {
			out[key] = schemaValue(child)
		}
	}
	return out
}
