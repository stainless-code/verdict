package configcmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

func fixedTime() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

// configRunner builds a minimal runner over scripted streams.
func configRunner(jsonMode bool) (*cli.Runner, *bytes.Buffer, *bytes.Buffer) {
	var out, diag bytes.Buffer
	return &cli.Runner{
		Out: &out, Diag: &diag, Human: &out,
		JSONMode: jsonMode, Cmd: "config",
		Repo: "acme/app", Pr: "7",
		Now: fixedTime,
	}, &out, &diag
}

// configReq builds the adapter request over the given environment.
func configReq(env map[string]string) Request {
	root := ""
	return Request{
		Runtime: &core.Runtime{Env: env, Now: fixedTime},
		Repo:    "acme/app", Pr: "7", Root: &root,
	}
}

// TestParseFlagsPinsModeExclusivity proves the mode gate: exactly one
// of --explain or --preview, with --file only on preview.
func TestParseFlagsPinsModeExclusivity(t *testing.T) {
	r, _, diag := configRunner(false)
	rejected := []struct {
		name string
		args []string
	}{
		{"both modes", []string{"--explain", "--preview"}},
		{"no mode", nil},
		{"explain with file", []string{"--explain", "--file", "p"}},
		{"unknown flag", []string{"--bogus"}},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			assertUsageRejected(t, r, tc.args)
		})
	}
	if !strings.Contains(diag.String(), "config") {
		t.Fatalf("usage misses the verb: %q", diag.String())
	}
	flags, code, done := parseFlags(r, []string{"--preview", "--file", "p.json"})
	if done || code != 0 || !flags.Preview || flags.File != "p.json" {
		t.Fatalf("preview with file: flags=%+v code=%d done=%v", flags, code, done)
	}
}

// assertUsageRejected fails unless the flags are refused as a usage
// error. The partially read flags are reported, not asserted: a refusal
// keeps what it already parsed for the caller's diagnostics.
func assertUsageRejected(t *testing.T, r *cli.Runner, args []string) {
	t.Helper()
	flags, code, done := parseFlags(r, args)
	if !done || code != cli.ExitUsage {
		t.Fatalf("args %v: done=%v code=%d flags=%+v, want usage", args, done, code, flags)
	}
}

// TestResolveBundleName pins the local mapping: the exact name wins,
// else the fetch-local local- form, else the typed no-bundle refusal.
func TestResolveBundleName(t *testing.T) {
	t.Chdir(t.TempDir())
	mkBundle(t, "local-demo")
	if name, ref := config.ResolveBundleName(".", "demo"); ref != nil || name != "local-demo" {
		t.Fatalf("local mapping: name=%q ref=%v", name, ref)
	}
	mkBundle(t, "7")
	if name, ref := config.ResolveBundleName(".", "7"); ref != nil || name != "7" {
		t.Fatalf("exact mapping: name=%q ref=%v", name, ref)
	}
	if _, ref := config.ResolveBundleName(".", "99"); ref == nil || ref.Code != config.CodeNoBundle {
		t.Fatalf("missing: ref=%v, want config-no-bundle", ref)
	}
}

// mkBundle creates one fetch-local bundle directory under .verdict/state.
func mkBundle(t *testing.T, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(".verdict", "state", name), 0o755); err != nil {
		t.Fatalf("mkdir bundle %s: %v", name, err)
	}
}

// TestRunExplainEnvelope pins the read path end to end: the stored
// bundle explains through the envelope with no live calls, and the
// human lines carry the trust bit and origins.
func TestRunExplainEnvelope(t *testing.T) {
	t.Chdir(t.TempDir())
	writeExplainFixture(t)
	code, stdout := runExplain(t, true)
	if code != cli.ExitOK {
		t.Fatalf("Run explain: code=%d", code)
	}
	assertContainsAll(t, stdout, `"mode": "explain"`, `"trusted": true`)
	code, stdout = runExplain(t, false)
	if code != cli.ExitOK {
		t.Fatalf("human Run explain: code=%d", code)
	}
	assertContainsAll(t, stdout, "trusted=true", "budget_usd")
}

// writeExplainFixture stores a trusted run bundle whose budget came from
// the environment, then proves controls accepts it before any rendering
// runs: the envelope assertion below is about the render, not the gate.
func writeExplainFixture(t *testing.T) {
	t.Helper()
	guard := 9.5
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
		Evidence: config.Evidence{
			SchemaVersion: "1",
			BaseSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Plan:          config.Plan{Status: config.PendingStatus},
			Values: []config.EvidenceValue{
				{Key: "budget_usd", Value: 9.5, Origin: config.FieldOrigin{Layer: config.LayerEnv, Source: "REVIEW_BUDGET_USD"}},
			},
		},
		Resolved: &config.Resolved{
			Plan:     config.Plan{Status: config.PendingStatus},
			Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{BudgetUSD: &guard}},
		},
	}
	if serr := config.WriteRunState(filepath.Join(".", ".verdict", "state", "7"), rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: filepath.Join(".", ".verdict", "state", "7"), Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// runExplain drives the verb once and returns its exit code with stdout.
func runExplain(t *testing.T, jsonMode bool) (int, string) {
	t.Helper()
	r, out, _ := configRunner(jsonMode)
	code := Run(context.Background(), r, []string{"--explain"}, configReq(nil))
	return code, out.String()
}

// assertContainsAll fails naming the first fragment stdout misses.
func assertContainsAll(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, frag := range want {
		if !strings.Contains(got, frag) {
			t.Fatalf("output misses %q:\n%s", frag, got)
		}
	}
}

// TestRunExplainMissingBundle pins the required-bundle refusal through
// the adapter: exit 1 with the typed code, never a usage error.
func TestRunExplainMissingBundle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	r, out, _ := configRunner(true)
	if code := Run(context.Background(), r, []string{"--explain"}, configReq(nil)); code != cli.ExitRefused {
		t.Fatalf("missing bundle: code=%d, want refusal", code)
	}
	if !strings.Contains(out.String(), config.CodeNoBundle) {
		t.Fatalf("envelope misses the code: %q", out.String())
	}
}
