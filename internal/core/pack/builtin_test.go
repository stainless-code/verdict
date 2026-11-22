package pack

import (
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// shippedLanguagePacks is the curated builtin set the binary ships, in
// directory order. It is the acceptance list for the feature: the stacks a
// review carries rules and a lens for without any repository configuration.
var shippedLanguagePacks = []string{
	"verdict/builtin", "verdict/dockerfile", "verdict/github-workflows",
	"verdict/go", "verdict/java", "verdict/python", "verdict/react",
	"verdict/rust", "verdict/security", "verdict/shell", "verdict/sql",
	"verdict/terraform", "verdict/ts-js",
}

// TestBuiltinOriginsShipsCoreAndLanguagePacks proves the embedded builtin
// layer is the core pack plus every curated pack, in one deterministic order.
func TestBuiltinOriginsShipsCoreAndLanguagePacks(t *testing.T) {
	t.Parallel()
	origins, ref := BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	names := make([]string, 0, len(origins))
	for _, origin := range origins {
		if origin.Layer != LayerBuiltin {
			t.Errorf("%s loaded at layer %q, want builtin", origin.Pack.Name, origin.Layer)
		}
		names = append(names, origin.Pack.Name)
	}
	if !slices.Equal(names, shippedLanguagePacks) {
		t.Fatalf("builtin packs = %v, want %v", names, shippedLanguagePacks)
	}
	if got, want := origins[0].Source, "builtin"; got != want {
		t.Errorf("core source = %q, want %q", got, want)
	}
	if got, want := origins[1].Source, "builtin:dockerfile"; got != want {
		t.Errorf("language source = %q, want %q", got, want)
	}
}

// TestShippedPacksDispatchRepresentativeFiles pins the acceptance matrix: one
// representative path per curated stack, the lens it must offer, and a rule id
// it must select. The expectations are literals, not a re-derivation from the
// manifests, so a manifest edit that stops matching its stack fails here.
func TestShippedPacksDispatchRepresentativeFiles(t *testing.T) {
	t.Parallel()
	set := shippedSet(t)
	cases := []struct {
		path  string
		lens  string
		rules []string
	}{
		{"cmd/verdict/main.go", "go", []string{"go-context-lifecycle", "go-shared-state"}},
		{"apps/web/src/App.tsx", "react", []string{"react-effect-cleanup", "react-dangerous-html"}},
		{"apps/web/src/api/client.ts", "ts-js", []string{"ts-js-floating-async"}},
		{"services/api/worker.py", "python", []string{"python-mutable-default"}},
		{"crates/core/src/lib.rs", "rust", []string{"rust-lock-across-await"}},
		{"src/main/java/acme/Service.java", "java", []string{"java-shared-state"}},
		{"db/migrations/001_init.sql", "sql", []string{"sql-destructive-ddl"}},
		{"infra/main.tf", "terraform", []string{"tf-wildcard-iam"}},
		{".github/workflows/ci.yml", "github-workflows", []string{"gha-script-injection"}},
		{"Dockerfile", "dockerfile", []string{"docker-root-runtime"}},
		{"scripts/deploy.sh", "shell", []string{"sh-unquoted-expansion"}},
		{"services/api/auth/session.go", "security", []string{"sec-broken-authz", "sec-ssrf"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			requireOffered(t, set, tc.path, tc.lens)
			requireRules(t, set, tc.path, tc.rules)
		})
	}
}

// TestShippedPacksStayOutOfUnrelatedDiffs proves the curated packs are quiet:
// a docs-only change is offered the three core lenses and no pack rule, and a
// Dockerfile change is offered only the Dockerfile and security lenses.
func TestShippedPacksStayOutOfUnrelatedDiffs(t *testing.T) {
	t.Parallel()
	set := shippedSet(t)
	if got, want := offeredIDs(set, "docs/guide.md"), "api-contract,perf,test"; got != want {
		t.Errorf("docs lenses = %q, want %q", got, want)
	}
	if got := set.RulesFor([]string{"docs/guide.md"}); len(got) != 0 {
		t.Errorf("docs rules = %v, want none", ruleIDs(got))
	}
	if got, want := offeredIDs(set, "Dockerfile"), "api-contract,dockerfile,perf,test"; got != want {
		t.Errorf("Dockerfile lenses = %q, want %q", got, want)
	}
	requireReactScoped(t, set)
}

// requireReactScoped proves the React lens is scoped to JSX/TSX: a backend
// TypeScript file is offered the general TypeScript lens and no React lens or
// React rule, while a component file is offered both.
func requireReactScoped(t *testing.T, set *Set) {
	t.Helper()
	if got, want := offeredIDs(set, "src/api/client.ts"), "api-contract,perf,security,test,ts-js"; got != want {
		t.Errorf("TypeScript lenses = %q, want %q", got, want)
	}
	if got, want := offeredIDs(set, "src/App.tsx"), "api-contract,perf,react,security,test,ts-js"; got != want {
		t.Errorf("TSX lenses = %q, want %q", got, want)
	}
	for _, id := range ruleIDs(set.RulesFor([]string{"src/api/client.ts"})) {
		if strings.HasPrefix(id, "react-") {
			t.Errorf("React rule %s reached a backend TypeScript path", id)
		}
	}
}

// TestSetNoteForCarriesOnlyOfferedLenses proves lenses.txt stays scoped to the
// changed files: a Go diff carries the Go and security bodies, never Rust's.
func TestSetNoteForCarriesOnlyOfferedLenses(t *testing.T) {
	t.Parallel()
	set := shippedSet(t)
	note := set.NoteFor([]string{"cmd/verdict/main.go"})
	for _, want := range []string{"# lens api-contract", "# lens go", "# lens security"} {
		if !strings.Contains(note, want) {
			t.Errorf("NoteFor lacks %q for a Go diff:\n%s", want, note)
		}
	}
	for _, unwanted := range []string{"# lens rust", "# lens python", "# lens react", "# lens terraform", "verdict/rust@"} {
		if strings.Contains(note, unwanted) {
			t.Errorf("NoteFor carried %q for a Go-only diff", unwanted)
		}
	}
	if !strings.Contains(set.Note(), "# lens rust") {
		t.Error("Note() no longer renders every loaded lens")
	}
}

// TestNoteForAnEmptyFileListKeepsOnlyAlwaysOfferedLenses proves the restricted
// note never falls back to the whole loaded set: an empty diff (or one whose
// every file was excluded) is offered the globless lenses alone, so no curated
// language body reaches a bundle that changed nothing it can dispatch to.
func TestNoteForAnEmptyFileListKeepsOnlyAlwaysOfferedLenses(t *testing.T) {
	t.Parallel()
	set := shippedSet(t)
	for _, files := range [][]string{nil, {}} {
		requireOnlyCoreLenses(t, set.NoteFor(files))
	}
}

// requireOnlyCoreLenses proves a note carries the three globless core lenses
// and no curated language body.
func requireOnlyCoreLenses(t *testing.T, note string) {
	t.Helper()
	for _, want := range []string{"# lens api-contract", "# lens perf", "# lens test"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks the always-offered %q: %s", want, note)
		}
	}
	for _, unwanted := range []string{"# lens go", "# lens rust", "# lens security", "verdict/go@"} {
		if strings.Contains(note, unwanted) {
			t.Errorf("note carried %q for an empty diff", unwanted)
		}
	}
}

