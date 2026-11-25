package report

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestUnknownFlagListsAllReportFlags(t *testing.T) {
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--bogus"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d; want %d", code, cli.ExitUsage)
	}
	got := diag.String()
	for _, flag := range []string{
		"--status", "--kind", "--category", "--output", "--sarif",
		"--include-withheld", "--cost", "--risk", "--max-risk", "--check", "--compare", "--completeness", "--live", "--json", "--policy",
	} {
		if !strings.Contains(got, flag) {
			t.Errorf("unknown-flag message %q missing %s", got, flag)
		}
	}
}

// replayUsage runs one report argv that must be refused on usage and returns
// the diagnostic text, so each refusal test states only its flag and the words
// the operator has to read.
func replayUsage(t *testing.T, args ...string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag,
		Cmd: "report", Repo: "acme/app", Pr: "1", Effective: cli.Defaults(),
		Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}
	if code := Run(context.Background(), r, args, Request{Repo: "acme/app", Pr: "1"}); code != cli.ExitUsage {
		t.Fatalf("exit = %d; want %d", code, cli.ExitUsage)
	}
	return diag.String()
}

// TestPolicyReplayRefusesCompare pins that --compare cannot ride along with
// --policy: the replay prints its own two-sided comparison and would drop the
// second bundle silently, so an empty and a named baseline alike are usage
// errors rather than a replay that answers a different question.
func TestPolicyReplayRefusesCompare(t *testing.T) {
	for _, baseline := range []string{"", "other-bundle"} {
		t.Run(baseline, func(t *testing.T) {
			got := replayUsage(t, "--policy", "p.json", "--compare", baseline)
			if !strings.Contains(got, "--compare would be ignored") {
				t.Fatalf("usage text does not name the ignored flag: %q", got)
			}
		})
	}
}

// TestPolicyReplayRefusesGateFlags proves a gate flag cannot ride along with
// --policy: the replay exits 0 by contract, so accepting --check would swallow
// the very exit code a caller gates CI on.
func TestPolicyReplayRefusesGateFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--policy", "p.json", "--check", "critical"},
		{"--policy", "p.json", "--completeness"},
		{"--policy", "p.json", "--max-risk", "high"},
	} {
		t.Run(strings.Join(args[2:], ""), func(t *testing.T) {
			if got := replayUsage(t, args...); !strings.Contains(got, "swallowed") {
				t.Fatalf("usage text does not explain the refusal: %q", got)
			}
		})
	}
}

// TestPolicyReplayRefusesAnEmptyPath proves an explicitly empty --policy is a
// usage error, not a silent fall back to an ordinary report: a caller passing
// an unset variable must not believe it replayed.
func TestPolicyReplayRefusesAnEmptyPath(t *testing.T) {
	if got := replayUsage(t, "--policy", ""); !strings.Contains(got, "--policy needs a policy file path") {
		t.Fatalf("usage text does not name the missing path: %q", got)
	}
}

// TestPolicyReplayRefusesArtifactPaths proves the replay stays a read-only
// question: it refuses the artifact paths instead of silently ignoring them.
func TestPolicyReplayRefusesArtifactPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "output", args: []string{"--policy", "p.json", "--output", "out.json"}},
		{name: "sarif", args: []string{"--policy", "p.json", "--sarif", "out.sarif"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replayUsage(t, tc.args...); !strings.Contains(got, "--policy") {
				t.Fatalf("usage text does not name --policy: %q", got)
			}
		})
	}
}

// TestPolicyReplayRefusesWithoutABundle proves a replay over a missing bundle
// refuses through the same typed path as report, rather than inventing a
// decision from nothing.
func TestPolicyReplayRefusesWithoutABundle(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag,
		Cmd: "report", Repo: "acme/app", Pr: "1", Effective: cli.Defaults(),
		Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}
	code := Run(context.Background(), r, []string{"--policy", "p.json"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d; want %d (refusal)", code, cli.ExitRefused)
	}
}

