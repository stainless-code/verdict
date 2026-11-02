package record_test

import (
	"strings"
	"testing"
)

// refuseFixture writes a local bundle with the given review body and extra
// files, for the typed-refusal parity legs (C6/I2).
func refuseFixture(t *testing.T, dir, review string, extra map[string]string) {
	t.Helper()
	files := map[string]string{
		".review/local-rf/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
		".review/local-rf/full.diff":   genDiff(2),
		".review/local-rf/review.json": review,
	}
	for rel, content := range extra {
		files[".review/local-rf/"+rel] = content
	}
	writeFiles(t, dir, files)
}

const refuseBody = `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`

// TestParityRefusals checks every Commit-A typed refusal matches byte-for-byte:
// codes, messages, exit codes, and no merge artifacts on either side.
func TestParityRefusals(t *testing.T) {
	comment := func(path, body, sugg string) string {
		return `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":` +
			path + `,"line":2,"side":"RIGHT","body":` + body + `,"suggestion":` + sugg + `}]}`
	}
	refuse := func(name, review string, extra map[string]string) {
		t.Helper()
		runBoth(t, "refuse/"+name, "local-rf", func(t *testing.T, dir string) {
			t.Helper()
			refuseFixture(t, dir, review, extra)
		}, "--json")
	}
	refuse("corrupt-review", `{broken`, nil)
	refuse("non-object-review", `[1,2]`, nil)
	refuse("non-object-comment", strings.Replace(refuseBody, `"comments":[]`, `"comments":[5]`, 1), nil)
	refuse("numeric-path", comment(`5`, `"x body with enough substance."`, `null`), nil)
	refuse("numeric-body", comment(`"notes.txt"`, `5`, `null`), nil)
	refuse("numeric-suggestion", comment(`"notes.txt"`, `"x body with enough substance."`, `5`), nil)
	refuse("true-suggestion", comment(`"notes.txt"`, `"x body with enough substance."`, `true`), nil)
	refuse("object-path", comment(`{"a":1}`, `"x body with enough substance."`, `null`), nil)
	refuse("non-string-resolved", strings.Replace(refuseBody, `"comments":[]`, `"comments":[],"resolved":[5]`, 1), nil)
	refuse("non-array-resolved", strings.Replace(refuseBody, `"comments":[]`, `"comments":[],"resolved":"x"`, 1), nil)
	refuse("non-object-triage", strings.Replace(refuseBody, `"comments":[]`, `"comments":[],"triage":[5]`, 1), nil)
	refuse("non-array-triage", strings.Replace(refuseBody, `"comments":[]`, `"comments":[],"triage":{"sig":"x"}`, 1), nil)
	refuse("non-object-sections", strings.Replace(refuseBody, `"comments":[]`, `"comments":[],"sections":[5]`, 1), nil)
	refuse("non-array-verification", refuseBody, map[string]string{"verification.json": `{"sig":"x"}`})
	refuse("non-object-verification", refuseBody, map[string]string{"verification.json": `[5]`})
	refuse("corrupt-prior", refuseBody, map[string]string{"findings.json": `{broken`})
	refuse("non-list-prior", refuseBody, map[string]string{"findings.json": `{"sig":"x"}`})
	refuse("true-prior", refuseBody, map[string]string{"findings.json": `true`})
	// Absence-shaped priors read as no prior on both sides (success).
	refuse("null-prior", refuseBody, map[string]string{"findings.json": `null`})
	refuse("false-prior", refuseBody, map[string]string{"findings.json": `false`})
	refuse("empty-prior", refuseBody, map[string]string{"findings.json": ``})
	// Invalid UTF-8 is not JSON on either side (M5): both refuse typed.
	refuse("invalid-utf8-review", "{\"schema_version\":\"1\",\"body\":\"> ✅ marker plus enough length to pass the guard ok.\",\"approved\":false,\"comments\":[{\"path\":\"notes.txt\",\"line\":2,\"side\":\"RIGHT\",\"body\":\"bad \xff byte here with enough substance.\"}]}", nil)
}

// TestParitySourceShapes checks corrupt/dirty source.json and corrupt PR.json.
func TestParitySourceShapes(t *testing.T) {
	// Corrupt source.json degrades to the dirty-head path on both sides.
	runBoth(t, "source/corrupt", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{oops`})
	}, "--json")
	// A missing head_sha reads dirty too.
	runBoth(t, "source/dirty-head", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{"head_ref":"work"}`})
	}, "--json")
	// A string "true" dirty flag is inert for record (both ignore it).
	runBoth(t, "source/dirty-string", "local-rf", func(t *testing.T, dir string) {
		t.Helper()
		refuseFixture(t, dir, refuseBody, map[string]string{"source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dirty":"true"}`})
	}, "--json")
	// Corrupt PR.json on a provider bundle refuses not-local on both sides.
	runBoth(t, "source/corrupt-pr", "4251", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".review/4251/PR.json":   `{oops`,
			".review/4251/full.diff": genDiff(2),
		})
	})
	// A garbage provider string records (only github is special-cased).
	runBoth(t, "source/garbage-provider", "47", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".review/47/PR.json":   `{"provider":"weird","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			".review/47/full.diff": genDiff(2),
		})
	})
}
