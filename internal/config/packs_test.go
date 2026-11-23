package config

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestParsePacksSection proves the built-in pack composition parses: the
// merge switch and the three id lists land on the typed configuration.
func TestParsePacksSection(t *testing.T) {
	t.Parallel()
	cfg, ref := Parse([]byte(`{"schema_version":"1","packs":{` +
		`"merge_builtin":false,` +
		`"disable_packs":["verdict/shell","go"],` +
		`"disable_lenses":["security"],` +
		`"disable_rules":["sec-ssrf","go-context-lifecycle"]}}`))
	if ref != nil {
		t.Fatalf("Parse: %s: %s", ref.Code, ref.Message)
	}
	if cfg.Packs == nil {
		t.Fatal("packs section missing")
	}
	if cfg.Packs.MergeBuiltin == nil || *cfg.Packs.MergeBuiltin {
		t.Errorf("merge_builtin = %v, want false", cfg.Packs.MergeBuiltin)
	}
	if got, want := strings.Join(cfg.Packs.DisablePacks, ","), "verdict/shell,go"; got != want {
		t.Errorf("disable_packs = %q, want %q", got, want)
	}
	if got, want := strings.Join(cfg.Packs.DisableLenses, ","), "security"; got != want {
		t.Errorf("disable_lenses = %q, want %q", got, want)
	}
	if got, want := strings.Join(cfg.Packs.DisableRules, ","), "sec-ssrf,go-context-lifecycle"; got != want {
		t.Errorf("disable_rules = %q, want %q", got, want)
	}
}

// TestParsePacksSectionRefusals pins one diagnostic per broken shape: the
// section is closed, the arrays are non-empty, and every id matches the
// grammar its list accepts.
func TestParsePacksSectionRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown key", `{"schema_version":"1","packs":{"disable_builtin":true}}`, "$.packs.disable_builtin: unknown field"},
		{"merge not bool", `{"schema_version":"1","packs":{"merge_builtin":"no"}}`, "$.packs.merge_builtin"},
		{"array not array", `{"schema_version":"1","packs":{"disable_packs":"verdict/go"}}`, "$.packs.disable_packs"},
		{"empty array", `{"schema_version":"1","packs":{"disable_rules":[]}}`, "$.packs.disable_rules"},
		{"bad pack id", `{"schema_version":"1","packs":{"disable_packs":["Verity/Go"]}}`, "$.packs.disable_packs"},
		{"bad lens id", `{"schema_version":"1","packs":{"disable_lenses":["go_lens"]}}`, "$.packs.disable_lenses"},
		{"bad rule id", `{"schema_version":"1","packs":{"disable_rules":["-leading"]}}`, "$.packs.disable_rules"},
		{"non-string id", `{"schema_version":"1","packs":{"disable_rules":[7]}}`, "$.packs.disable_rules"},
		{"duplicate id", `{"schema_version":"1","packs":{"disable_packs":["verdict/go","verdict/go"]}}`, "listed twice"},
		{"section not object", `{"schema_version":"1","packs":[]}`, "$.packs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requirePacksRefusal(t, tc.body, tc.want)
		})
	}
}

// requirePacksRefusal proves one document refuses bad-config with a message
// naming the offending key.
func requirePacksRefusal(t *testing.T, body, want string) {
	t.Helper()
	_, ref := Parse([]byte(body))
	if ref == nil {
		t.Fatalf("%s parsed, want a refusal", body)
	}
	if ref.Code != CodeBadConfig {
		t.Errorf("code = %q, want %q", ref.Code, CodeBadConfig)
	}
	if !strings.Contains(ref.Message, want) {
		t.Errorf("message = %q, want it to contain %q", ref.Message, want)
	}
}

// TestParseOrgPackPin proves the central-pack pin parses: a commit SHA or a
// semver tag ref, with an optional confined path.
//
//nolint:revive // the table rows read as one contract block
func TestParseOrgPackPin(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("a", 40)
	cases := []struct {
		name string
		body string
		want core.ConfigOrgPack
	}{
		{"sha ref no path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` + sha + `"}}}`, core.ConfigOrgPack{Repo: "central", Ref: sha}},
		{"tag ref with path", `{"schema_version":"1","packs":{"org_pack":{"repo":"https://example.invalid/acme/packs.git","ref":"v1.2.3","path":"packs/org"}}}`, core.ConfigOrgPack{Repo: "https://example.invalid/acme/packs.git", Ref: "v1.2.3", Path: "packs/org"}},
		{"bare tag", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"2.0.0"}}}`, core.ConfigOrgPack{Repo: "central", Ref: "2.0.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, ref := Parse([]byte(tc.body))
			if ref != nil {
				t.Fatalf("Parse: %s: %s", ref.Code, ref.Message)
			}
			if cfg.Packs == nil || cfg.Packs.OrgPack == nil {
				t.Fatal("org_pack missing")
			}
			if got := *cfg.Packs.OrgPack; got != tc.want {
				t.Errorf("org_pack = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseOrgPackRefusals pins one diagnostic per broken pin shape: a
// mutable branch ref, an empty or dash-led repo, a traversal or absolute
// path, and an unknown pin key.
func TestParseOrgPackRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"branch ref", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"main"}}}`, "$.packs.org_pack.ref"},
		{"missing ref", `{"schema_version":"1","packs":{"org_pack":{"repo":"central"}}}`, "ref"},
		{"empty repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo"},
		{"dash repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"-central","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo"},
		{"query repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"https://h/p?token=x","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo"},
		{"traversal path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"../escape"}}}`, "$.packs.org_pack.path"},
		{"nested traversal", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"a/../../escape"}}}`, "$.packs.org_pack.path"},
		{"absolute path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"/etc"}}}`, "$.packs.org_pack.path"},
		{"unknown pin key", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","branch":"main"}}}`, "$.packs.org_pack.branch: unknown field"},
		{"pin not object", `{"schema_version":"1","packs":{"org_pack":[]}}`, "$.packs.org_pack"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requirePacksRefusal(t, tc.body, tc.want)
		})
	}
}

// TestParsePacksSectionIsOptional proves an absent section leaves the merge
// default, so every repository written before this key existed reads the same.
func TestParsePacksSectionIsOptional(t *testing.T) {
	t.Parallel()
	cfg, ref := Parse([]byte(`{"schema_version":"1"}`))
	if ref != nil {
		t.Fatalf("Parse: %s: %s", ref.Code, ref.Message)
	}
	if cfg.Packs != nil {
		t.Errorf("packs = %+v, want absent", cfg.Packs)
	}
}
