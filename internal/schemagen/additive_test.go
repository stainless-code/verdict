package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// baselineDir is the committed snapshot of the accepted published schemas.
// `make schemas-baseline` rewrites it; see schemas/README.md.
const baselineDir = "internal/schemagen/testdata/baseline"

// TestSchemasStayAdditive compares every generated schema with the committed
// baseline and fails on a change that would break an existing consumer:
// a removed property or $defs entry, a removed enum value, a newly required
// key, closed additionalProperties, narrowed types or bounds, or a changed
// $ref/const. Additions pass.
//
// Accepting a deliberate break is explicit: run `make schemas-baseline` and
// commit the baseline diff in the same change, where reviewers see exactly
// what stopped being compatible.
func TestSchemasStayAdditive(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	dir := filepath.Join(root, baselineDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s baseline: %v (run `make schemas-baseline`)", baselineDir, err)
	}
	generated := make(map[string]bool, len(files))
	for _, file := range files {
		generated[file.Name] = true
		checkAdditive(t, dir, file)
	}
	for _, name := range baselineNames(entries) {
		if !generated[name] {
			t.Errorf("%s has no generated schema; remove it or regenerate the baseline",
				filepath.Join(baselineDir, name))
		}
	}
}

// checkAdditive compares one committed baseline document with its generated
// counterpart.
func checkAdditive(t *testing.T, dir string, file File) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, file.Name))
	if os.IsNotExist(err) {
		t.Errorf("%s is missing; run `make schemas-baseline` and commit it",
			filepath.Join(baselineDir, file.Name))
		return
	}
	if err != nil {
		t.Fatalf("read baseline %s: %v", file.Name, err)
	}
	changes := breakingChanges(mustSchema(t, raw), mustSchema(t, file.Data))
	if len(changes) == 0 {
		return
	}
	sort.Strings(changes)
	t.Errorf("%s has consumer-breaking changes:\n  %s\nAdditive changes are always allowed. "+
		"Accept a deliberate break by running `make schemas-baseline` and committing the baseline diff.",
		file.Name, strings.Join(changes, "\n  "))
}

