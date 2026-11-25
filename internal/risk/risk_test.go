package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	riskcore "github.com/stainless-code/verdict/internal/core/risk"
)

const (
	riskHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	riskBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	riskNow  = "2026-11-14T12:00:00Z"
)

// riskFixture seeds one open bundle and one merged bundle under the state home.
func riskFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeBundle(t, "42", "OPEN")
	writeBundle(t, "51", "MERGED")
}

// writeBundle lands one host-PR bundle with a single open critical finding.
func writeBundle(t *testing.T, name, state string) {
	t.Helper()
	dir := filepath.Join(".verdict", "state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"PR.json": `{"state":"` + state + `","headRefOid":"` + riskHead + `","baseRefOid":"` + riskBase + `"}`,
		"findings.json": `[{"sig":"c1","kind":"inline","path":"notes.txt","line":2,"side":"RIGHT",` +
			`"anchored":true,"body":"The changed line drops the saved value.","status":"open",` +
			`"severity":"critical","category":"security"}]`,
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type streams struct {
	out, human, diag *bytes.Buffer
}

func riskRunner(jsonMode bool) (*cli.Runner, *streams) {
	s := &streams{out: &bytes.Buffer{}, human: &bytes.Buffer{}, diag: &bytes.Buffer{}}
	return &cli.Runner{
		Out: s.out, Human: s.human, Diag: s.diag,
		Now:      func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) },
		JSONMode: jsonMode,
	}, s
}

// TestParseFlagRefusesOutOfBounds pins the --limit usage refusal.
func TestParseFlagRefusesOutOfBounds(t *testing.T) {
	for _, value := range []string{"0", "201", "x", "-1"} {
		r, s := riskRunner(false)
		if _, code, done := parseFlags(r, "acme/app", []string{"--limit", value}); !done || code != cli.ExitUsage {
			t.Errorf("--limit %s = code %d, done %t; want the usage refusal", value, code, done)
		}
		if want := "--limit wants an integer from 1 to 200, not \"" + value + "\"\n"; s.diag.String() != want {
			t.Errorf("--limit %s stderr = %q, want %q", value, s.diag.String(), want)
		}
	}
}

// TestParseFlagAcceptsBounds proves both ends of the published bound parse.
func TestParseFlagAcceptsBounds(t *testing.T) {
	for _, value := range []int{1, 200} {
		r, _ := riskRunner(false)
		opts, code, done := parseFlags(r, "acme/app", []string{"--limit", strconv.Itoa(value)})
		if done || code != 0 || opts.limit != value {
			t.Errorf("--limit %d = limit %d, code %d, done %t; want accepted", value, opts.limit, code, done)
		}
	}
}

// TestParseFlagStatusAcceptsTheDomain proves both published filters parse and
// that the bare form reads the default open filter at the default limit.
func TestParseFlagStatusAcceptsTheDomain(t *testing.T) {
	for _, want := range []string{riskcore.QueueStatusOpen, riskcore.QueueStatusAll} {
		r, _ := riskRunner(false)
		opts, code, done := parseFlags(r, "acme/app", []string{"--status", want})
		if done || code != 0 || opts.status != want {
			t.Errorf("--status %s = %q, code %d, done %t; want accepted", want, opts.status, code, done)
		}
	}
	r, _ := riskRunner(false)
	if opts, _, done := parseFlags(r, "acme/app", nil); done || opts.status != riskcore.QueueStatusOpen ||
		opts.limit != riskcore.DefaultQueueLimit {
		t.Errorf("bare flags = %+v, want open at the default limit", opts)
	}
}

// TestParseFlagRefusesBadStatus pins the two --status refusals: a value outside
// the domain and a flag with no value.
func TestParseFlagRefusesBadStatus(t *testing.T) {
	for _, args := range [][]string{{"--status", "weird"}, {"--status"}} {
		r, _ := riskRunner(false)
		if _, code, done := parseFlags(r, "acme/app", args); !done || code != cli.ExitUsage {
			t.Errorf("%v = code %d, done %t; want usage", args, code, done)
		}
	}
}

// TestRunRefusesUnknownFlagAndSecondPositional pins the two argv shapes the
// generic dispatch would otherwise swallow.
func TestRunRefusesUnknownFlagAndSecondPositional(t *testing.T) {
	cases := []struct {
		name     string
		contains string
		args     []string
	}{
		{name: "unknown flag", args: []string{"--bogus"}, contains: "unknown flag '--bogus' for risk"},
		{
			name: "second positional", args: []string{"42"},
			contains: "gating one bundle is verdict gate acme/app 42 --max-risk <band>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageRefusal(t, tc.args); !strings.Contains(got, tc.contains) {
				t.Errorf("stderr = %q, want %q", got, tc.contains)
			}
		})
	}
}

// usageRefusal runs the verb over the fixture and returns stderr after
// proving the run ended in a usage error.
func usageRefusal(t *testing.T, args []string) string {
	t.Helper()
	riskFixture(t)
	r, s := riskRunner(false)
	if code := Run(context.Background(), r, args, Request{Repo: "acme/app"}); code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	return s.diag.String()
}

