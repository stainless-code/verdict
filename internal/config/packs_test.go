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
// naming the offending key, and echoes none of the hide fragments — a refusal
// is a published diagnostic, so a credential-bearing value must never reach it.
func requirePacksRefusal(t *testing.T, body, want string, hide ...string) {
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
	for _, fragment := range hide {
		if fragment != "" && strings.Contains(ref.Message, fragment) {
			t.Errorf("message = %q echoes %q", ref.Message, fragment)
		}
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
		{"rc prerelease tag", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3-rc.1"}}}`, core.ConfigOrgPack{Repo: "central", Ref: "v1.2.3-rc.1"}},
		{"stable path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"packs/stable"}}}`, core.ConfigOrgPack{Repo: "central", Ref: "v1.2.3", Path: "packs/stable"}},
		{"root dot path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"."}}}`, core.ConfigOrgPack{Repo: "central", Ref: "v1.2.3", Path: "."}},
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
// path, a colon-bearing path, and an unknown pin key. A repo carrying a query
// credential refuses without echoing the token.
func TestParseOrgPackRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
		hide string
	}{
		{"branch ref", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"main"}}}`, "$.packs.org_pack.ref", ""},
		{"missing ref", `{"schema_version":"1","packs":{"org_pack":{"repo":"central"}}}`, "ref", ""},
		{"empty repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", ""},
		{"dash repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"-central","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", ""},
		{"query repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"https://h/p?token=ghp_DUMMYTOKEN","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", "ghp_DUMMYTOKEN"},
		{"fragment repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"https://h/p#ghp_DUMMYTOKEN","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", "ghp_DUMMYTOKEN"},
		{"local query repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"central?token=ghp_DUMMYTOKEN","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", "ghp_DUMMYTOKEN"},
		{"dash local query repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"-central?token=ghp_DUMMYTOKEN","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", "ghp_DUMMYTOKEN"},
		{"local fragment repo", `{"schema_version":"1","packs":{"org_pack":{"repo":"central#ghp_DUMMYTOKEN","ref":"v1.2.3"}}}`, "$.packs.org_pack.repo", "ghp_DUMMYTOKEN"},
		{"empty prerelease identifier", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3-alpha..1"}}}`, "$.packs.org_pack.ref", ""},
		{"lock suffix tag", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3+build.lock"}}}`, "$.packs.org_pack.ref", ""},
		{"traversal path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"../escape"}}}`, "$.packs.org_pack.path", ""},
		{"nested traversal", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"a/../../escape"}}}`, "$.packs.org_pack.path", ""},
		{"absolute path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"/etc"}}}`, "$.packs.org_pack.path", ""},
		{"colon path", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","path":"packs/release:stable"}}}`, "$.packs.org_pack.path", ""},
		{"unknown pin key", `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.2.3","branch":"main"}}}`, "$.packs.org_pack.branch: unknown field", ""},
		{"pin not object", `{"schema_version":"1","packs":{"org_pack":[]}}`, "$.packs.org_pack", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requirePacksRefusal(t, tc.body, tc.want, tc.hide)
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

// TestOrgPackRepoGitAllowlist proves the pin's repo runs through the shared
// git allowlist: a malformed bracketed host, a credential-bearing URL, and
// every scheme the allowlist does not publish refuse bad-config, and no
// refusal echoes a secret.
func TestOrgPackRepoGitAllowlist(t *testing.T) {
	t.Parallel()
	refused := []struct {
		name string
		repo string
		hide string
	}{
		{"bracketed host text", "https://[not a host]:443/packs.git", "not a host"},
		{"bracketed host empty", "https://[]/packs.git", "[]"},
		{"bracketed ssh host", "ssh://git@[garbage]/packs.git", "garbage"},
		{"http scheme", "http://example.invalid/packs.git", ""},
		{"file scheme", "file:///etc/packs", ""},
		{"git scheme", "git://example.invalid/packs.git", ""},
		{"credentialed https", "https://oauth2:ghp_SECRET@example.invalid/packs.git", "ghp_SECRET"},
		{"credentialed ssh", "ssh://user:S3CRET@example.invalid/packs.git", "S3CRET"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertOrgRepoRefused(t, tc.repo, tc.hide)
		})
	}
}

// assertOrgRepoRefused proves one repo spelling refuses bad-config by the
// allowlist, without echoing the secret it may carry.
func assertOrgRepoRefused(t *testing.T, repo, hide string) {
	t.Helper()
	sha := strings.Repeat("a", 40)
	body := `{"schema_version":"1","packs":{"org_pack":{"repo":"` + repo + `","ref":"` + sha + `"}}}`
	_, ref := Parse([]byte(body))
	if ref == nil || ref.Code != CodeBadConfig {
		t.Fatalf("Parse(%q) = %v, want %s", repo, ref, CodeBadConfig)
	}
	if !strings.Contains(ref.Message, "allowlisted git remote") {
		t.Fatalf("message = %q, want the allowlist named", ref.Message)
	}
	if hide != "" && strings.Contains(ref.Message, hide) {
		t.Fatalf("message = %q echoes %q", ref.Message, hide)
	}
}

// TestOrgPackRepoGitAllowlistAccepts proves every published remote form and a
// local mirror path still parse with the pin's repo carried verbatim.
func TestOrgPackRepoGitAllowlistAccepts(t *testing.T) {
	t.Parallel()
	accepted := []string{
		"https://example.invalid/acme/packs.git",
		"ssh://git@example.invalid/acme/packs.git",
		"git@example.invalid:org/repo.git",
		"central",
		"./mirrors/packs",
	}
	for _, repo := range accepted {
		t.Run(repo, func(t *testing.T) {
			t.Parallel()
			assertOrgRepoAccepted(t, repo)
		})
	}
}

// assertOrgRepoAccepted proves one published remote form parses with its repo
// carried verbatim into the typed pin.
func assertOrgRepoAccepted(t *testing.T, repo string) {
	t.Helper()
	sha := strings.Repeat("a", 40)
	body := `{"schema_version":"1","packs":{"org_pack":{"repo":"` + repo + `","ref":"` + sha + `"}}}`
	cfg, ref := Parse([]byte(body))
	if ref != nil {
		t.Fatalf("Parse(%q) = %s: %s, want accepted", repo, ref.Code, ref.Message)
	}
	if cfg.Packs.OrgPack.Repo != repo {
		t.Fatalf("repo = %q, want the verbatim pin %q", cfg.Packs.OrgPack.Repo, repo)
	}
}
