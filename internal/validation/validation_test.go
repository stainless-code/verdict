package validation_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/validation"
)

const (
	headSHA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sigA      = "0123456789abcdef0123456789abcdef01234567"
	sigB      = "fedcba9876543210fedcba9876543210fedcba98"
	reasonOne = "Nil deref is reachable from the cited hunk."
)

// validationFixture is one well-formed validation.json: a confirmed and a
// downgraded result at the bundle head.
const validationFixture = `{"schema_version":"1","head_sha":"` + headSHA + `","results":[` +
	`{"sig":"` + sigA + `","verdict":"CONFIRMED","reason":"Nil deref is reachable from the cited hunk.","checks":["hedge","repro"]},` +
	`{"sig":"` + sigB + `","verdict":"DOWNGRADED","reason":"Real bug, local blast radius.","from_severity":"critical","to_severity":"important"}]}`

// findingsFixture is the review's candidate set the sigs resolve against.
func findingsFixture() []any {
	return []any{jq.NewObj("sig", sigA), jq.NewObj("sig", sigB)}
}

// TestParseRefusesBadValidation pins every malformed artifact: the reader
// names one cause per file and never half-applies a bad result.
func TestParseRefusesBadValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		code    string
		message string
	}{
		{
			name: "unknown verdict",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"` + sigA +
				`","verdict":"MAYBE","reason":"` + reasonOne + `"}]}`,
			code: "bad-validation",
		},
		{
			name: "short reason",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"` + sigA +
				`","verdict":"REJECTED","reason":"nope"}]}`,
			code: "bad-validation",
		},
		{
			name: "downgrade skips two ranks",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"` + sigA +
				`","verdict":"DOWNGRADED","reason":"` + reasonOne + `","from_severity":"critical","to_severity":"informational"}]}`,
			code: "bad-validation",
		},
		{
			name: "downgrade without a pair",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"` + sigA +
				`","verdict":"DOWNGRADED","reason":"` + reasonOne + `"}]}`,
			code: "bad-validation",
		},
		{
			name: "duplicate sig",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[` +
				`{"sig":"` + sigA + `","verdict":"CONFIRMED","reason":"` + reasonOne + `"},` +
				`{"sig":"` + sigA + `","verdict":"REJECTED","reason":"` + reasonOne + `"}]}`,
			code: "bad-validation",
		},
		{
			name: "unknown sig",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"9999999999999999999999999999999999999999",` +
				`"verdict":"CONFIRMED","reason":"` + reasonOne + `"}]}`,
			code: "bad-validation",
		},
		{
			name: "unknown key",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[],"verdicts":[]}`,
			code: "bad-validation",
		},
		{
			name: "stale head",
			body: `{"schema_version":"1","head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","results":[]}`,
			code: "validation-stale",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := validation.Parse([]byte(tc.body), headSHA, findingsFixture())
			if ref == nil || ref.Code != tc.code {
				t.Fatalf("Parse refusal = %v, want %s", ref, tc.code)
			}
		})
	}
}

// TestParseResolvesPrefixAndDropsUnknownChecks pins the two tolerant rules:
// an unambiguous sig prefix stores the full sig, and a check name outside the
// published five is dropped with a warning instead of refusing the file.
func TestParseResolvesPrefixAndDropsUnknownChecks(t *testing.T) {
	t.Parallel()
	body := `{"schema_version":"1","head_sha":"` + headSHA + `","results":[{"sig":"` + sigA[:8] +
		`","verdict":"CONFIRMED","reason":"` + reasonOne + `","checks":["hedge","vibes"]}]}`
	file, ref := validation.Parse([]byte(body), headSHA, findingsFixture())
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	if len(file.Results) != 1 || file.Results[0].Sig != sigA {
		t.Fatalf("results = %+v, want sig %s", file.Results, sigA)
	}
	if got := file.Results[0].Checks; len(got) != 1 || got[0] != "hedge" {
		t.Fatalf("checks = %v, want the known one kept and the unknown one dropped", got)
	}
}

// TestLoadHashesExactBytes pins criterion 7: the recorded hash is the SHA-256
// of the file's exact bytes, so a whitespace edit moves it.
func TestLoadHashesExactBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, want string }{
		{
			name: "fixture",
			body: `{"schema_version":"1","head_sha":"` + headSHA + `","results":[]}`,
			want: "c8298e87eb0eac59def75e660b61da7b76052cdd2221cf8ee3c1fe7f14a7a8b8",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "validation.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			file, hash, ref := validation.Load(dir, headSHA, nil)
			if ref != nil || file == nil {
				t.Fatalf("Load = %v, %v; want the file", file, ref)
			}
			if hash != tc.want {
				t.Fatalf("hash = %q, want %q", hash, tc.want)
			}
			// The hash covers the exact bytes: one trailing newline moves it.
			if err := os.WriteFile(filepath.Join(dir, "validation.json"), []byte(tc.body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, moved, _ := validation.Load(dir, headSHA, nil); moved == hash {
				t.Fatal("a trailing newline left the hash unchanged")
			}
		})
	}
}

// TestLoadAbsent pins the absent artifact: no file reads as no validation and
// no hash, never a refusal.
func TestLoadAbsent(t *testing.T) {
	t.Parallel()
	file, hash, ref := validation.Load(t.TempDir(), headSHA, nil)
	if file != nil || hash != "" || ref != nil {
		t.Fatalf("Load(absent) = %v, %q, %v; want absent", file, hash, ref)
	}
}

// TestPolicyDefaultsAndConfig pins the precedence the gate reads: the typed
// base configuration over the built-in floors, and mode advisory by default.
func TestPolicyDefaultsAndConfig(t *testing.T) {
	t.Parallel()
	defaults := validation.PolicyFromConfig(nil)
	if defaults.Mode != core.ValidationWarn || defaults.Floors.Critical != core.RungWalked ||
		defaults.Floors.Important != core.RungPointed || defaults.Floors.Informational != core.RungSaid {
		t.Fatalf("defaults = %+v, want warn walked/pointed/said", defaults)
	}
	cfg := &core.Config{RigorPolicy: &core.ConfigRigorPolicy{Mode: "gate", Critical: "ran"}}
	policy := validation.PolicyFromConfig(cfg)
	if policy.Mode != core.ValidationGate || policy.Floors.Critical != core.RungRan ||
		policy.Floors.Important != core.RungPointed || policy.Floors.Informational != core.RungSaid {
		t.Fatalf("policy = %+v, want gate ran/pointed/said", policy)
	}
}

// TestRequireRigorFlag pins the flag's precedence: it names tiers, implies
// gate mode for them, and refuses a tier or rung it does not know.
func TestRequireRigorFlag(t *testing.T) {
	t.Parallel()
	policy, ref := validation.PolicyFromConfig(nil).WithRequireRigor([]string{"critical=walked"})
	if ref != nil {
		t.Fatalf("WithRequireRigor: %v", ref)
	}
	if policy.Mode != core.ValidationGate || policy.Floors.Critical != core.RungWalked {
		t.Fatalf("policy = %+v, want gate with critical walked", policy)
	}
	for _, bad := range []string{"critical=guessed", "minor=said", "critical"} {
		if _, ref := validation.PolicyFromConfig(nil).WithRequireRigor([]string{bad}); ref == nil || ref.Code != "bad-rigor-policy" {
			t.Errorf("%q refusal = %v, want bad-rigor-policy", bad, ref)
		}
	}
}

// TestFloors pins the three floor outcomes: a violation, the critical
// downgrade to important, and the tier whose rung already clears the floor.
func TestFloors(t *testing.T) {
	t.Parallel()
	policy, ref := validation.PolicyFromConfig(nil).WithRequireRigor([]string{"critical=walked"})
	if ref != nil {
		t.Fatalf("policy: %v", ref)
	}
	entries := []any{
		jq.NewObj("sig", sigA, "severity", "critical", "status", "open", "rigor", "said"),
		jq.NewObj("sig", sigB, "severity", "critical", "status", "open", "rigor", "pointed"),
		jq.NewObj("sig", "c", "severity", "important", "status", "open", "rigor", "pointed"),
	}
	downgrades, violations := validation.Floors(entries, policy)
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want only the said critical", violations)
	}
	if violations[0].Sig != sigA || violations[0].Severity != "critical" ||
		violations[0].Rigor != "said" || violations[0].Floor != "walked" {
		t.Fatalf("violation = %+v", violations[0])
	}
	if len(downgrades) != 1 || downgrades[0].Sig != sigB {
		t.Fatalf("downgrades = %+v, want the pointed critical downgraded to important", downgrades)
	}
}