// TestRunRefusesMissingRepo proves the arity guard's reading of a bare verb.
func TestRunRefusesMissingRepo(t *testing.T) {
	r, s := riskRunner(false)
	if code := Run(context.Background(), r, nil, Request{}); code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	if got, want := s.diag.String(), usageRisk+"\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// riskEnvelope is the published --json shape, key for key, so a renamed field
// fails the decode instead of reading zero.
type riskEnvelope struct {
	Verb string `json:"verb"`
	Repo string `json:"repo"`
	Pr   *int   `json:"pr"`
	At   string `json:"at"`
	Next string `json:"next"`
	Data struct {
		Rows       []riskRow `json:"rows"`
		Bundles    int       `json:"bundles"`
		NeedsHuman int       `json:"needs_human"`
		ByBand     struct {
			Critical int `json:"critical"`
			Low      int `json:"low"`
		} `json:"by_band"`
	} `json:"data"`
}

// riskRow is one decoded queue row.
type riskRow struct {
	Bundle string `json:"bundle"`
	Pr     *int   `json:"pr"`
	State  string `json:"state"`
	Score  struct {
		Value int `json:"value"`
	} `json:"score"`
}

// TestRunJSONEnvelope proves the verb's entry point: one envelope under the
// risk verb with a null PR, the tallies, the ranked rows, and the follow-up.
func TestRunJSONEnvelope(t *testing.T) {
	env := runEnvelope(t)
	assertEnvelopeHead(t, env)
	assertEnvelopeRow(t, env)
}

// assertEnvelopeHead checks the envelope fields and the queue's tallies.
func assertEnvelopeHead(t *testing.T, env *riskEnvelope) {
	t.Helper()
	if env.Verb != "risk" || env.Repo != "acme/app" || env.Pr != nil || env.At != riskNow {
		t.Errorf("envelope = %+v, want risk/acme/app/null at the injected clock", *env)
	}
	if env.Data.Bundles != 1 || env.Data.NeedsHuman != 1 || env.Data.ByBand.Critical != 1 {
		t.Errorf("data = %+v, want one critical bundle that needs a human", env.Data)
	}
	if want := "verdict fetch acme/app 42 --index origin/main"; env.Next != want {
		t.Errorf("next = %q, want %q", env.Next, want)
	}
}

// assertEnvelopeRow checks the one seeded row's identity and score.
func assertEnvelopeRow(t *testing.T, env *riskEnvelope) {
	t.Helper()
	row := env.Data.Rows[0]
	if row.Bundle != "42" || row.Pr == nil || *row.Pr != 42 {
		t.Errorf("row = %+v, want the open PR 42", row)
	}
	if row.State != "open" || row.Score.Value != 40 {
		t.Errorf("row = %+v, want state open at score 40", row)
	}
}

// runEnvelope runs the verb in JSON mode and decodes its one envelope.
func runEnvelope(t *testing.T) *riskEnvelope {
	t.Helper()
	riskFixture(t)
	r, s := riskRunner(true)
	if code := Run(context.Background(), r, nil, Request{Repo: "acme/app"}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env riskEnvelope
	if err := json.Unmarshal(s.out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, s.out.String())
	}
	if len(env.Data.Rows) != 1 {
		t.Fatalf("rows = %d, want exactly the one seeded bundle", len(env.Data.Rows))
	}
	return &env
}

// TestRunHumanPrintsOneLinePerRow proves human mode renders the header, one
// row per queued bundle, the skipped tally, and the next command.
func TestRunHumanPrintsOneLinePerRow(t *testing.T) {
	riskFixture(t)
	r, s := riskRunner(false)
	if code := Run(context.Background(), r, nil, Request{Repo: "acme/app"}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if s.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty in human mode", s.out.String())
	}
	lines := strings.Split(strings.TrimSuffix(s.human.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("human = %q, want 4 lines", s.human.String())
	}
	if want := "risk: acme/app — 1 bundle(s), 1 need a human (critical=1 high=0 medium=0 unknown=0 low=0)"; lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if want := "1  critical  score=40  42  open=1  reach=-  churn=-  cat=security  rung=said"; lines[1] != want {
		t.Errorf("row = %q, want %q", lines[1], want)
	}
	if want := "skipped: 1 bundle(s) not open (--status all includes them)"; lines[2] != want {
		t.Errorf("skipped line = %q, want %q", lines[2], want)
	}
	if want := "next: verdict fetch acme/app 42 --index origin/main"; lines[3] != want {
		t.Errorf("next line = %q, want %q", lines[3], want)
	}
}

// TestRunEmptyStoreRendersTheHint proves an empty bundle home is not an error.
func TestRunEmptyStoreRendersTheHint(t *testing.T) {
	t.Chdir(t.TempDir())
	r, s := riskRunner(false)
	if code := Run(context.Background(), r, nil, Request{Repo: "acme/app"}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if want := "risk: acme/app — no bundles under .verdict/state (fetch a PR first)\n"; s.human.String() != want {
		t.Errorf("human = %q, want %q", s.human.String(), want)
	}
}
