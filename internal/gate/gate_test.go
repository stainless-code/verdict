package gate

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// gateNow is the fixed clock every leg renders with.
func gateNow() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

const (
	gateHead   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gateSigImp = "2222222222222222222222222222222222222222"
	gateSigInf = "3333333333333333333333333333333333333333"
)

// openFinding renders one open findings entry at severity.
func openFinding(sig, severity string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Gate fixture body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
		`"severity":"` + severity + `","sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
}

// gateBundle seeds a PR bundle with the given findings and returns the read
// request every leg runs against.
func gateBundle(t *testing.T, findings string) Request {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeGateFile(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"`+gateHead+`","baseRefOid":"`+strings.Repeat("b", 40)+`"}`)
	writeGateFile(t, ".verdict/state/5101/findings.json", findings)
	writeGateFile(t, ".verdict/state/5101/record.json",
		`{"schema_version":"1","status":"pr","head":"`+gateHead+`",`+
			`"evidence":{"head":"`+gateHead+`","diff_sha256":"`+strings.Repeat("0", 64)+`",`+
			`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},`+
			`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`)
	return Request{Repo: "acme/app", Pr: "5101"}
}

func writeGateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gateRun wires a runner writing to buffers and runs the verb with flags only
// (the front door strips the positionals before dispatch).
func gateRun(t *testing.T, args []string, req Request, jsonMode bool) (int, string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: gateNow, JSONMode: jsonMode}
	code := Run(context.Background(), r, args, req)
	return code, out.String(), diag.String()
}