// TestReplayWireKeepsGateOrder pins the published replay shape: only the
// dimension the candidate moves is listed, in the captured gate order, and the
// reason rides the side it belongs to.
func TestReplayWireKeepsGateOrder(t *testing.T) {
	replay := &reportcore.PolicyReplay{
		Captured: reportcore.Decision{GatePassed: true, Approved: true, Dimensions: []core.GateDimension{
			{ID: "spec", Status: "pass"}, {ID: "compliance", Status: "pass"}, {ID: "evidence", Status: "pass"},
		}},
		Candidate: reportcore.Decision{GatePassed: false, Reason: "open critical finding", Dimensions: []core.GateDimension{
			{ID: "spec", Status: "pass"}, {ID: "compliance", Status: "fail"}, {ID: "evidence", Status: "pass"},
		}},
	}
	got := replayWire("candidate.json", replay)
	want := &core.ReportReplay{
		PolicyPath: "candidate.json",
		Captured:   core.ReportReplaySide{GatePassed: true, Approved: true},
		Candidate:  core.ReportReplaySide{GatePassed: false, Reason: "open critical finding"},
		Changed:    []core.ReportReplayMove{{ID: "compliance", Before: "pass", After: "fail"}},
	}
	if got.PolicyPath != want.PolicyPath || got.Captured != want.Captured || got.Candidate != want.Candidate {
		t.Fatalf("replay wire = %#v; want %#v", got, want)
	}
	if !slices.Equal(got.Changed, want.Changed) {
		t.Fatalf("replay wire changed = %#v; want %#v", got.Changed, want.Changed)
	}
}

// TestReplayWireSerializesAnEmptyChangeList pins the unchanged replay: the
// schema requires `changed` to be an array, so an identical candidate must
// serialize `[]` rather than a nil list the encoder would render as null.
func TestReplayWireSerializesAnEmptyChangeList(t *testing.T) {
	dims := []core.GateDimension{{ID: "spec", Status: "pass"}, {ID: "compliance", Status: "pass"}}
	got := replayWire("candidate.json", &reportcore.PolicyReplay{
		Captured:  reportcore.Decision{GatePassed: true, Approved: true, Dimensions: dims},
		Candidate: reportcore.Decision{GatePassed: true, Approved: true, Dimensions: dims},
	})
	if got.Changed == nil {
		t.Fatal("changed is nil; an unchanged replay must serialize [] so the schema's array requirement holds")
	}
	if len(got.Changed) != 0 {
		t.Fatalf("changed = %#v; want empty", got.Changed)
	}
}

// TestRentryKeepsAnEmptyArgument proves the re-enter line a refusal prints
// survives an empty flag value. Dropping it would close the gap the operator
// never typed: `--status` would stand bare, and with `--json` already appended
// the value that follows would move onto the wrong token.
func TestRentryKeepsAnEmptyArgument(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Run("human", func(t *testing.T) {
		assertRentry(t, false, "re-enter: verdict report acme/app 1 --status ''\n")
	})
	t.Run("json", func(t *testing.T) {
		assertRentry(t, true, "re-enter: verdict report acme/app 1 --status '' --json\n")
	})
}

// assertRentry runs one report refusal with an empty --status value and pins
// the re-enter line it prints.
func assertRentry(t *testing.T, jsonMode bool, want string) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, JSONMode: jsonMode,
		Cmd: "report", Repo: "acme/app", Pr: "1", Effective: cli.Defaults(),
		Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}
	code := Run(context.Background(), r, []string{"--status", ""}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d; want %d (a missing bundle refuses)", code, cli.ExitRefused)
	}
	if got := diag.String(); !strings.Contains(got, want) {
		t.Errorf("diag = %q; want it to carry %q", got, want)
	}
}

const testRiskHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func setupRiskCLIBundle(t *testing.T, postJSON string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/local-risk-cli"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"source.json":   `{"head_sha":"` + testRiskHeadSHA + `","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		"findings.json": "[]",
		// Every gate mode carries the evidence dimension, so the fixture ships
		// the receipt that backs the head it grades.
		"record.json": `{"schema_version":"1","status":"local","head":"` + testRiskHeadSHA + `",` +
			`"evidence":{"head":"` + testRiskHeadSHA + `","diff_sha256":"` + strings.Repeat("0", 64) + `",` +
			`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
			`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if postJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(postJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportMaxRiskOK(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Isolated helper refactor with tests.","assessed_head":"`+testRiskHeadSHA+`"}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "low"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestReportMaxRiskExceeded(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"high","rationale":"Touches core state machine.","assessed_head":"`+testRiskHeadSHA+`"}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "medium"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestReportMaxRiskUnknown(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "critical"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), `"risk-unknown"`) {
		t.Errorf("json output missing risk-unknown: %s", out.String())
	}
}

func TestReportMaxRiskInvalid(t *testing.T) {
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--max-risk", "bogus"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want 2; diag=%s", code, diag.String())
	}
}

func TestReportMaxRiskMissingStore(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--max-risk", "low"}, Request{Repo: "acme/app", Pr: "missing-bundle"})
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want 3; out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestJSONValidationUsesStoredReceiptWithoutTableWarning(t *testing.T) {
	setupRiskCLIBundle(t, `{"validation_mode":"gate","validation_hash":"9f2b","validation_counts":{"confirmed":1}}`)
	if err := os.WriteFile(".verdict/state/local-risk-cli/validation.json", []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &diag, Diag: &diag, JSONMode: true, Effective: cli.Defaults()}
	code := Run(t.Context(), r, []string{"--validation", "--live", "skip"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != 0 || !strings.Contains(out.String(), `"hash": "9f2b"`) {
		t.Fatalf("exit=%d, out=%s, diag=%s; want recorded validation hash 9f2b", code, &out, &diag)
	}
	if strings.Contains(out.String(), "the table lists no per-finding results") {
		t.Errorf("JSON contains a table warning: %s", &out)
	}
}

// setupCompareCLIBundles writes a reported local bundle and a recorded
// baseline beside it, enough for `report --compare`.
func setupCompareCLIBundles(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for name, body := range map[string]string{
		".verdict/state/local-head/source.json":    `{"repo":"acme/app","head_sha":"` + testRiskHeadSHA + `"}`,
		".verdict/state/local-head/findings.json":  `[{"kind":"inline","path":"a.go","side":"RIGHT","line":3,"body":"current bug","severity":"important","sig":"` + strings.Repeat("a", 40) + `","status":"open"}]`,
		".verdict/state/local-prior/findings.json": `[{"kind":"inline","path":"b.go","side":"RIGHT","line":4,"body":"prior bug","severity":"important","sig":"` + strings.Repeat("b", 40) + `","status":"open"}]`,
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runCompareCLI drives one report invocation and returns its exit code and
// both streams.
func runCompareCLI(t *testing.T, jsonMode bool, args ...string) (int, string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, JSONMode: jsonMode,
		Cmd: "report", Repo: "acme/app", Pr: "local-head", Effective: cli.Defaults(),
		Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}
	code := Run(context.Background(), r, args, Request{Repo: "acme/app", Pr: "local-head"})
	return code, out.String(), diag.String()
}

// TestReportCompareHumanAndJSON proves the adapter parses --compare and both
// surfaces carry the four buckets.
//
//nolint:revive // both surfaces read as one block
func TestReportCompareHumanAndJSON(t *testing.T) {
	setupCompareCLIBundles(t)
	code, out, diag := runCompareCLI(t, false, "--compare", "local-prior")
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; diag=%s", code, diag)
	}
	for _, want := range []string{
		"compare: .verdict/state/local-prior @ unrecorded — new=1 persisting=0 resolved=0 not-reviewed=1",
		"new (1)",
		"not reviewed (1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
	code, out, diag = runCompareCLI(t, true, "--compare", "local-prior")
	if code != cli.ExitOK {
		t.Fatalf("json exit = %d, want 0; diag=%s", code, diag)
	}
	for _, want := range []string{`"compare"`, `"not_reviewed": 1`, `"reason": "partial-review"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

// TestReportCompareRefusesMissingBaseline proves a bad baseline is a typed
// refusal (exit 1) with the hint, not a usage error.
func TestReportCompareRefusesMissingBaseline(t *testing.T) {
	setupCompareCLIBundles(t)
	code, out, diag := runCompareCLI(t, true, "--compare", "local-absent")
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out, diag)
	}
	if !strings.Contains(out, `"missing-compare-bundle"`) {
		t.Errorf("json refusal missing the typed code: %s", out)
	}
	code, _, diag = runCompareCLI(t, false, "--compare", "local-absent")
	if code != cli.ExitRefused || !strings.Contains(diag, "try: pass an existing bundle directory") {
		t.Errorf("human refusal = exit %d, diag %q; want exit 1 and the hint", code, diag)
	}
}

// TestReportCompareEmptyValueRefuses proves an explicitly empty operand
// refuses typed instead of silently disabling the comparison.
func TestReportCompareEmptyValueRefuses(t *testing.T) {
	setupCompareCLIBundles(t)
	code, out, diag := runCompareCLI(t, true, "--compare", "")
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out, diag)
	}
	if !strings.Contains(out, `"bad-compare"`) {
		t.Errorf("json refusal missing bad-compare: %s", out)
	}
}

// TestReportCompareNeedsAValue proves the flag's operand is required (exit 2,
// no envelope), like every other report value flag.
func TestReportCompareNeedsAValue(t *testing.T) {
	code, out, diag := runCompareCLI(t, false, "--compare")
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want 2; out=%s diag=%s", code, out, diag)
	}
	if !strings.Contains(diag, "--compare") || !strings.Contains(diag, "<bundle-dir|pr|slug>") {
		t.Errorf("usage error missing the operand name: %q", diag)
	}
}

// TestCheckLinesReachStderrInert runs `report --check` over a bundle whose
// record carries a violated spec item spelled with an OSC 52 clipboard write,
// a C1 CSI, and a BEL. The gate prints that item on stderr through the
// production runner, which must deliver it as plain text.
func TestCheckLinesReachStderrInert(t *testing.T) {
	setupRiskCLIBundle(t, "")
	item := `evil\u001b]52;c;cGF5bG9hZA==\u0007item\u009b2J`
	record := `{"schema_version":"1","status":"local","head":"` + testRiskHeadSHA + `",` +
		`"evidence":{"head":"` + testRiskHeadSHA + `","diff_sha256":"` + strings.Repeat("0", 64) + `",` +
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
		`"spec":{"total":1,"covered":1,"min":80,"met":true},"insufficient":false},` +
		`"spec_checks":[{"item":"` + item + `","status":"violated",` +
		`"evidence":{"path":"notes.txt","quote":"the quoted line","line":2}}]}`
	if err := os.WriteFile(".verdict/state/local-risk-cli/record.json", []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("report", "acme/app", "local-risk-cli", &out, &diag, func(string) string { return "" })
	r.Effective = cli.Defaults()
	r.RouteHuman()
	code := Run(context.Background(), r, []string{"--check", "critical"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	got := diag.String()
	if code != cli.ExitRefused || !strings.Contains(got, "evil ]52;c;cGF5bG9hZA== item 2J") {
		t.Fatalf("exit %d, stderr %q; want the gate to fail on the folded spec item", code, got)
	}
	if strings.ContainsAny(got, "\x1b\x07\u009b") {
		t.Fatalf("stderr carries a control byte: %q", got)
	}
}
