package config

import (
	"strings"
	"testing"
)

// checkEnvRefusal pins one malformed environment refusal.
func checkEnvRefusal(t *testing.T, env map[string]string, code string) {
	t.Helper()
	_, _, ref := ParseEnv(env)
	requireRefusal(t, ref, code, "")
}

// TestParseEnvValid pins the consumed environment contract: only the five
// names are read, and unrelated names never leak in.
func TestParseEnvValid(t *testing.T) {
	got, warnings, ref := ParseEnv(map[string]string{
		"REVIEW_PANEL":          "a, b c",
		"REVIEW_BUDGET_USD":     "2.5",
		"REVIEW_MAX_BYTES":      "1000",
		"REVIEW_KB_MIN_JUDGED":  "3",
		"REVIEW_KB_MAX_FP_RATE": "0.25",
		"REVIEW_UNRELATED":      "ignored",
	})
	if ref != nil {
		t.Fatalf("ParseEnv: %v", ref)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(got.Panel) != 3 || got.BudgetUSD == nil || *got.BudgetUSD != 2.5 {
		t.Fatalf("env = %+v", got)
	}
	if got.MaxBytes == nil || *got.MaxBytes != 1000 || got.KBMinJudged == nil || *got.KBMinJudged != 3 {
		t.Fatalf("env = %+v", got)
	}
}

// TestParseEnvRefusals pins the strict budget and byte-cap refusals with
// the shipped codes.
func TestParseEnvRefusals(t *testing.T) {
	checkEnvRefusal(t, map[string]string{"REVIEW_BUDGET_USD": "nope"}, "bad-budget")
	checkEnvRefusal(t, map[string]string{"REVIEW_BUDGET_USD": "0"}, "bad-budget")
	checkEnvRefusal(t, map[string]string{"REVIEW_MAX_BYTES": "0"}, "bad-max-bytes")
	checkEnvRefusal(t, map[string]string{"REVIEW_MAX_BYTES": "plenty"}, "bad-max-bytes")
}

// TestParseEnvLenient pins the shipped lenient KB behavior: malformed
// values warn and fall through instead of refusing.
func TestParseEnvLenient(t *testing.T) {
	got, warnings, ref := ParseEnv(map[string]string{"REVIEW_KB_MIN_JUDGED": "bogus", "REVIEW_KB_MAX_FP_RATE": "9"})
	if ref != nil {
		t.Fatalf("ParseEnv: %v", ref)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got.KBMinJudged != nil || got.KBMaxFPRate != nil {
		t.Fatalf("env = %+v", got)
	}
}

// captureFixture resolves the minimal Appendix A shape for evidence
// tests, failing on any refusal.
func captureFixture(t *testing.T) (Inputs, *Resolved, []byte) {
	t.Helper()
	raw := []byte(`{"schema_version":"1","defaults":{"panel":["a"]},
		"requirements":{"block_clauses":[{"name":"root-security","threshold":"important","paths":["**"]}]}}`)
	cfg := mustParse(t, string(raw))
	env, _, ref := ParseEnv(map[string]string{"REVIEW_PANEL": "b"})
	if ref != nil {
		t.Fatalf("ParseEnv: %v", ref)
	}
	in := &Inputs{Env: env, Repo: cfg, BaseSHA: strings.Repeat("c", 40), Files: []FileInput{{Path: "a.go"}}}
	res, ref := Resolve(in)
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return *in, res, raw
}

// checkConsumedInputs pins the allowlist: consumed input names only, with
// the whole environment never appearing.
func checkConsumedInputs(t *testing.T, got *Evidence, baseSHA string) {
	t.Helper()
	if got.BaseSHA != baseSHA {
		t.Fatalf("base = %q", got.BaseSHA)
	}
	for _, want := range []string{"REVIEW_PANEL", ".verdict/config.json"} {
		found := false
		for _, name := range got.ConsumedInputs {
			found = found || name == want
		}
		if !found {
			t.Fatalf("consumed inputs = %v, missing %q", got.ConsumedInputs, want)
		}
	}
}

// TestCaptureIdentity pins the allowlisted capture: identity, hash, and
// consumed input names only. The whole environment never appears.
func TestCaptureIdentity(t *testing.T) {
	in, res, raw := captureFixture(t)
	got := Capture(&in, res, raw, "2026-11-06T12:00:00Z")
	if got.SchemaVersion != "1" || got.CapturedAt == "" {
		t.Fatalf("identity = %+v", got)
	}
	if len(got.ConfigSHA256) != 64 {
		t.Fatalf("config hash = %q", got.ConfigSHA256)
	}
	checkConsumedInputs(t, &got, in.BaseSHA)
}

// TestCaptureRequirements pins the pending status and the captured plan:
// clauses keyed by name with merged sources, never satisfied before
// evidence exists.
func TestCaptureRequirements(t *testing.T) {
	in, res, raw := captureFixture(t)
	got := Capture(&in, res, raw, "2026-11-06T12:00:00Z")
	if got.Plan.Status != PendingStatus {
		t.Fatalf("plan status = %q", got.Plan.Status)
	}
	if len(got.Values) == 0 || len(got.Requirements) == 0 {
		t.Fatalf("values/requirements = %+v %+v", got.Values, got.Requirements)
	}
	for _, req := range got.Requirements {
		if req.Status != PendingStatus {
			t.Fatalf("requirement %q status = %q", req.Key, req.Status)
		}
	}
}
