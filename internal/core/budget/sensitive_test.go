package budget

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
)

// TestSensitiveGlobsMatchTheirTree pins one path per built-in glob plus the
// near misses that must not match, so the default list cannot rot into globs
// that only look like they select something.
func TestSensitiveGlobsMatchTheirTree(t *testing.T) {
	cases := []struct {
		glob string
		hit  string
		miss []string
	}{
		// A leading `**` lets the glob match at any depth, while the trailing
		// `**` needs a path inside the directory, never the name itself.
		{"**/auth/**", "internal/auth/session.go", []string{"docs/authentic.md", "src/authentication.go", "auth"}},
		{"**/security/**", "security/policy.md", []string{"docs/security.md", "internal/securely/thing.go"}},
		{"**/migrations/**", "db/migrations/0001_init.sql", []string{"db/migrations.md", "migrations"}},
		{"**/billing/**", "app/billing/invoice.go", []string{"src/billing_test.go", "billing.go"}},
		// The CI-workflow glob is repository-root anchored, the way git reads
		// its own .github directory; a sibling directory is not a workflow tree.
		{".github/workflows/**", ".github/workflows/ci.yml", []string{".github/workflows-x/y.yml", "docs/.github/workflows/ci.yml"}},
	}
	for _, tc := range cases {
		t.Run(tc.glob, func(t *testing.T) { assertGlobCase(t, tc.glob, tc.hit, tc.miss) })
	}
}

// assertGlobCase grades one glob's own path and every near miss beside it.
func assertGlobCase(t *testing.T, glob, hit string, misses []string) {
	t.Helper()
	if !config.Match(glob, hit) {
		t.Errorf("built-in glob %q does not match its own path %q", glob, hit)
	}
	for _, miss := range misses {
		if config.Match(glob, miss) {
			t.Errorf("built-in glob %q matches the near miss %q", glob, miss)
		}
	}
}

// TestSensitiveGlobsSelectDeep proves every built-in glob reaches the
// classifier as a deep reason, so a glob the matcher accepts but the decision
// ignores fails here.
func TestSensitiveGlobsSelectDeep(t *testing.T) {
	cases := []struct {
		path string
		glob string
	}{
		{"internal/auth/session.go", "**/auth/**"},
		{"security/policy.md", "**/security/**"},
		{"db/migrations/0001_init.sql", "**/migrations/**"},
		{"app/billing/invoice.go", "**/billing/**"},
		{".github/workflows/ci.yml", ".github/workflows/**"},
	}
	for _, tc := range cases {
		t.Run(tc.glob, func(t *testing.T) { assertGlobSelectsDeep(t, tc.path, tc.glob) })
	}
}

// assertGlobSelectsDeep grades one path through the whole classifier.
func assertGlobSelectsDeep(t *testing.T, path, glob string) {
	t.Helper()
	in := Inputs{
		Files: []File{{Path: path, Role: "source"}}, FileCount: 1, DiffBytes: 40,
		MaxFiles: defaultFiles, MaxBytes: defaultBytes, Sensitive: BuiltinSensitive(),
	}
	decision := Classify(&in)
	if decision.Tier != TierDeep {
		t.Fatalf("tier = %q; want %q", decision.Tier, TierDeep)
	}
	if want := []string{"sensitive-path: " + glob}; !slices.Equal(decision.Reasons, want) {
		t.Fatalf("reasons = %q; want %q", decision.Reasons, want)
	}
}

// TestBuiltinSensitiveIsOwnedByTheCaller proves the returned slice is a copy:
// extending it with a repository glob must not mutate the built-in list, which
// is shared by every classification in the process.
func TestBuiltinSensitiveIsOwnedByTheCaller(t *testing.T) {
	first := BuiltinSensitive()
	first[0] = "**/hijacked/**"
	extended := append(BuiltinSensitive(), "**/payments/**")
	extended[len(extended)-1] = "**/hijacked/**"
	if got := BuiltinSensitive()[0]; got != "**/auth/**" {
		t.Fatalf("BuiltinSensitive()[0] = %q after a caller edited its copy", got)
	}
	if got := len(BuiltinSensitive()); got != 5 {
		t.Fatalf("built-in glob count = %d; want 5", got)
	}
}
