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
		name   string
		store  string
		args   []string
		subs   []string
		banned []string
		want   int
	}{
		{
			name: "pass", store: "[" + openFinding(gateSigInf, "informational") + "]",
			args: []string{"--max-severity", "important", "--live", "skip"},
			want: cli.ExitOK,
			subs: []string{
				"gate: findings     pass  no open findings at-or-above important",
				"gate: skipped live_threads",
				"gate: PASS",
			},
			banned: []string{"try:"},
		},
		{
			name: "tier-fail", store: "[" + openFinding(gateSigImp, "important") + "]",
			args: []string{"--max-severity", "important", "--live", "skip"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: findings     fail  1 open finding(s) at-or-above important",
				"gate: FAIL (1 fail, 1 skip)",
				"try: address or triage the open findings, then re-run gate",
			},
		},
		{
			name: "untagged-never-meets-a-tier", store: "[" + openFinding(gateSigImp, "null") + "]",
			args: []string{"--max-severity", "informational", "--live", "skip"},
			want: cli.ExitOK,
			subs: []string{"gate: PASS"},
		},
		{
			name: "head-unknown", store: "[]",
			args: []string{"--require-head", "--live", "skip"},
			want: cli.ExitRefused,
			subs: []string{
				"gate: head         unknown  no completed post record for HEAD aaaaaaa",
				"try: review + post the fetched HEAD, then re-run gate",
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
			name: "usage-unknown-flag", args: []string{"--max-risk", "high"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown flag '--max-risk' for gate"},
		},
		{
			name: "usage-coverage-unknown", args: []string{"--coverage", "80"}, store: "[]",
			want: cli.ExitUsage,
			subs: []string{"unknown flag '--coverage' for gate"},
		},
		{
			name: "tool-error", store: `[42]`,
			args: []string{"--max-severity", "critical", "--live", "skip"},
			want: cli.ExitBroke,
			subs: []string{"error: findings store .verdict/state/5101/findings.json is unreadable"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertGateCase(t, tc.name, tc.store, tc.args, tc.subs, tc.banned, tc.want) })
	}
}

// assertGateCase runs one exit-table leg and grades its exit code and streams.
func assertGateCase(t *testing.T, name, store string, args, subs, banned []string, want int) {
	t.Helper()
	code, stdout, stderr := gateRun(t, args, gateBundle(t, store), false)
	if code != want {
		t.Fatalf("%s: exit = %d, want %d\nstdout: %s\nstderr: %s", name, code, want, stdout, stderr)
	}
	assertCarries(t, name, stdout+stderr, subs)
	for _, cut := range banned {
		if strings.Contains(stdout+stderr, cut) {
			t.Errorf("%s: output must not carry %q", name, cut)
		}
	}
	if want == cli.ExitUsage && strings.Contains(stdout, "schema_version") {
		t.Errorf("%s: usage error printed an envelope: %q", name, stdout)
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
	code, stdout, stderr := gateRun(t, []string{"--max-severity", "important", "--require-head", "--live", "skip"}, req, true)
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
	wantIDs := []string{"findings", "head", "live_threads"}
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
	code, _, _ := gateRun(t, []string{"--max-severity", "important"}, req, false)
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
	code, stdout, _ := gateRun(t, []string{"--max-severity", "critical", "--live", live}, req, true)
	if code != want {
		t.Fatalf("%s: exit = %d, want %d\n%s", live, code, want, stdout)
	}
	if len(cmd.argvs) != wantCalls {
		t.Errorf("%s: commander calls = %d, want %d", live, len(cmd.argvs), wantCalls)
	}
	dims := gateDimensions(t, gateData(t, stdout))
	got := dims[len(dims)-1]
	if got["id"] != "live_threads" {
		t.Fatalf("%s: last dimension = %v, want live_threads", live, got["id"])
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