// baselineNames lists the schema files in the baseline directory.
func baselineNames(entries []os.DirEntry) []string {
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// mustSchema decodes one schema document with number fidelity.
func mustSchema(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	unmarshalJSON(t, raw, &doc)
	return doc
}

// TestBreakingChangesClassifies pins the classifier in both directions:
// each consumer-breaking shape is reported, and each additive shape is not.
//
//nolint:revive // the two-direction table reads best inline
func TestBreakingChangesClassifies(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		current string
		want    string
	}{
		{
			name:    "removed property",
			base:    `{"type":"object","properties":{"kept":{},"gone":{}}}`,
			current: `{"type":"object","properties":{"kept":{}}}`,
			want:    `property "gone" removed`,
		},
		{
			name:    "removed enum value",
			base:    `{"enum":["open","closed"]}`,
			current: `{"enum":["open"]}`,
			want:    "enum value closed removed",
		},
		{
			name:    "newly required field",
			base:    `{"required":["a"]}`,
			current: `{"required":["a","b"]}`,
			want:    `newly required "b"`,
		},
		{
			name:    "additionalProperties closed",
			base:    `{"type":"object","additionalProperties":true}`,
			current: `{"type":"object","additionalProperties":false}`,
			want:    "additionalProperties closed",
		},
		{
			name:    "type narrowed",
			base:    `{"type":["string","null"]}`,
			current: `{"type":"string"}`,
			want:    `type "null" no longer accepted`,
		},
		{
			name:    "minimum raised",
			base:    `{"type":"integer","minimum":0}`,
			current: `{"type":"integer","minimum":1}`,
			want:    "minimum 0 raised to 1",
		},
		{
			name:    "definition removed",
			base:    `{"$defs":{"a":{},"b":{}}}`,
			current: `{"$defs":{"a":{}}}`,
			want:    `$defs "b" removed`,
		},
		{
			name:    "added property",
			base:    `{"type":"object","properties":{"a":{}}}`,
			current: `{"type":"object","properties":{"a":{},"b":{}}}`,
		},
		{
			name:    "added enum value",
			base:    `{"enum":["a"]}`,
			current: `{"enum":["a","b"]}`,
		},
		{
			name:    "required removed",
			base:    `{"required":["a"]}`,
			current: `{"required":[]}`,
		},
		{
			name:    "additionalProperties opened",
			base:    `{"type":"object","additionalProperties":false}`,
			current: `{"type":"object","additionalProperties":true}`,
		},
		{
			name:    "type widened",
			base:    `{"type":"string"}`,
			current: `{"type":["string","null"]}`,
		},
		{
			name:    "definition added",
			base:    `{"$defs":{"a":{}}}`,
			current: `{"$defs":{"a":{},"b":{}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := breakingChanges(mustSchema(t, []byte(tc.base)), mustSchema(t, []byte(tc.current)))
			if tc.want == "" {
				if len(changes) != 0 {
					t.Errorf("additive change reported as breaking: %v", changes)
				}
				return
			}
			if !containsSubstring(changes, tc.want) {
				t.Errorf("breaking changes = %v; want one naming %q", changes, tc.want)
			}
		})
	}
}

// containsSubstring reports whether one reported change names want.
func containsSubstring(changes []string, want string) bool {
	for _, change := range changes {
		if strings.Contains(change, want) {
			return true
		}
	}
	return false
}

// breakingChanges reports every baseline contract the current document no
// longer honors. New properties, enum values, optional keys, definitions and
// accepted types are additive and never reported.
func breakingChanges(base, current map[string]any) []string {
	var changes []string
	walkBreaking("$", base, current, &changes)
	return changes
}

// walkBreaking compares one pair of schema nodes.
//
//nolint:revive // one pass over the schema keywords the generator emits
func walkBreaking(path string, base, current map[string]any, changes *[]string) {
	if base == nil || current == nil {
		return
	}
	if ref, ok := base["$ref"]; ok {
		if got, ok := current["$ref"]; !ok || !reflect.DeepEqual(ref, got) {
			*changes = append(*changes, path+": $ref changed or removed")
		}
		return
	}
	if _, ok := current["$ref"]; ok {
		*changes = append(*changes, path+": schema became a $ref")
		return
	}
	if want, ok := base["const"]; ok {
		if got, ok := current["const"]; !ok || !reflect.DeepEqual(want, got) {
			*changes = append(*changes, path+": const changed")
		}
	}
	checkTypes(path, base, current, changes)
	checkEnums(path, base, current, changes)
	checkRequired(path, base, current, changes)
	checkAdditional(path, base, current, changes)
	checkBounds(path, base, current, changes)
	checkProperties(path, base, current, changes)
	checkDefs(path, base, current, changes)
	checkItems(path, base, current, changes)
	checkAnyOf(path, base, current, changes)
}

// checkTypes reports a type that the current schema no longer accepts.
func checkTypes(path string, base, current map[string]any, changes *[]string) {
	want, constrained := typeSet(base["type"])
	got, stillConstrained := typeSet(current["type"])
	if !stillConstrained {
		return
	}
	if !constrained {
		*changes = append(*changes, path+": type constraint added")
		return
	}
	for typ := range want {
		if !got[typ] {
			*changes = append(*changes, fmt.Sprintf("%s: type %q no longer accepted", path, typ))
		}
	}
}

// typeSet normalizes a type keyword; ok is false when it is unconstrained.
func typeSet(raw any) (map[string]bool, bool) {
	switch typ := raw.(type) {
	case string:
		return map[string]bool{typ: true}, true
	case []any:
		out := make(map[string]bool, len(typ))
		for _, item := range typ {
			name, ok := item.(string)
			if !ok {
				return nil, false
			}
			out[name] = true
		}
		return out, true
	default:
		return nil, false
	}
}

// checkEnums reports a value the current enum no longer accepts. Dropping the
// enum keyword itself widens the contract, so it is additive.
func checkEnums(path string, base, current map[string]any, changes *[]string) {
	want, ok := base["enum"].([]any)
	if !ok {
		return
	}
	got, ok := current["enum"].([]any)
	if !ok {
		return
	}
	for _, member := range want {
		if !enumContains(got, member) {
			*changes = append(*changes, fmt.Sprintf("%s: enum value %v removed", path, member))
		}
	}
}

// checkRequired reports a key that became required.
func checkRequired(path string, base, current map[string]any, changes *[]string) {
	want := nameSet(base["required"])
	for name := range nameSet(current["required"]) {
		if !want[name] {
			*changes = append(*changes, fmt.Sprintf("%s: newly required %q", path, name))
		}
	}
}

// checkAdditional reports additionalProperties closing.
func checkAdditional(path string, base, current map[string]any, changes *[]string) {
	if openSchema(base["additionalProperties"]) && !openSchema(current["additionalProperties"]) {
		*changes = append(*changes, path+": additionalProperties closed")
	}
}

// checkBounds reports a numeric range that narrowed.
func checkBounds(path string, base, current map[string]any, changes *[]string) {
	checkMinimum(path, base, current, changes)
	checkMaximum(path, base, current, changes)
}

// checkMinimum reports a raised or newly added lower bound.
func checkMinimum(path string, base, current map[string]any, changes *[]string) {
	low, bounded := boundOf(base, keyMinimum)
	if !bounded {
		if _, ok := boundOf(current, keyMinimum); ok {
			*changes = append(*changes, path+": minimum added")
		}
		return
	}
	if got, ok := boundOf(current, keyMinimum); ok && got > low {
		*changes = append(*changes, fmt.Sprintf("%s: minimum %v raised to %v", path, low, got))
	}
}

// checkMaximum reports a lowered or newly added upper bound.
func checkMaximum(path string, base, current map[string]any, changes *[]string) {
	high, bounded := boundOf(base, keyMaximum)
	if !bounded {
		if _, ok := boundOf(current, keyMaximum); ok {
			*changes = append(*changes, path+": maximum added")
		}
		return
	}
	if got, ok := boundOf(current, keyMaximum); ok && got < high {
		*changes = append(*changes, fmt.Sprintf("%s: maximum %v lowered to %v", path, high, got))
	}
}

// checkProperties reports removed properties and compares the survivors.
func checkProperties(path string, base, current map[string]any, changes *[]string) {
	want := objectOf(base, "properties")
	got := objectOf(current, "properties")
	for name, sub := range want {
		next, ok := got[name]
		if !ok {
			*changes = append(*changes, fmt.Sprintf("%s: property %q removed", path, name))
			continue
		}
		walkSubschema(path+".properties."+name, sub, next, changes)
	}
}

// checkDefs reports removed definitions and compares the survivors.
func checkDefs(path string, base, current map[string]any, changes *[]string) {
	want := objectOf(base, "$defs")
	got := objectOf(current, "$defs")
	for name, sub := range want {
		next, ok := got[name]
		if !ok {
			*changes = append(*changes, fmt.Sprintf("%s: $defs %q removed", path, name))
			continue
		}
		walkSubschema(path+".$defs."+name, sub, next, changes)
	}
}

// checkItems compares array element schemas.
func checkItems(path string, base, current map[string]any, changes *[]string) {
	walkSubschema(path+".items", base["items"], current["items"], changes)
}

// checkAnyOf compares tagged-union branches by position; dropping a branch
// narrows the accepted shapes.
func checkAnyOf(path string, base, current map[string]any, changes *[]string) {
	want, ok := base["anyOf"].([]any)
	if !ok {
		return
	}
	got, ok := current["anyOf"].([]any)
	if !ok {
		return
	}
	if len(got) < len(want) {
		*changes = append(*changes, path+": anyOf branch removed")
	}
	for i := range want {
		if i >= len(got) {
			break
		}
		walkSubschema(fmt.Sprintf("%s.anyOf[%d]", path, i), want[i], got[i], changes)
	}
}

// walkSubschema recurses when both sides are schema objects.
func walkSubschema(path string, base, current any, changes *[]string) {
	baseMap, baseOK := base.(map[string]any)
	currentMap, currentOK := current.(map[string]any)
	if baseOK && currentOK {
		walkBreaking(path, baseMap, currentMap, changes)
	}
}

// objectOf returns one nested object member, or nil when absent/mistyped.
func objectOf(m map[string]any, key string) map[string]any {
	obj, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	return obj
}

// nameSet turns a required array into a set.
func nameSet(raw any) map[string]bool {
	list, ok := raw.([]any)
	if !ok {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(list))
	for _, item := range list {
		if name, ok := item.(string); ok {
			out[name] = true
		}
	}
	return out
}

// openSchema reports whether additionalProperties leaves the object open.
func openSchema(raw any) bool {
	closed, ok := raw.(bool)
	return !ok || closed
}
