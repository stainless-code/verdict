package config

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestSpecCoverageMinDefault(t *testing.T) {
	t.Parallel()
	res, ref := Resolve(&Inputs{})
	if ref != nil {
		t.Fatalf("Resolve() refused: %+v", ref)
	}
	assertSpecCoverageResolved(t, res, 80, LayerBuiltIn)
}

func TestSpecCoverageMinValidBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		doc  string
		want int
	}{
		{doc: `{"schema_version":"1","defaults":{"spec_coverage_min":1}}`, want: 1},
		{doc: `{"schema_version":"1","defaults":{"spec_coverage_min":100}}`, want: 100},
	} {
		cfg, ref := Parse([]byte(tc.doc))
		if ref != nil {
			t.Fatalf("Parse(%s) refused: %+v", tc.doc, ref)
		}
		if cfg.Defaults == nil || cfg.Defaults.SpecCoverageMin == nil || *cfg.Defaults.SpecCoverageMin != tc.want {
			t.Fatalf("Parse(%s) SpecCoverageMin = %v, want %d", tc.doc, cfg.Defaults, tc.want)
		}
	}
}

func TestSpecCoverageMinInvalidRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		doc  string
	}{
		{name: "zero", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":0}}`},
		{name: "over 100", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":101}}`},
		{name: "negative", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":-5}}`},
		{name: "float", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":80.5}}`},
		{name: "string", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":"80"}}`},
		{name: "null", doc: `{"schema_version":"1","defaults":{"spec_coverage_min":null}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := Parse([]byte(tc.doc))
			assertSpecRefusal(t, ref, CodeBadConfig)
		})
	}
}

func TestSpecCoverageMinPersonalOverride(t *testing.T) {
	t.Parallel()
	personal, ref := ParsePersonal([]byte(`{"review":{"spec_coverage_min":95}}`))
	if ref != nil {
		t.Fatalf("ParsePersonal refused: %+v", ref)
	}
	res, ref := Resolve(&Inputs{Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve refused: %+v", ref)
	}
	assertSpecCoverageResolved(t, res, 95, LayerPersonal)
}

func TestSpecCoverageMinRepoOverridesPersonal(t *testing.T) {
	t.Parallel()
	personal, ref := ParsePersonal([]byte(`{"review":{"spec_coverage_min":95}}`))
	if ref != nil {
		t.Fatalf("ParsePersonal refused: %+v", ref)
	}
	repo, ref := Parse([]byte(`{"schema_version":"1","defaults":{"spec_coverage_min":70}}`))
	if ref != nil {
		t.Fatalf("Parse refused: %+v", ref)
	}
	res, ref := Resolve(&Inputs{Repo: repo, Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve refused: %+v", ref)
	}
	assertSpecCoverageResolved(t, res, 70, LayerRepo)
}

func TestSpecCoverageMinPersonalOutOfRangeRefused(t *testing.T) {
	t.Parallel()
	_, ref := ParsePersonal([]byte(`{"review":{"spec_coverage_min":101}}`))
	assertSpecRefusal(t, ref, CodeBadConfig)
}

func TestSpecCoverageMinPerScopeRefused(t *testing.T) {
	t.Parallel()
	doc := `{"schema_version":"1","scopes":[{"name":"api","include":["api/**"],"defaults":{"spec_coverage_min":90}}]}`
	_, ref := Parse([]byte(doc))
	assertSpecRefusal(t, ref, CodeBadScope)
}

func TestSpecCoverageMinEvidenceCapture(t *testing.T) {
	t.Parallel()
	res, ref := Resolve(&Inputs{})
	if ref != nil {
		t.Fatalf("Resolve refused: %+v", ref)
	}
	ev := Capture(&Inputs{}, res, nil, "2026-11-19T00:00:00Z")
	v := findEvidenceValue(t, ev.Values, "spec_coverage_min")
	if v.Value != 80 || v.Origin.Layer != LayerBuiltIn {
		t.Fatalf("evidence spec_coverage_min = %+v, want Value=80 Layer=built-in", v)
	}
}

func findEvidenceValue(t *testing.T, values []EvidenceValue, key string) EvidenceValue {
	t.Helper()
	for _, v := range values {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("evidence.Values missing %q", key)
	return EvidenceValue{}
}

func assertSpecCoverageResolved(t *testing.T, res *Resolved, wantVal int, wantLayer string) {
	t.Helper()
	if res.Defaults.Values.SpecCoverageMin != wantVal {
		t.Fatalf("SpecCoverageMin = %d, want %d", res.Defaults.Values.SpecCoverageMin, wantVal)
	}
	if got := res.Defaults.Origins["spec_coverage_min"]; got.Layer != wantLayer {
		t.Fatalf("origin = %+v, want layer %q", got, wantLayer)
	}
}

func assertSpecRefusal(t *testing.T, ref *core.RefusalError, wantCode string) {
	t.Helper()
	if ref == nil {
		t.Fatalf("got nil refusal, want %s", wantCode)
	}
	if ref.Code != wantCode || !strings.Contains(ref.Message, "spec_coverage_min") {
		t.Fatalf("refusal = (%q, %q), want (%q, containing spec_coverage_min)", ref.Code, ref.Message, wantCode)
	}
}
