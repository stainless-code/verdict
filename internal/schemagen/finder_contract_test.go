package schemagen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

func TestAggregateReviewVersionContract(t *testing.T) {
	schema := loadSchemaDocument(t, "review.schema.json")
	cases := []struct {
		name, doc string
		valid     bool
	}{
		{"finder", `{"schema_version":"1","findings":[],"summary":"Reviewed."}`, true},
		{"aggregate", `{"schema_version":"2","execution":{"id":"execution-A","finders":2},"findings":[],"summary":"Reviewed."}`, true},
		{"stripped", `{"schema_version":"2","findings":[],"summary":"Reviewed."}`, false},
		{"downgraded", `{"schema_version":"1","execution":{"id":"execution-A","finders":2},"findings":[],"summary":"Reviewed."}`, false},
		{"null reference", `{"schema_version":"2","execution":null,"findings":[],"summary":"Reviewed."}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := parityViolations(t, schema, tc.doc)
			assertRuntimeReviewValidity(t, tc.doc, tc.valid)
			if (len(failures) == 0) != tc.valid {
				t.Fatalf("valid=%v failures=%v", tc.valid, failures)
			}
		})
	}
}

func TestRunExecutionResponseSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "envelope.schema.json")
	data := &core.RunData{
		Agent: "claude", Binary: "claude", Argv: []string{"claude", "-p", "<prompt>"},
		Bundle: "bundle", Artifact: "review.json", Timeout: 300, Execution: &core.RunExecution{
			ID: "execution-A", Status: "partial", Counts: &core.FinderSummary{Completed: 1, Pending: 1},
			Failures: []core.RunPhaseFailure{{Phase: "x-finder-1", Code: "agent-failed"}},
		},
	}
	validateWire(t, schema, "run data", defOf(t, schema, "runData"), decodeWire(t, data.Wire()))
}

func TestNullableCostSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "envelope.schema.json")
	data := core.ReportCost{Runs: 2}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"input_tokens":null`) {
		t.Fatalf("unknown usage = %s", raw)
	}
	validateWire(t, schema, "report cost", defOf(t, schema, "reportCost"), decodeWire(t, data))
	for _, cost := range []struct {
		doc any
		def string
	}{
		{decodeWire(t, core.CostTier{}), "costTier"},
		{decodeWire(t, core.CostTiers{}), "costTiers"},
	} {
		validateWire(t, schema, cost.def, defOf(t, schema, cost.def), cost.doc)
	}
}

func TestAutomaticPlanSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "panel-plan.schema.json")
	cases := []struct {
		name, doc string
		valid     bool
	}{
		{"complete", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[]}`, true},
		{"missing review binding", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","checks":[],"sources":[]}`, false},
		{"missing sources", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[]}`, false},
		{"merged source", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[{"sig":"kept","origins":[{"finder":"finder-1","index":0,"finding":{}},{"finder":"finder-2","index":0,"finding":{}}],"merged":{"sig":"dropped","checker":"finder-2"}}]}`, true},
		{"merged source without checker", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[{"sig":"kept","origins":[{"finder":"finder-1","index":0,"finding":{}},{"finder":"finder-2","index":0,"finding":{}}],"merged":{"sig":"dropped"}}]}`, false},
		{"merged source with an unknown checker", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[{"sig":"kept","origins":[{"finder":"finder-1","index":0,"finding":{}},{"finder":"finder-2","index":0,"finding":{}}],"merged":{"sig":"dropped","checker":"finder-3"}}]}`, false},
		{"merged source with an extra key", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[{"sig":"kept","origins":[{"finder":"finder-1","index":0,"finding":{}},{"finder":"finder-2","index":0,"finding":{}}],"merged":{"sig":"dropped","checker":"finder-1","why":"x"}}]}`, false},
		{"merged source null", `{"schema_version":"2","head_sha":"head","execution_id":"execution-A","diff_sha256":"diff","review_sha256":"review","checks":[],"sources":[{"sig":"kept","origins":[{"finder":"finder-1","index":0,"finding":{}},{"finder":"finder-2","index":0,"finding":{}}],"merged":null}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := parityViolations(t, schema, tc.doc)
			if (len(failures) == 0) != tc.valid {
				t.Fatalf("valid=%v failures=%v", tc.valid, failures)
			}
		})
	}
}

func assertRuntimeReviewValidity(t *testing.T, doc string, valid bool) {
	t.Helper()
	runtime := recordcore.CheckReview([]byte(doc), "")
	if (runtime.Errors == 0) != valid {
		t.Fatalf("runtime validity differs: %+v", runtime)
	}
}

func TestFinderCountConfigSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "config.schema.json")
	for _, tc := range []struct {
		doc   string
		valid bool
	}{
		{`{"schema_version":"1","defaults":{"finders":1}}`, true},
		{`{"schema_version":"1","defaults":{"finders":2}}`, true},
		{`{"schema_version":"1","defaults":{"finders":0}}`, false},
		{`{"schema_version":"1","defaults":{"finders":3}}`, false},
		{`{"schema_version":"1","scopes":[{"name":"api","include":["*.go"],"defaults":{"finders":1}}]}`, false},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			if failures := parityViolations(t, schema, tc.doc); (len(failures) == 0) != tc.valid {
				t.Fatalf("valid=%v failures=%v", tc.valid, failures)
			}
		})
	}
}