// shippedSet merges the shipped builtin layer.
func shippedSet(t *testing.T) *Set {
	t.Helper()
	origins, ref := BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	set, ref := Merge(origins)
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	return set
}

// requireOffered proves one lens is offered for path and comes from the pack
// the stack's slug names.
func requireOffered(t *testing.T, set *Set, path, lens string) {
	t.Helper()
	offered := set.Offered([]string{path})
	for i := range offered {
		if offered[i].ID != lens {
			continue
		}
		if want := "verdict/" + lens; offered[i].Pack != want {
			t.Errorf("lens %s won by %q, want %q", lens, offered[i].Pack, want)
		}
		return
	}
	t.Errorf("no %s lens offered for %s (offered %s)", lens, path, offeredIDs(set, path))
}

// requireRules proves every expected rule is selected for path.
func requireRules(t *testing.T, set *Set, path string, want []string) {
	t.Helper()
	got := ruleIDs(set.RulesFor([]string{path}))
	for _, id := range want {
		if !slices.Contains(got, id) {
			t.Errorf("rule %s not selected for %s (selected %v)", id, path, got)
		}
	}
}

// offeredIDs renders the offered lens ids for one path.
func offeredIDs(set *Set, path string) string {
	offered := set.Offered([]string{path})
	ids := make([]string, len(offered))
	for i := range offered {
		ids[i] = offered[i].ID
	}
	return strings.Join(ids, ",")
}

// ruleIDs renders merged rule ids.
func ruleIDs(rules []Rule) []string {
	ids := make([]string, len(rules))
	for i := range rules {
		ids[i] = rules[i].ID
	}
	return ids
}
