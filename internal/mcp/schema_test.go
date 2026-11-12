package mcp

import (
	"reflect"
	"slices"
	"testing"
)

// TestSchemaEnums proves the reflector copies the closed vocabularies the Go
// request structs already declare, so a bad status is a schema error before
// the call rather than a runtime refusal.
//
//nolint:revive,gocognit // the vocabulary matrix reads as one block
func TestSchemaEnums(t *testing.T) {
	t.Parallel()
	statuses := []any{"open", "addressed", "wont-fix", "false-positive", "deferred"}
	providers := []any{"github", "gitlab", "gitcafe", "forgejo", "bitbucket", "azure"}
	cases := []struct {
		name string
		tool string
		path []string
		want []any
	}{
		{"triage status", "triage", []string{"status"}, statuses},
		{"report status", "report", []string{"flags", "status"}, append([]any{"all"}, statuses...)},
		{"report kind", "report", []string{"flags", "kind"}, []any{"all", "inline", "section"}},
		{"report category", "report", []string{"flags", "category"}, []any{"logic", "edge-case", "security", "performance", "accidental-commit", "style", "docs", "other"}},
		{"report check", "report", []string{"flags", "check"}, []any{"critical", "important", "informational"}},
		{"report live", "report", []string{"flags", "live"}, []any{"auto", "strict", "skip"}},
		{"next status", "next", []string{"status"}, append([]any{"all"}, statuses...)},
		{"init preset", "init", []string{"preset"}, []any{"chill", "standard", "strict"}},
		{"learn rigor", "learn", []string{"rigor"}, []any{"said", "pointed", "walked", "ran", "reproduced"}},
		{"post approve", "post", []string{"flags", "approve"}, []any{"true", "false"}},
		{"fetch provider", "fetch", []string{"flags", "provider"}, providers},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node := inputSchema(findTool(t, tc.tool))
			for _, key := range tc.path {
				props, ok := node[schemaProperties].(map[string]any)
				if !ok {
					t.Fatalf("%s: %v has no properties", tc.name, key)
				}
				node, ok = props[key].(map[string]any)
				if !ok {
					t.Fatalf("%s: property %q is not an object", tc.name, key)
				}
			}
			got, ok := node[schemaEnum].([]any)
			if !ok {
				t.Fatalf("%s: no enum keyword in %v", tc.name, node)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s enum = %v, want %v", tc.name, got, tc.want)
			}
			if node[schemaType] != typeString {
				t.Errorf("%s enum lost its string type: %v", tc.name, node)
			}
		})
	}
}

// TestIndexSchemaListsEveryArgument proves the published index schema
// advertises every argument the index request carries, including pr for the
// blast-radius bundle form: a schema-respecting client must be able to send
// it, and additionalProperties:false means an omitted field is unsendable.
func TestIndexSchemaListsEveryArgument(t *testing.T) {
	t.Parallel()
	props, ok := inputSchema(findTool(t, "index"))[schemaProperties].(map[string]any)
	if !ok {
		t.Fatal("index schema has no properties")
	}
	got := make([]string, 0, len(props))
	for key := range props {
		got = append(got, key)
	}
	slices.Sort(got)
	want := []string{
		"at", "commits", "depth", "from", "limit", "minCount", "path", "paths",
		"pr", "query", "since", "symbol",
	}
	if !slices.Equal(got, want) {
		t.Errorf("index schema arguments = %v; want %v", got, want)
	}
}

// TestSchemaBounds proves min/max struct tags become JSON Schema bounds (the
// MCP request surface has no numeric field yet, so the test uses a synthetic
// shape).
//
//nolint:revive // the bound matrix reads as one block
func TestSchemaBounds(t *testing.T) {
	t.Parallel()
	type bounded struct {
		Count int     `max:"5"   min:"1"`
		Ratio float64 `max:"2.5" min:"0.5"`
		Free  int
	}
	props, ok := objectSchema(reflect.TypeFor[bounded](), nil)[schemaProperties].(map[string]any)
	if !ok {
		t.Fatal("bounded schema has no properties")
	}
	count, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatalf("count property = %v", props["count"])
	}
	if count[schemaMinimum] != 1 || count[schemaMaximum] != 5 {
		t.Errorf("count bounds = %v/%v", count[schemaMinimum], count[schemaMaximum])
	}
	ratio, ok := props["ratio"].(map[string]any)
	if !ok {
		t.Fatalf("ratio property = %v", props["ratio"])
	}
	if ratio[schemaMinimum] != 0.5 || ratio[schemaMaximum] != 2.5 {
		t.Errorf("ratio bounds = %v/%v", ratio[schemaMinimum], ratio[schemaMaximum])
	}
	free, ok := props["free"].(map[string]any)
	if !ok {
		t.Fatalf("free property = %v", props["free"])
	}
	if _, bounded := free[schemaMinimum]; bounded {
		t.Error("untagged field gained a minimum")
	}
}
