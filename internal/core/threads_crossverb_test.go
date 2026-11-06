package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// threadCrossPR is the live PR payload the stub answers for every verb.
const threadCrossPR = `{"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"state":"OPEN","mergedAt":null}`

// threadCrossFinding is the one open inline finding every verb reads. Its
// body and path match the comments carried by matchable thread nodes.
const threadCrossFinding = `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Thread body here with enough substance.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"critical",` +
	`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"",` +
	`"history":[]}]`

// threadCrossComment is the matchable comment block for calibrate's join.
const threadCrossComment = `"comments":{"nodes":[` +
	`{"body":"Thread body here with enough substance.","path":"notes.txt"}]}`

// threadStubCommander answers the three live reads the verbs issue and
// fails loudly on any unscripted spawn.
type threadStubCommander struct {
	t       *testing.T
	graphql string
}

// Run answers one scripted live read.
func (c threadStubCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Helper()
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "api graphql"):
		return core.Result{Stdout: c.graphql}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: threadCrossPR}, nil
	case strings.Contains(argv, "pr checks"):
		return core.Result{Stdout: "[]"}, nil
	default:
		return core.Result{}, fmt.Errorf("unexpected argv: %q", cmd.Argv)
	}
}

// threadPayload wraps one nodes array in the paginated GraphQL page shape.
func threadPayload(nodes string) string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":` +
		nodes + `}}}}}`
}

// writeThreadCrossFixture creates the shared bundle and returns a request
// whose runtime points at the stub commander.
func writeThreadCrossFixture(t *testing.T, payload string) core.ReadRequest {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadCrossFile(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",`+
			`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeThreadCrossFile(t, ".verdict/state/5101/findings.json", threadCrossFinding)
	writeThreadCrossFile(t, ".verdict/state/5101/post.json",
		`{"schema_version":"1","review":{"id":424242},`+
			`"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"posted_at":"2026-01-01T00:00:00Z","confidence":null,`+
			`"security_summary":null,"tool_version":"t","prompts_hash":"h","model":"m"}`)
	return core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return time.Time{} },
			Commander: threadStubCommander{t: t, graphql: payload},
		},
		Repo: "acme/app",
		Pr:   "5101",
	}
}

// writeThreadCrossFile writes one fixture file, creating parents.
func writeThreadCrossFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reportLiveCount extracts the live-thread count from report's gate line.
func reportLiveCount(t *testing.T, lines []string) int {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(line, "live open thread(s)") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("malformed live gate line %q", line)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("malformed live gate count in %q: %v", line, err)
		}
		return n
	}
	t.Fatalf("no live-thread gate line in %v", lines)
	return 0
}

// calibratePrecisionOpen reads the live join's open outcome count.
func calibratePrecisionOpen(data *calibratecore.Data) (int, bool) {
	precVal, _ := data.Wire().Get("precision")
	prec, ok := precVal.(*jq.Obj)
	if !ok || prec == nil {
		return -1, false
	}
	openVal, _ := prec.Get("open")
	return jq.IntOf(openVal)
}

// calibrateFirstOutcome reads the first joined outcome string.
func calibrateFirstOutcome(data *calibratecore.Data) (string, bool) {
	outcomesVal, _ := data.Wire().Get("outcomes")
	outcomes, ok := outcomesVal.([]any)
	if !ok || len(outcomes) == 0 {
		return "", false
	}
	first, ok := outcomes[0].(*jq.Obj)
	if !ok || first == nil {
		return "", false
	}
	outcomeVal, _ := first.Get("outcome")
	outcome, ok := outcomeVal.(string)
	return outcome, ok
}

// calibrateOpen reads the live join's open outcome count and first outcome.
func calibrateOpen(data *calibratecore.Data) (int, string) {
	open, ok := calibratePrecisionOpen(data)
	if !ok {
		return -1, ""
	}
	outcome, ok := calibrateFirstOutcome(data)
	if !ok {
		return open, ""
	}
	return open, outcome
}

// readyThreadCount runs the ready advisory and returns its thread count.
func readyThreadCount(t *testing.T, req core.ReadRequest) int {
	t.Helper()
	resp, ref := readycore.Run(context.Background(), &readycore.Request{
		ReadRequest: req,
		Check:       true,
	})
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	return resp.Data.ThreadsOpen
}

