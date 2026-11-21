package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestBudgetResolveAndCapture proves defaults.budget resolves from the
// repository layer and reaches the capture with that origin, so `run` can read
// the requested depth out of run.json.
func TestBudgetResolveAndCapture(t *testing.T) {
	repo, ref := Parse([]byte(`{"schema_version":"1","defaults":{"budget":"auto"}}`))
	if ref != nil {
		t.Fatal(ref)
	}
	in := &Inputs{Repo: repo}
	resolved, ref := Resolve(in)
	if ref != nil {
		t.Fatal(ref)
	}
	value := findEvidenceValue(t, Capture(in, resolved, nil, "fixed").Values, "budget")
	if value.Value != "auto" || value.Origin.Layer != LayerRepo {
		t.Fatalf("captured budget = %+v; want auto from the repo layer", value)
	}
}

// TestSensitivePathsResolveAndCapture proves the configured globs reach the
// capture verbatim, in configuration order.
func TestSensitivePathsResolveAndCapture(t *testing.T) {
	repo, ref := Parse([]byte(`{"schema_version":"1","defaults":{"sensitive_paths":["**/payments/**","app/secrets/**"]}}`))
	if ref != nil {
		t.Fatal(ref)
	}
	in := &Inputs{Repo: repo}
	resolved, ref := Resolve(in)
	if ref != nil {
		t.Fatal(ref)
	}
	value := findEvidenceValue(t, Capture(in, resolved, nil, "fixed").Values, "sensitive_paths")
	got, ok := value.Value.([]string)
	if !ok || !slices.Equal(got, []string{"**/payments/**", "app/secrets/**"}) {
		t.Fatalf("captured sensitive_paths = %#v", value.Value)
	}
}

// TestBudgetUnsetStaysOutOfTheCapture proves an unset defaults.budget and
// defaults.sensitive_paths leave the effective defaults byte-identical: the
// capture a pre-budget fetch wrote re-verifies unchanged.
func TestBudgetUnsetStaysOutOfTheCapture(t *testing.T) {
	raw, err := json.Marshal(EffectiveDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"budget", "sensitive_paths"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("unset effective defaults carry %q: %s", key, raw)
		}
	}
}

// TestBudgetRejectsInvalidValues proves the parser names the key path for every
// value a depth can carry but the vocabulary does not.
func TestBudgetRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		code string
		want string
	}{
		{"bad enum", `{"budget":"fast"}`, CodeBadConfig, "$.defaults.budget"},
		{"misplaced null", `{"budget":null}`, CodeBadConfig, "$.defaults.budget"},
		{"wrong type", `{"budget":1}`, CodeBadConfig, "$.defaults.budget"},
		{"empty glob array", `{"sensitive_paths":[]}`, CodeBadConfig, "$.defaults.sensitive_paths"},
		{"absolute glob", `{"sensitive_paths":["/etc/**"]}`, CodeBadConfig, "$.defaults.sensitive_paths[0]"},
		{"escaping glob", `{"sensitive_paths":["../secrets/**"]}`, CodeBadConfig, "$.defaults.sensitive_paths[0]"},
		{"backslash glob", `{"sensitive_paths":["app\\secrets"]}`, CodeBadConfig, "$.defaults.sensitive_paths[0]"},
		{"unknown key", `{"budgets":"auto"}`, CodeBadConfig, "budgets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := Parse([]byte(`{"schema_version":"1","defaults":` + tc.raw + `}`))
			if ref == nil || ref.Code != tc.code || !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("refusal = %v; want %s naming %s", ref, tc.code, tc.want)
			}
		})
	}
}

// TestBudgetRefusesScopedValues proves both keys are global-only: a scope that
// sets either refuses bad-scope instead of silently never reaching the
// classifier.
func TestBudgetRefusesScopedValues(t *testing.T) {
	for _, raw := range []string{`{"budget":"deep"}`, `{"sensitive_paths":["**/payments/**"]}`} {
		t.Run(raw, func(t *testing.T) {
			doc := `{"schema_version":"1","scopes":[{"name":"api","include":["*.go"],"defaults":` + raw + `}]}`
			_, ref := Parse([]byte(doc))
			if ref == nil || ref.Code != "bad-scope" || !strings.Contains(ref.Message, "global-only") {
				t.Fatalf("refusal = %v; want bad-scope global-only", ref)
			}
		})
	}
}