// TestGateExitTable pins the gate's one exit table: 0 when every requested
// dimension passes (a skip is neutral), 1 when a dimension fails or reads
// unknown, 2 for usage with no envelope, 3 for a tool error.
func TestGateExitTable(t *testing.T) {
	tests := []struct {
		name     string
		store    string
		args     []string
		subs     []string
		banned   []string
		noRecord bool
		want     int
	}{
		{
			name: "pass", store: "[" + openFinding(gateSigInf, "informational") + "]",
			args: []string{"--max-severity", "important", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitOK,
			subs: []string{
				"gate: findings     pass  no open findings at-or-above important",
				"gate: skipped coverage, live_threads",
				"gate: PASS",
			},
			banned: []string{"try:"},
		},
		{
			name: "tier-fail", store: "[" + openFinding(gateSigImp, "important") + "]",
			args: []string{"--max-severity", "important", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: findings     fail  1 open finding(s) at-or-above important",
				"gate: FAIL (1 fail, 2 pass, 2 skip)",
				"try: address or triage the open findings, then re-run gate",
			},
		},
		{
			name: "untagged-never-meets-a-tier", store: "[" + openFinding(gateSigImp, "null") + "]",
			args: []string{"--max-severity", "informational", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitOK,
			subs: []string{"gate: PASS"},
		},
		{
			name: "head-unknown", store: "[]",
			args: []string{"--require-head", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: head         unknown  no completed post record for HEAD aaaaaaa",
				"try: review + post the fetched HEAD, then re-run gate",
			},
		},
		{
			// R11: the evidence dimension rides every gate mode, so a head
			// that matches with no receipt still fails the gate.
			name: "head-pass-without-receipt", store: "[]", noRecord: true,
			args: []string{"--require-head", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: evidence     unknown  evidence receipt missing for head aaaaaaa [review-insufficient]",
				"gate: FAIL (2 unknown, 1 pass, 2 skip)",
			},
		},
		{
			name: "usage-bare", store: "[]", want: cli.ExitUsage,
			subs: []string{"gate needs at least one dimension"},
		},
		{
			name: "usage-live-is-not-a-dimension", args: []string{"--live", "skip"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"gate needs at least one dimension"},
		},
		{
			name: "usage-bad-tier", args: []string{"--max-severity", "severe"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown --max-severity tier 'severe' for gate"},
		},
		{
			name: "usage-bad-live", args: []string{"--require-head", "--live", "sometimes"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown --live posture 'sometimes' for gate"},
		},
		{
			// report's spelling is not gate's: --check is --max-severity here.
			name: "usage-unknown-flag", args: []string{"--check", "high"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown flag '--check' for gate"},
		},
		{
			name: "risk-pass", store: "[" + openFinding(gateSigInf, "informational") + "]",
			args: []string{"--max-risk", "critical", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitOK,
			subs: []string{"gate: risk         pass  reported risk medium <= max-risk critical", "gate: PASS"},
		},
		{
			name: "risk-exceeded", store: "[" + openFinding(gateSigInf, "informational") + "]",
			args: []string{"--max-risk", "low", "--live", "skip", "--coverage", "warn"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: risk         fail  reported risk medium exceeds max-risk low",
				"try: resolve the open findings that raise the reported risk, or re-record a lower risk claim, then re-run gate",
			},
		},
		{
			name: "usage-bad-max-risk", args: []string{"--max-risk", "extreme"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown --max-risk level 'extreme' for gate"},
		},
		{
			name:  "category-narrows-the-tier",
			store: "[" + openFinding(gateSigImp, "important") + "]",
			args:  []string{"--max-severity", "important", "--category", "security", "--live", "skip", "--coverage", "warn"},
			want:  cli.ExitOK,
			subs:  []string{"gate: PASS"},
		},
		{
			name: "usage-category-alone", args: []string{"--require-head", "--category", "security"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"--category narrows --max-severity"},
		},
		{
			name: "usage-bad-category", args: []string{"--max-severity", "critical", "--category", "vibes"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown --category 'vibes' for gate"},
		},
		{
			name: "usage-coverage-is-a-posture", args: []string{"--coverage", "warn"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"gate needs at least one dimension"},
		},
		{
			name: "usage-bad-coverage", args: []string{"--coverage", "80"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown --coverage posture '80' for gate"},
		},
		{
			name: "tool-error", store: `[42]`,
			args: []string{"--max-severity", "critical", "--live", "skip"},
			want: cli.ExitBroke,
			subs: []string{"error: findings store .verdict/state/5101/findings.json is unreadable"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertGateCase(t, &gateCase{
				name: tc.name, store: tc.store, args: tc.args,
				subs: tc.subs, banned: tc.banned, noRecord: tc.noRecord, want: tc.want,
			})
		})
	}
}

// gateCase is one exit-table leg: the fixture store, the argv, the substrings
// the streams must carry (and must not), and the expected exit code.
type gateCase struct {
	name     string
	store    string
	args     []string
	subs     []string
	banned   []string
	noRecord bool
	want     int
}

// assertGateCase runs one exit-table leg and grades its exit code and streams.
func assertGateCase(t *testing.T, tc *gateCase) {
	t.Helper()
	req := gateBundle(t, tc.store)
	if tc.noRecord {
		// A bundle that never recorded a review carries no receipt at all.
		if err := os.Remove(".verdict/state/5101/record.json"); err != nil {
			t.Fatalf("%s: remove record.json: %v", tc.name, err)
		}
	}
	code, stdout, stderr := gateRun(t, tc.args, req, false)
	if code != tc.want {
		t.Fatalf("%s: exit = %d, want %d\nstdout: %s\nstderr: %s", tc.name, code, tc.want, stdout, stderr)
	}
	assertGateStreams(t, tc, stdout, stderr)
}

// assertGateStreams grades what one leg printed: every wanted substring, no
// banned one, and no envelope behind a usage error.
func assertGateStreams(t *testing.T, tc *gateCase, stdout, stderr string) {
	t.Helper()
	assertCarries(t, tc.name, stdout+stderr, tc.subs)
	for _, cut := range tc.banned {
		if strings.Contains(stdout+stderr, cut) {
			t.Errorf("%s: output must not carry %q", tc.name, cut)
		}
	}
	if tc.want == cli.ExitUsage && strings.Contains(stdout, "schema_version") {
		t.Errorf("%s: usage error printed an envelope: %q", tc.name, stdout)
	}
}

// assertCarries requires every wanted substring in one stream.
func assertCarries(t *testing.T, name, output string, subs []string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(output, sub) {
			t.Errorf("%s: output = %q, want it to contain %q", name, output, sub)
		}
	}
}

// TestGateEnvelopeShape pins the gate envelope: exactly one verb:"gate" frame
// on stdout with data.passed and data.dimensions[] in wire order, count null
// where the dimension has no tally, and error null even on a failed gate.
func TestGateEnvelopeShape(t *testing.T) {
	req := gateBundle(t, "["+openFinding(gateSigImp, "important")+"]")
	writeGateFile(t, ".verdict/state/5101/post.json", `{"head":"`+strings.Repeat("c", 40)+`"}`)
	code, stdout, stderr := gateRun(t, []string{"--max-severity", "important", "--require-head", "--live", "skip", "--coverage", "warn"}, req, true)
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1\nstdout: %s", code, stdout)
	}
	data := gateData(t, stdout)
	if data["passed"] != false {
		t.Errorf("passed = %v, want false", data["passed"])
	}
	dims := gateDimensions(t, data)
	assertGateShape(t, dims)
	if dims[1]["status"] != "fail" {
		t.Errorf("head status = %v, want fail for a stale post", dims[1]["status"])
	}
	if dims[1]["count"] != nil {
		t.Errorf("head count = %v, want null", dims[1]["count"])
	}
	if fmt.Sprint(dims[0]["count"]) != "1" {
		t.Errorf("findings count = %v, want 1", dims[0]["count"])
	}
	assertEnvelopeFrame(t, stdout, stderr)
}

// assertEnvelopeFrame pins the one frame on stdout and the clean diagnostic
// stream: a verb tag, a null error, and no envelope on stderr.
func assertEnvelopeFrame(t *testing.T, stdout, stderr string) {
	t.Helper()
	carries := []string{`"verb": "gate"`, `"error": null`}
	for _, want := range carries {
		if !strings.Contains(stdout, want) {
			t.Errorf("envelope = %q, want %q", stdout, want)
		}
	}
	if strings.Contains(stderr, "schema_version") {
		t.Errorf("stderr carries an envelope: %q", stderr)
	}
}

// assertGateShape pins the published dimension order and the exact key set the
// wire contract carries, so an added or renamed key fails here first.
func assertGateShape(t *testing.T, dims []map[string]any) {
	t.Helper()
	wantIDs := []string{"findings", "head", "coverage", "live_threads", "spec", "evidence"}
	if len(dims) != len(wantIDs) {
		t.Fatalf("dimensions = %v, want %d", dims, len(wantIDs))
	}
	for i, dim := range dims {
		if dim["id"] != wantIDs[i] {
			t.Errorf("dimension %d id = %v, want %s", i, dim["id"], wantIDs[i])
		}
		assertDimensionKeys(t, i, dim)
	}
}

// assertDimensionKeys pins one dimension's exact published key set.
func assertDimensionKeys(t *testing.T, i int, dim map[string]any) {
	t.Helper()
	wantKeys := []string{"count", "detail", "id", "status"}
	keys := make([]string, 0, len(dim))
	for k := range dim {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, wantKeys) {
		t.Errorf("dimension %d keys = %v, want %v", i, keys, wantKeys)
	}
}

func gateData(t *testing.T, stdout string) map[string]any {
	t.Helper()
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("envelope is not an object: %s", stdout)
	}
	if m["verb"] != "gate" {
		t.Errorf("verb = %v, want gate", m["verb"])
	}
	if m["error"] != nil {
		t.Errorf("error = %v; a failed gate is not a refusal", m["error"])
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %v", m["data"])
	}
	return data
}

func gateDimensions(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, ok := data["dimensions"].([]any)
	if !ok {
		t.Fatalf("dimensions = %v, want an array", data["dimensions"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, dim := range raw {
		dm, ok := dim.(map[string]any)
		if !ok {
			t.Fatalf("dimension is not an object: %v", dim)
		}
		out = append(out, dm)
	}
	return out
}

// TestGateNeverWritesOrPosts pins the read-only contract: the bundle bytes and
// the store are unchanged after a run, no post.json appears, and the only
// command spawns are the read-shaped live-thread lookup.
func TestGateNeverWritesOrPosts(t *testing.T) {
	req := gateBundle(t, "["+openFinding(gateSigImp, "important")+"]")
	cmd := &recordCommander{stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":` +
		`{"nodes":[{"isResolved":true}]}}}}}`}
	req.Runtime = &core.Runtime{Now: gateNow, Commander: cmd}
	before := bundleFingerprint(t)
	code, _, _ := gateRun(t, []string{"--max-severity", "important", "--coverage", "warn"}, req, false)
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := bundleFingerprint(t); got != before {
		t.Errorf("bundle bytes moved: %s, want %s", got, before)
	}
	if _, err := os.Stat(".verdict/state/5101/post.json"); err == nil {
		t.Error("gate wrote post.json")
	}
	assertReadShapedSpawns(t, cmd.argvs)
}

// assertReadShapedSpawns pins that every spawn was the read-only thread
// lookup: a gh api graphql reviewThreads read and never a mutation.
func assertReadShapedSpawns(t *testing.T, argvs [][]string) {
	t.Helper()
	if len(argvs) != 1 {
		t.Fatalf("spawned %d commands, want the one live-thread read", len(argvs))
	}
	for _, argv := range argvs {
		assertReadShape(t, argv)
	}
}

// assertReadShape pins one spawn as the read-shaped thread lookup.
func assertReadShape(t *testing.T, argv []string) {
	t.Helper()
	joined := strings.Join(argv, " ")
	if len(argv) < 2 || argv[0] != "gh" || argv[1] != "api" || !strings.Contains(joined, "reviewThreads") {
		t.Errorf("gate spawned %v; only the read-shaped thread lookup is allowed", argv)
	}
	if strings.Contains(joined, "mutation") || strings.Contains(joined, "--method") {
		t.Errorf("gate spawned a write-shaped command %v", argv)
	}
}

// TestGateLivePostures pins the live dimension's posture at the verb level:
// skip pays zero network, strict fails closed on an unreachable lookup, and
// auto keeps the fail-open skip so the exit follows the other dimensions.
func TestGateLivePostures(t *testing.T) {
	tests := []struct {
		name      string
		live      string
		status    string
		want      int
		wantCalls int
	}{
		{name: "skip", live: "skip", want: cli.ExitOK, wantCalls: 0, status: "skip"},
		{name: "auto", live: "auto", want: cli.ExitOK, wantCalls: 1, status: "skip"},
		{name: "strict", live: "strict", want: cli.ExitRefused, wantCalls: 1, status: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertLivePosture(t, tc.live, tc.status, tc.want, tc.wantCalls) })
	}
}

// assertLivePosture runs one --live posture against an unreachable lookup and
// grades the exit, the probe count, and the resulting dimension status.
func assertLivePosture(t *testing.T, live, status string, want, wantCalls int) {
	t.Helper()
	req := gateBundle(t, "[]")
	cmd := &recordCommander{fail: true, stderr: "unreachable here"}
	req.Runtime = &core.Runtime{Now: gateNow, Commander: cmd}
	code, stdout, _ := gateRun(t, []string{"--max-severity", "critical", "--live", live, "--coverage", "warn"}, req, true)
	if code != want {
		t.Fatalf("%s: exit = %d, want %d\n%s", live, code, want, stdout)
	}
	if len(cmd.argvs) != wantCalls {
		t.Errorf("%s: commander calls = %d, want %d", live, len(cmd.argvs), wantCalls)
	}
	dims := gateDimensions(t, gateData(t, stdout))
	var got map[string]any
	for _, dim := range dims {
		if dim["id"] == "live_threads" {
			got = dim
			break
		}
	}
	if got == nil {
		t.Fatalf("%s: live_threads dimension missing from %v", live, dims)
	}
	if got["status"] != status {
		t.Errorf("%s: live status = %v, want %s", live, got["status"], status)
	}
}

// recordCommander records every argv it is asked to run and answers with the
// configured streams and status.
type recordCommander struct {
	stdout string
	stderr string
	argvs  [][]string
	fail   bool
}

func (c *recordCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.argvs = append(c.argvs, cmd.Argv)
	res := core.Result{Stdout: c.stdout, Stderr: c.stderr}
	if c.fail {
		res.ExitCode = 1
	}
	return res, nil
}

// bundleFingerprint hashes the bytes of every file under .review/.
func bundleFingerprint(t *testing.T) string {
	t.Helper()
	h := fnv.New64a()
	err := filepath.Walk(".verdict", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		h.Write([]byte(path))
		h.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// TestGateHintNamesReRunOnce pins the recovery line's shape: the hint appends
// its own re-run clause, so a dimension action must not carry one too.
func TestGateHintNamesReRunOnce(t *testing.T) {
	gate := &core.ReportGate{Dimensions: []core.GateDimension{{ID: "compliance", Status: statusFail}}}
	if got := gateHint(gate); strings.Count(got, "re-run gate") != 1 {
		t.Errorf("gateHint = %q, want the re-run clause exactly once", got)
	}
}

// TestGateNextNamesCompliance pins the fourth dimension: a gate whose only
// failing dimension is compliance used to publish "none — every requested
// dimension passed" next to a FAIL verdict, with no recovery line at all.
func TestGateNextNamesCompliance(t *testing.T) {
	for _, status := range []string{statusFail, statusUnknown} {
		gate := &core.ReportGate{Dimensions: []core.GateDimension{{ID: "compliance", Status: status}}}
		got := gateNext(gate)
		if got == "none — every requested dimension passed" {
			t.Errorf("gateNext(%s compliance) = %q, want a recovery action", status, got)
		}
		if got == "" {
			t.Errorf("gateNext(%s compliance) is empty, want a recovery action", status)
		}
	}
}

// gateDefaultArgs gates on findings and skips the live probe, leaving the
// coverage posture at its default. The store it runs against decides that
// dimension, so each leg writes the receipt it needs.
var gateDefaultArgs = []string{"--max-severity", "critical", "--live", "skip"}

// coverageReceipt renders the head and measured summary `record` and `post`
// store side by side.
func coverageReceipt(head, summary string) string {
	return `{"schema_version":"1","head":"` + head + `","posted_at":"2026-01-01T00:00:00Z","coverage":` + summary +
		`,"evidence":{"head":"` + head + `","diff_sha256":"` + strings.Repeat("0", 64) + `",` +
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
		`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`
}

// chunkedReceipt renders the head and chunk attestation a chunked post or
// record stores in place of the measured summary.
func chunkedReceipt(head string, attested, total int) string {
	return fmt.Sprintf(`{"schema_version":"1","head":%q,"posted_at":"2026-01-01T00:00:00Z",`+
		`"chunk_coverage":{"attested":%d,"total":%d}}`, head, attested, total)
}

// The three stored summaries a gate can read: fully attested, partial, and a
// bundle with nothing non-exempt to measure.
const (
	coverageComplete = `{"percent":100,"attested":12,"total":12,"files":1,"exempt":0,"gaps":0}`
	coveragePartial  = `{"percent":75,"attested":9,"total":12,"files":2,"exempt":0,"gaps":2}`
	coverageExempt   = `{"percent":null,"attested":0,"total":0,"files":0,"exempt":2,"gaps":0}`
	// A stored receipt can claim no gaps and still measure under 100%: the
	// gate grades the percentage the store holds, not the gap count alone.
	coverageShortNoGaps = `{"percent":90,"attested":9,"total":10,"files":1,"exempt":0,"gaps":0}`
)

// TestGateCoveragePosture pins the coverage dimension against the stored
// receipt: require (the default) fails on absent or partial, warn reports the
// same shortfall and never fails, and a fully attested or wholly exempt
// bundle passes under either posture.
func TestGateCoveragePosture(t *testing.T) {
	tests := []struct {
		name    string
		post    string
		record  string
		args    []string
		warn    string
		status  string
		subs    []string
		want    int
		chunked bool
	}{
		{
			name: "complete-passes", post: coverageReceipt(gateHead, coverageComplete),
			args: gateDefaultArgs, status: statusPass, want: cli.ExitOK,
			subs: []string{
				"gate: coverage     pass  100% of full hunk spans, both sides and context attested (12/12 lines)",
				"gate: PASS",
			},
		},
		{
			name: "complete-passes-under-warn", post: coverageReceipt(gateHead, coverageComplete),
			args: append(slices.Clone(gateDefaultArgs), "--coverage", "warn"), status: statusPass, want: cli.ExitOK,
			subs: []string{"gate: coverage     pass  100% of full hunk spans, both sides and context attested (12/12 lines)"},
		},
		{
			name: "partial-fails-by-default", post: coverageReceipt(gateHead, coveragePartial),
			args: gateDefaultArgs, status: statusFail, want: cli.ExitRefused,
			subs: []string{
				"gate: coverage     fail  2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines)",
				"try: attest the unattested ranges in review.json (coverage[], or every chunk in chunks[] when the bundle is chunked), then re-record or re-post, then re-run gate",
			},
		},
		{
			name: "under-100-without-gaps-fails", post: coverageReceipt(gateHead, coverageShortNoGaps),
			args: gateDefaultArgs, status: statusFail, want: cli.ExitRefused,
			subs: []string{"gate: coverage     fail  0 unattested range(s) at 90% of full hunk spans, both sides and context (9/10 lines)"},
		},
		{
			name: "partial-warns-under-warn", post: coverageReceipt(gateHead, coveragePartial),
			args: append(slices.Clone(gateDefaultArgs), "--coverage", "warn"), status: statusSkip, want: cli.ExitOK,
			subs: []string{
				"gate: coverage     skip  2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines) — coverage is advisory (--coverage warn)",
				"gate: PASS",
			},
			warn: "warn: coverage: 2 unattested range(s) at 75% of full hunk spans, both sides and context (9/12 lines) — coverage is advisory (--coverage warn)",
		},
		{
			name: "absent-fails-by-default", args: gateDefaultArgs, status: statusUnknown, want: cli.ExitRefused,
			record: "-",
			subs: []string{
				"gate: coverage     unknown  stored coverage absent for HEAD aaaaaaa — no completed post or record",
			},
		},
		{
			name: "absent-warns-under-warn", args: append(slices.Clone(gateDefaultArgs), "--coverage", "warn"),
			status: statusSkip, want: cli.ExitOK,
			subs: []string{"gate: coverage     skip  stored coverage absent for HEAD aaaaaaa — the receipt carries no coverage[] attestation — coverage is advisory (--coverage warn)"},
			warn: "warn: coverage: stored coverage absent for HEAD aaaaaaa — the receipt carries no coverage[] attestation — coverage is advisory (--coverage warn)",
		},
		{
			name: "exempt-only-passes", post: coverageReceipt(gateHead, coverageExempt),
			args: gateDefaultArgs, status: statusPass, want: cli.ExitOK,
			subs: []string{"gate: coverage     pass  nothing changed to measure (2 file(s) exempt)"},
		},
		{
			name: "stale-head-fails", post: coverageReceipt(strings.Repeat("c", 40), coverageComplete),
			record: "-",
			args:   gateDefaultArgs, status: statusUnknown, want: cli.ExitRefused,
			subs: []string{"gate: coverage     unknown  stored coverage is for HEAD ccccccc, not HEAD aaaaaaa"},
		},
		{
			name: "receipt-without-attestation-fails", post: `{"schema_version":"1","head":"` + gateHead + `"}`,
			args: gateDefaultArgs, status: statusUnknown, want: cli.ExitRefused,
			subs: []string{"gate: coverage     unknown  stored coverage absent for HEAD aaaaaaa — the receipt carries no coverage[] attestation"},
		},
		{
			name: "record-receipt-passes", record: coverageReceipt(gateHead, coverageComplete),
			args: gateDefaultArgs, status: statusPass, want: cli.ExitOK,
			subs: []string{"gate: coverage     pass  100% of full hunk spans, both sides and context attested (12/12 lines)"},
		},
		{
			name: "chunked-all-attested-passes", chunked: true,
			post: chunkedReceipt(gateHead, 2, 2),
			args: gateDefaultArgs, status: statusPass, want: cli.ExitOK,
			subs: []string{
				"gate: coverage     pass  2/2 chunk(s) attested",
				"gate: PASS",
			},
		},
		{
			name: "chunked-missing-chunk-fails", chunked: true,
			post: chunkedReceipt(gateHead, 1, 2),
			args: gateDefaultArgs, status: statusFail, want: cli.ExitRefused,
			subs: []string{
				"gate: coverage     fail  1 of 2 chunk(s) unattested",
				"try: attest the unattested ranges in review.json (coverage[], or every chunk in chunks[] when the bundle is chunked), then re-record or re-post, then re-run gate",
			},
		},
		{
			name: "chunked-warns-under-warn", chunked: true,
			post: chunkedReceipt(gateHead, 1, 2),
			args: append(slices.Clone(gateDefaultArgs), "--coverage", "warn"), status: statusSkip, want: cli.ExitOK,
			subs: []string{"gate: coverage     skip  1 of 2 chunk(s) unattested — coverage is advisory (--coverage warn)"},
			warn: "warn: coverage: 1 of 2 chunk(s) unattested — coverage is advisory (--coverage warn)",
		},
		{
			name: "chunked-predates-the-attestation-fails-closed", chunked: true,
			post: coverageReceipt(gateHead, coverageComplete),
			args: gateDefaultArgs, status: statusUnknown, want: cli.ExitRefused,
			subs: []string{"gate: coverage     unknown  stored coverage absent for HEAD aaaaaaa — the receipt predates the chunk attestation"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertCoverageCase(t, tc.post, tc.record, tc.chunked, tc.args, tc.warn, tc.status, tc.subs, tc.want)
		})
	}
}

// assertCoverageCase seeds one receipt and grades the exit, the human lines,
// the warning stream, and the published dimension status.
func assertCoverageCase(t *testing.T, post, record string, chunked bool, args []string, warn, status string, subs []string, want int) {
	t.Helper()
	req := gateBundle(t, "[]")
	seedCoverageStore(t, post, record, chunked)
	code, stdout, stderr := gateRun(t, args, req, false)
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	assertCarries(t, "coverage", stdout, subs)
	if warn != "" && !strings.Contains(stderr, warn) {
		t.Errorf("stderr = %q, want it to carry %q", stderr, warn)
	}
	_, jsonOut, jsonErr := gateRun(t, args, req, true)
	if jsonOut == "" {
		t.Fatalf("no envelope on stdout: %s", jsonErr)
	}
	dim := coverageDimension(t, gateData(t, jsonOut))
	if dim["status"] != status {
		t.Errorf("coverage status = %v, want %s\ndetail: %v", dim["status"], status, dim["detail"])
	}
}

// seedCoverageStore writes the receipts and the chunk manifest one leg gates
// against: post.json, record.json, and the chunked marker are each optional.
func seedCoverageStore(t *testing.T, post, record string, chunked bool) {
	t.Helper()
	const dir = ".verdict/state/5101"
	if post != "" {
		writeGateFile(t, dir+"/post.json", post)
	}
	if record == "-" {
		if err := os.Remove(dir + "/record.json"); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	} else if record != "" {
		writeGateFile(t, dir+"/record.json", record)
	}
	if chunked {
		writeGateFile(t, dir+"/chunks/manifest.json",
			`{"schema_version":"1","chunks":[{"name":"01","diff":"chunks/01.diff"}]}`)
	}
}

// coverageDimension returns the coverage entry of a gate envelope.
func coverageDimension(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	for _, dim := range gateDimensions(t, data) {
		if dim["id"] == "coverage" {
			return dim
		}
	}
	t.Fatalf("no coverage dimension in %v", data["dimensions"])
	return nil
}

// TestGateLinesReachStderrInert runs the gate over a bundle whose record
// carries a violated spec item spelled with an OSC 52 clipboard write, a C1
// CSI, and a BEL. Under --json the dimension lines ride the production
// runner's diagnostic stream, which must deliver the item as plain text.
func TestGateLinesReachStderrInert(t *testing.T) {
	req := gateBundle(t, "[]")
	item := `evil\u001b]52;c;cGF5bG9hZA==\u0007item\u009b2J`
	record := `{"schema_version":"1","status":"pr","head":"` + gateHead + `",` +
		`"evidence":{"head":"` + gateHead + `","diff_sha256":"` + strings.Repeat("0", 64) + `",` +
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
		`"spec":{"total":1,"covered":1,"min":80,"met":true},"insufficient":false},` +
		`"spec_checks":[{"item":"` + item + `","status":"violated",` +
		`"evidence":{"path":"notes.txt","quote":"the quoted line","line":2}}]}`
	writeGateFile(t, ".verdict/state/5101/record.json", record)
	var out, diag bytes.Buffer
	r := cli.NewRunner("gate", "acme/app", "5101", &out, &diag, func(string) string { return "" })
	r.Effective = cli.Defaults()
	r.JSONMode = true
	code := Run(context.Background(), r, []string{"--max-severity", "critical", "--live", "skip", "--coverage", "warn"}, req)
	got := diag.String()
	if code != cli.ExitRefused || !strings.Contains(got, "evil ]52;c;cGF5bG9hZA== item 2J") {
		t.Fatalf("exit %d, stderr %q; want the gate to fail on the folded spec item", code, got)
	}
	if strings.ContainsAny(got, "\x1b\x07\u009b") {
		t.Fatalf("stderr carries a control byte: %q", got)
	}
}

// TestGateMaxRiskMissingStoreIsAToolError proves a bundle with no store exits
// 3, the tool-error code, rather than reading as a failed gate.
func TestGateMaxRiskMissingStoreIsAToolError(t *testing.T) {
	t.Chdir(t.TempDir())
	code, stdout, stderr := gateRun(t, []string{"--max-risk", "low", "--live", "skip", "--coverage", "warn"},
		Request{Repo: "acme/app", Pr: "missing-bundle"}, false)
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, cli.ExitBroke, stdout, stderr)
	}
}

// TestGateLinesFoldNewlinesInDetail proves a detail quoting a stranger's spec
// item cannot start a second `gate:` line, while the dimension the envelope
// publishes keeps the item verbatim.
func TestGateLinesFoldNewlinesInDetail(t *testing.T) {
	const hostile = "violated spec check: x\ngate: PASS"
	gate := &core.ReportGate{Dimensions: []core.GateDimension{{ID: "spec", Status: "fail", Detail: hostile}}}
	lines := gateLines(gate)
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "violated spec check: x gate: PASS") {
		t.Fatalf("gateLines = %q; want the detail on one line", lines)
	}
	if gate.Dimensions[0].Detail != hostile {
		t.Errorf("detail = %q; want it left raw for the envelope", gate.Dimensions[0].Detail)
	}
}