// reportThreadCount runs report's live gate and returns its thread count.
func reportThreadCount(t *testing.T, req core.ReadRequest) int {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Check: "informational", Live: "auto"},
	})
	if ref != nil {
		t.Fatalf("report refusal: %v", ref)
	}
	return reportLiveCount(t, resp.CheckLines)
}

// calibrateThreadOutcome runs the live join and returns its open count and
// first outcome.
func calibrateThreadOutcome(t *testing.T, req core.ReadRequest) (int, string) {
	t.Helper()
	resp, ref := calibratecore.Run(context.Background(), &calibratecore.Request{
		ReadRequest: req,
	})
	if ref != nil {
		t.Fatalf("calibrate refusal: %v", ref)
	}
	return calibrateOpen(resp.Data)
}

// assertThreadPredicate pins the shared helper's count and resolution
// predicate for one payload shape.
func assertThreadPredicate(t *testing.T, nodesJSON string, want int) {
	t.Helper()
	nodes := threadNodesOf(t, nodesJSON)
	if got := readcommon.OpenThreadCount(nodes); got != want {
		t.Errorf("OpenThreadCount(%s) = %d; want %d", nodesJSON, got, want)
	}
	if got := readcommon.ThreadResolved(nodes[0]); got != (want == 0) {
		t.Errorf("ThreadResolved(first node) = %v; want %v", got, want == 0)
	}
}

// threadCountCase is one payload shape the three verbs must agree on.
type threadCountCase struct {
	name          string
	nodes         string
	wantCalResult string
	want          int
	wantCalOpen   int
}

// TestThreadCountVerbsAgree pins one fail-closed thread count across ready,
// report --live, and calibrate. Ready and report must return the same count
// for every payload shape, including malformed nodes. Calibrate joins
// threads to findings by comment body, so a null or string node can never be
// attributed to a finding; it must report that finding as unknown (never
// resolved), while every matchable node uses the same predicate.
func TestThreadCountVerbsAgree(t *testing.T) {
	matchable := func(resolved string) string {
		return `[{"isResolved":` + resolved + `,` + threadCrossComment + `}]`
	}
	cases := []threadCountCase{
		{"all resolved", matchable("true"), "resolved-unfixed", 0, 0},
		{"one unresolved", matchable("false"), "open", 1, 1},
		{"null node", `[null]`, "unknown", 1, 0},
		{"string node", `["open"]`, "unknown", 1, 0},
		{"missing isResolved", `[{"comments":{"nodes":[` +
			`{"body":"Thread body here with enough substance.","path":"notes.txt"}]}}]`, "open", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertVerbsAgree(t, tc)
		})
	}
}

// assertVerbsAgree proves one payload shape reads the same in all verbs.
func assertVerbsAgree(t *testing.T, tc threadCountCase) {
	t.Helper()
	req := writeThreadCrossFixture(t, threadPayload(tc.nodes))
	assertThreadPredicate(t, tc.nodes, tc.want)
	if got := readyThreadCount(t, req); got != tc.want {
		t.Errorf("ready threads_open = %d; want %d", got, tc.want)
	}
	if got := reportThreadCount(t, req); got != tc.want {
		t.Errorf("report --live count = %d; want %d", got, tc.want)
	}
	open, outcome := calibrateThreadOutcome(t, req)
	if open != tc.wantCalOpen {
		t.Errorf("calibrate precision.open = %d; want %d", open, tc.wantCalOpen)
	}
	if outcome != tc.wantCalResult {
		t.Errorf("calibrate outcome = %q; want %q", outcome, tc.wantCalResult)
	}
}

// threadNodesOf decodes one nodes array for the shared-helper assertions.
func threadNodesOf(t *testing.T, nodes string) []any {
	t.Helper()
	doc, err := jq.Decode([]byte(nodes))
	if err != nil {
		t.Fatal(err)
	}
	out, ok := doc.([]any)
	if !ok {
		t.Fatalf("nodes fixture is not an array: %s", nodes)
	}
	return out
}
