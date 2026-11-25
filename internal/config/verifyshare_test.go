package config

import (
	"slices"
	"testing"
)

func TestVerifyShareParsed(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","verify":{"share":["node_modules","web/node_modules",".venv"]}}`)
	if cfg.Verify == nil || !slices.Equal(cfg.Verify.Share, []string{"node_modules", "web/node_modules", ".venv"}) {
		t.Fatalf("cfg.Verify = %+v", cfg.Verify)
	}
	empty := mustParse(t, `{"schema_version":"1","verify":{"share":[]}}`)
	if empty.Verify == nil || len(empty.Verify.Share) != 0 {
		t.Fatalf("an empty share list = %+v, want an empty list", empty.Verify)
	}
}

func TestVerifyShareRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		give string
	}{
		{name: "dotdot", give: `["../outside"]`},
		{name: "inner dotdot", give: `["a/../b"]`},
		{name: "absolute", give: `["/etc"]`},
		{name: "drive", give: `["C:/deps"]`},
		{name: "backslash", give: `["deps\\x"]`},
		{name: "dot", give: `["."]`},
		{name: "trailing slash", give: `["node_modules/"]`},
		{name: "git dir", give: `[".git"]`},
		{name: "git dir any case", give: `["a/.GIT/x"]`},
		{name: "repeat", give: `["node_modules","node_modules"]`},
		{name: "nested", give: `["node_modules","node_modules/.bin"]`},
		{name: "parent after child", give: `["web/node_modules","web"]`},
		{name: "non-string", give: `[1]`},
		{name: "control", give: `["a\u0001"]`},
		{name: "not a list", give: `"node_modules"`},
		{name: "null", give: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := Parse([]byte(`{"schema_version":"1","verify":{"share":` + tc.give + `}}`))
			requireRefusal(t, ref, CodeBadConfig, "")
		})
	}
	for _, give := range []string{`{"schema_version":"1","verify":{"mount":[]}}`, `{"schema_version":"1","verify":null}`} {
		_, ref := Parse([]byte(give))
		requireRefusal(t, ref, CodeBadConfig, "")
	}
}
