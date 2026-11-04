package schemagen

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestEnvelopeSchemaMatchesRenderer pins the published envelope key set to
// the keys RenderEnvelope actually emits. The description is hand-maintained
// while the key set is typed, so this guard fails on the next field add or
// prose drift instead of relying on review.
func TestEnvelopeSchemaMatchesRenderer(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	env := core.RenderEnvelope("report", "acme/app", "5101",
		"2026-11-02T12:00:00Z", "", nil, jq.NewObj(), nil)

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("envelope schema has no properties object")
	}
	required, ok := doc["required"].([]any)
	if !ok {
		t.Fatalf("envelope schema has no required array")
	}
	if len(env.Keys) != len(props) || len(env.Keys) != len(required) {
		t.Fatalf("envelope keys = %v; schema properties = %d, required = %d",
			env.Keys, len(props), len(required))
	}
	if got := anyStrings(required); !slices.Equal(got, env.Keys) {
		t.Errorf("required = %v; RenderEnvelope emits %v", got, env.Keys)
	}
	propKeys := make([]string, 0, len(props))
	for key := range props {
		propKeys = append(propKeys, key)
	}
	slices.Sort(propKeys)
	sortedKeys := slices.Clone(env.Keys)
	slices.Sort(sortedKeys)
	if !slices.Equal(propKeys, sortedKeys) {
		t.Errorf("properties = %v; RenderEnvelope emits %v", propKeys, sortedKeys)
	}

	checkEnvelopeDescription(t, doc, len(props))
}

// checkEnvelopeDescription requires the envelope description to name the
// renderer and to spell the property count it publishes.
func checkEnvelopeDescription(t *testing.T, doc map[string]any, keys int) {
	t.Helper()
	desc, ok := doc["description"].(string)
	if !ok || desc == "" {
		t.Fatalf("envelope schema has no description")
	}
	if !strings.Contains(desc, "RenderEnvelope") {
		t.Errorf("envelope description does not name RenderEnvelope: %q", desc)
	}
	match := keyCountRe.FindStringSubmatch(desc)
	if match == nil {
		t.Fatalf("envelope description does not spell a key count: %q", desc)
	}
	count, ok := numberWords[match[1]]
	if !ok {
		t.Fatalf("envelope description uses unknown number word %q", match[1])
	}
	if count != keys {
		t.Errorf("envelope description says %q keys, schema has %d", match[1], keys)
	}
}

// TestGeneratedDescriptionsAvoidShellReferences forbids descriptions that
// cite the deleted bash emitters; the schema is the machine contract, so it
// describes the Go renderer only.
func TestGeneratedDescriptionsAvoidShellReferences(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, file := range files {
		checkNoShellReferences(t, file)
	}
}

// shellReferenceRe matches prose that cites the deleted bash emitters.
var shellReferenceRe = regexp.MustCompile(`json_redirect|jq -n|shell`)

// checkNoShellReferences fails when one generated schema's descriptions cite
// the deleted bash emitters.
func checkNoShellReferences(t *testing.T, file File) {
	t.Helper()
	for _, desc := range descriptions(t, file) {
		if hit := shellReferenceRe.FindString(desc); hit != "" {
			t.Errorf("%s description cites %q: %q", file.Name, hit, desc)
		}
	}
}

// descriptions collects every description string in one generated schema.
func descriptions(t *testing.T, file File) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(file.Data, &doc); err != nil {
		t.Fatalf("parse %s: %v", file.Name, err)
	}
	var out []string
	collectDescriptions(doc, &out)
	return out
}

// collectDescriptions walks one decoded schema node for description keys.
func collectDescriptions(node any, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		collectMapDescriptions(v, out)
	case []any:
		for _, child := range v {
			collectDescriptions(child, out)
		}
	}
}

// collectMapDescriptions collects one object's description, then descends.
func collectMapDescriptions(object map[string]any, out *[]string) {
	if desc, ok := object["description"].(string); ok {
		*out = append(*out, desc)
	}
	for _, child := range object {
		collectDescriptions(child, out)
	}
}

// anyStrings converts a decoded JSON string array.
func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		s, ok := value.(string)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

// keyCountRe matches the spelled property count ("nine keys").
var keyCountRe = regexp.MustCompile(`\b([a-z]+) keys\b`)

// numberWords maps a spelled count to its value.
var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}
