package config

import "testing"

func TestTestsDraftParsedAndResolved(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","tests":{"draft":"off"}}`)
	if cfg.Tests == nil || cfg.Tests.Draft != "off" {
		t.Fatalf("cfg.Tests = %+v", cfg.Tests)
	}
	res := resolveMust(t, &Inputs{Repo: cfg})
	if res.Tests == nil || res.Tests.Draft != "off" {
		t.Fatalf("res.Tests = %+v", res.Tests)
	}
}

func TestTestsDraftDefaultAbsent(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1"}`)
	if cfg.Tests != nil {
		t.Fatalf("cfg.Tests = %+v, want nil", cfg.Tests)
	}
	res := resolveMust(t, &Inputs{Repo: cfg})
	if res.Tests != nil {
		t.Fatalf("res.Tests = %+v, want nil", res.Tests)
	}
}

func TestTestsDraftRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		give string
		code string
	}{
		{name: "bad enum", give: `{"schema_version":"1","tests":{"draft":"sometimes"}}`, code: CodeBadConfig},
		{name: "unknown field", give: `{"schema_version":"1","tests":{"keep":true}}`, code: CodeBadConfig},
		{name: "null draft", give: `{"schema_version":"1","tests":{"draft":null}}`, code: CodeBadConfig},
		{name: "null tests", give: `{"schema_version":"1","tests":null}`, code: CodeBadConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := Parse([]byte(tc.give))
			requireRefusal(t, ref, tc.code, "")
		})
	}
}
