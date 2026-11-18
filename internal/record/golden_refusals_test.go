package record_test

import (
	"strings"
	"testing"
)

// refuseFixture writes a local bundle with the given review body and extra
// files, for the typed-refusal legs.
func refuseFixture(t *testing.T, dir, review string, extra map[string]string) {
	t.Helper()
	files := map[string]string{
		".verdict/state/local-rf/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".verdict/state/local-rf/full.diff":   genDiff(2),
		".verdict/state/local-rf/review.json": review,
	}
	for rel, content := range extra {
		files[".verdict/state/local-rf/"+rel] = content
	}
	writeFiles(t, dir, files)
}

const refuseBody = `{"schema_version":"1","findings":[]}`

// TestGoldenRefusals checks every Commit-A typed refusal matches byte-for-byte:
// codes, messages, exit codes, and no merge artifacts on either side.
func TestGoldenRefusals(t *testing.T) {
	finding := func(path, claim, sugg string) string {
		return `{"schema_version":"1","findings":[{"path":` + path +
			`,"line":2,"side":"RIGHT","severity":"important","claim":` + claim +
			`,"quote":"new line","confidence":"high","suggestion":` + sugg + `}]}`
	}
	refuse := func(name, review string, extra map[string]string) {
		t.Helper()
		goldenBoth(t, "refuse/"+name, "local-rf", func(t *testing.T, dir string) {
			t.Helper()
			refuseFixture(t, dir, review, extra)
		}, "--json")
	}
	refuse("corrupt-review", `{broken`, nil)
	refuse("non-object-review", `[1,2]`, nil)
	refuse("non-object-finding", strings.Replace(refuseBody, `"findings":[]`, `"findings":[5]`, 1), nil)
	refuse("numeric-path", finding(`5`, `"x claim with enough substance."`, `null`), nil)
	refuse("numeric-claim", finding(`"notes.txt"`, `5`, `null`), nil)
	refuse("numeric-suggestion", finding(`"notes.txt"`, `"x claim with enough substance."`, `5`), nil)
	refuse("true-suggestion", finding(`"notes.txt"`, `"x claim with enough substance."`, `true`), nil)
	refuse("object-path", finding(`{"a":1}`, `"x claim with enough substance."`, `null`), nil)
	refuse("non-string-resolved", strings.Replace(refuseBody, `"findings":[]`, `"findings":[],"resolved":[5]`, 1), nil)
	refuse("non-array-resolved", strings.Replace(refuseBody, `"findings":[]`, `"findings":[],"resolved":5`, 1), nil)
	refuse("non-object-triage", strings.Replace(refuseBody, `"findings":[]`, `"findings":[],"triage":[5]`, 1), nil)
	refuse("non-array-triage", strings.Replace(refuseBody, `"findings":[]`, `"findings":[],"triage":{"sig":"x"}`, 1), nil)
	refuse("non-array-verification", refuseBody, map[string]string{"verification.json": `{"sig":"x"}`})
	refuse("non-object-verification", refuseBody, map[string]string{"verification.json": `[5]`})
	refuse("corrupt-prior", refuseBody, map[string]string{"findings.json": `{broken`})
	refuse("non-list-prior", refuseBody, map[string]string{"findings.json": `{"sig":"x"}`})
	refuse("true-prior", refuseBody, map[string]string{"findings.json": `true`})
	// Absence-shaped priors read as no prior (success).
	refuse("null-prior", refuseBody, map[string]string{"findings.json": `null`})
	refuse("false-prior", refuseBody, map[string]string{"findings.json": `false`})
	refuse("empty-prior", refuseBody, map[string]string{"findings.json": ``})
	// Invalid UTF-8 is not JSON on either side (M5): both refuse typed.
	refuse("utf8-scrubbed-claim", "{\"schema_version\":\"1\",\"findings\":[{\"path\":\"notes.txt\",\"line\":2,\"severity\":\"important\",\"claim\":\"bad \xff byte here with enough substance.\",\"quote\":\"new line\",\"confidence\":\"high\"}]}", nil)
}

// TestGoldenSourceShapes checks corrupt/dirty source.json and corrupt PR.json.
func TestGoldenSourceShapes(t *testing.T) {
	// Corrupt source.json degrades to the dirty-head path.
	goldenBoth(t, "source/corrupt", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{oops`})
	}, "--json")
	// A missing head_sha reads dirty too.
	goldenBoth(t, "source/dirty-head", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`})
	}, "--json")
	// A string "true" dirty flag is inert for record (both ignore it).
	goldenBoth(t, "source/dirty-string", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","dirty":"true"}`})
	}, "--json")
	// Corrupt PR.json on a provider bundle refuses not-local.
	goldenBoth(t, "source/corrupt-pr", "4251", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".verdict/state/4251/PR.json":   `{oops`,
			".verdict/state/4251/full.diff": genDiff(2),
		})
	})
	// A garbage provider string records (only github is special-cased).
	goldenBoth(t, "source/garbage-provider", "47", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".verdict/state/47/PR.json":   `{"provider":"weird","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
			".verdict/state/47/full.diff": genDiff(2),
		})
	})
}
