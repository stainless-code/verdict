package config

import "testing"

func TestFindersResolveAndCapture(t *testing.T) {
	for _, tc := range []struct {
		name, personal, repo, layer string
		want                        int
	}{
		{"built-in", `{}`, `{"schema_version":"1"}`, "built-in", 2},
		{"personal", `{"review":{"finders":1}}`, `{"schema_version":"1"}`, "personal", 1},
		{"repo", `{"review":{"finders":1}}`, `{"schema_version":"1","defaults":{"finders":2}}`, "repo", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFinderCapture(t, tc.personal, tc.repo, tc.layer, tc.want)
		})
	}
}

func TestFindersRejectInvalidAndScoped(t *testing.T) {
	for _, raw := range []string{`0`, `3`, `-1`, `1.5`, `"1"`, `null`, `true`} {
		t.Run(raw, func(t *testing.T) {
			if _, ref := Parse([]byte(`{"schema_version":"1","defaults":{"finders":` + raw + `}}`)); ref == nil || ref.Code != "bad-config" {
				t.Fatalf("invalid %s accepted: %v", raw, ref)
			}
		})
	}
}

func TestFindersRejectScoped(t *testing.T) {
	if _, ref := Parse([]byte(`{"schema_version":"1","scopes":[{"name":"api","include":["*.go"],"defaults":{"finders":1}}]}`)); ref == nil || ref.Code != "bad-scope" {
		t.Fatalf("scoped count accepted: %v", ref)
	}
}

func assertFinderCapture(t *testing.T, personalRaw, repoRaw, layer string, want int) {
	t.Helper()
	personal, ref := ParsePersonal([]byte(personalRaw))
	if ref != nil {
		t.Fatal(ref)
	}
	repo, ref := Parse([]byte(repoRaw))
	if ref != nil {
		t.Fatal(ref)
	}
	in := &Inputs{Personal: personal, Repo: repo}
	resolved, ref := Resolve(in)
	if ref != nil {
		t.Fatal(ref)
	}
	value := findEvidenceValue(t, Capture(in, resolved, nil, "fixed").Values, "finders")
	if value.Value != want || value.Origin.Layer != layer {
		t.Fatalf("capture = %+v; want %d %s", value, want, layer)
	}
}
