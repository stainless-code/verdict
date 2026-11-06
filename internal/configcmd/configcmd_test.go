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
	"github.com/stainless-code/verdict/internal/core"
)

func fixedTime() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

// configRunner builds a minimal runner over scripted streams.
func configRunner(env map[string]string, jsonMode bool) (*cli.Runner, *bytes.Buffer, *bytes.Buffer) {
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
	r, _, diag := configRunner(nil, false)
	if _, code, done := parseFlags(r, []string{"--explain", "--preview"}); !done || code != cli.ExitUsage {
		t.Fatalf("both modes: done=%v code=%d, want usage", done, code)
	}
	if _, code, done := parseFlags(r, nil); !done || code != cli.ExitUsage {
		t.Fatalf("no mode: done=%v code=%d, want usage", done, code)
	}
	if _, code, done := parseFlags(r, []string{"--explain", "--file", "p"}); !done || code != cli.ExitUsage {
		t.Fatalf("explain with file: done=%v code=%d, want usage", done, code)
	}
	if _, code, done := parseFlags(r, []string{"--bogus"}); !done || code != cli.ExitUsage {
		t.Fatalf("unknown flag: done=%v code=%d, want usage", done, code)
	}
	if !strings.Contains(diag.String(), "config") {
		t.Fatalf("usage misses the verb: %q", diag.String())
	}
	flags, code, done := parseFlags(r, []string{"--preview", "--file", "p.json"})
	if done || code != 0 || !flags.Preview || flags.File != "p.json" {
		t.Fatalf("preview with file: flags=%+v code=%d done=%v", flags, code, done)
	}
}

// TestResolveBundleName pins the local mapping: the exact name wins,
// else the fetch-local local- form, else the typed no-bundle refusal.
func TestResolveBundleName(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".verdict", "state", "local-demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if name, ref := config.ResolveBundleName(".", "demo"); ref != nil || name != "local-demo" {
		t.Fatalf("local mapping: name=%q ref=%v", name, ref)
	}
	if err := os.MkdirAll(filepath.Join(".verdict", "state", "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	if name, ref := config.ResolveBundleName(".", "7"); ref != nil || name != "7" {
		t.Fatalf("exact mapping: name=%q ref=%v", name, ref)
	}
	if _, ref := config.ResolveBundleName(".", "99"); ref == nil || ref.Code != config.CodeNoBundle {
		t.Fatalf("missing: ref=%v, want config-no-bundle", ref)
	}
}

// TestRunExplainEnvelope pins the read path end to end: the stored
// bundle explains through the envelope with no live calls, and the
// human lines carry the trust bit and origins.
func TestRunExplainEnvelope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
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
	if serr := config.WriteRunState(".", "7", rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	r, out, _ := configRunner(map[string]string{}, true)
	if code := Run(context.Background(), r, []string{"--explain"}, configReq(nil)); code != cli.ExitOK {
		t.Fatalf("Run explain: code=%d", code)
	}
	if !strings.Contains(out.String(), `"mode": "explain"`) || !strings.Contains(out.String(), `"trusted": true`) {
		t.Fatalf("envelope misses the explain data:\n%s", out.String())
	}
	r, out, _ = configRunner(map[string]string{}, false)
	if code := Run(context.Background(), r, []string{"--explain"}, configReq(nil)); code != cli.ExitOK {
		t.Fatalf("human Run explain: code=%d", code)
	}
	if !strings.Contains(out.String(), "trusted=true") || !strings.Contains(out.String(), "budget_usd") {
		t.Fatalf("human lines miss trust/values:\n%s", out.String())
	}
}

// TestRunExplainMissingBundle pins the required-bundle refusal through
// the adapter: exit 1 with the typed code, never a usage error.
func TestRunExplainMissingBundle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	r, out, _ := configRunner(map[string]string{}, true)
	if code := Run(context.Background(), r, []string{"--explain"}, configReq(nil)); code != cli.ExitRefused {
		t.Fatalf("missing bundle: code=%d, want refusal", code)
	}
	if !strings.Contains(out.String(), config.CodeNoBundle) {
		t.Fatalf("envelope misses the code: %q", out.String())
	}
}
