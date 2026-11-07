package readcommon_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// threadReadCommander records the argv of one gh read and replays a payload.
type threadReadCommander struct {
	stdout string
	argv   []string
}

// Run replays the scripted stdout.
func (c *threadReadCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.argv = cmd.Argv
	return core.Result{Stdout: c.stdout}, nil
}

// threadNodes decodes one nodes fixture.
func threadNodes(t *testing.T, doc string) []any {
	t.Helper()
	v, err := jq.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	nodes, ok := v.([]any)
	if !ok {
		t.Fatalf("fixture is not an array: %s", doc)
	}
	return nodes
}

// assertThreadCount pins one fixture's open, malformed, and resolved
// readings.
func assertThreadCount(t *testing.T, nodesJSON string, want, malformed int) {
	t.Helper()
	nodes := threadNodes(t, nodesJSON)
	if got := readcommon.OpenThreadCount(nodes); got != want {
		t.Errorf("OpenThreadCount(%s) = %d; want %d", nodesJSON, got, want)
	}
	if got := readcommon.MalformedThreadCount(nodes); got != malformed {
		t.Errorf("MalformedThreadCount(%s) = %d; want %d", nodesJSON, got, malformed)
	}
	if got := readcommon.ThreadResolved(nodes[0]); got != (want == 0) {
		t.Errorf("ThreadResolved(%s) = %v; want %v", nodesJSON, got, want == 0)
	}
}

// TestOpenThreadCountFailClosed pins the shared malformed-node posture: only
// an exactly-true isResolved reads resolved; every other node counts as
// unresolved and is reported as malformed.
func TestOpenThreadCountFailClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		nodes     string
		want      int
		malformed int
	}{
		{"all resolved", `[{"isResolved":true},{"isResolved":true}]`, 0, 0},
		{"one unresolved", `[{"isResolved":false},{"isResolved":true}]`, 1, 0},
		{"null node", `[null]`, 1, 1},
		{"string node", `["open"]`, 1, 1},
		{"missing isResolved", `[{}]`, 1, 1},
		{"null isResolved", `[{"isResolved":null}]`, 1, 1},
		{"string isResolved", `[{"isResolved":"no"}]`, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertThreadCount(t, tc.nodes, tc.want, tc.malformed)
		})
	}
}

// threadPage is a two-page thread payload.
const threadPage = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":` +
	`[{"isResolved":false}]}}}}}` + "\n" +
	`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":` +
	`[{"isResolved":true}]}}}}}`

// threadReadCase is one query-shape expectation.
type threadReadCase struct {
	name         string
	wantQuery    string
	avoidQuery   string
	withComments bool
}

// assertThreadRead pins the flattened nodes and the argv query shape.
func assertThreadRead(t *testing.T, nodes []any, res readcommon.GhResult, argv []string, tc threadReadCase) {
	t.Helper()
	if got := len(nodes); got != 2 {
		t.Errorf("Threads() returned %d nodes; want 2", got)
	}
	if got := readcommon.OpenThreadCount(nodes); got != 1 {
		t.Errorf("OpenThreadCount() = %d; want 1", got)
	}
	if res.Stdout != threadPage {
		t.Errorf("Threads() stdout = %q; want the full page payload", res.Stdout)
	}
	joined := strings.Join(argv, " ")
	if !strings.HasPrefix(joined, "gh api graphql --paginate ") {
		t.Errorf("Threads() argv = %q; want the shared graphql read", joined)
	}
	query := argv[len(argv)-1]
	if !strings.Contains(query, tc.wantQuery) {
		t.Errorf("Threads() query = %q; want %q", query, tc.wantQuery)
	}
	if tc.avoidQuery != "" && strings.Contains(query, tc.avoidQuery) {
		t.Errorf("Threads() query = %q; must not carry %q", query, tc.avoidQuery)
	}
}

// TestThreadsRoutesThroughCommander proves the shared read spawns through
// the injected commander, one query per payload shape, and flattens pages.
func TestThreadsRoutesThroughCommander(t *testing.T) {
	t.Parallel()
	cases := []threadReadCase{
		{"resolution only", "nodes { isResolved }", "comments(first:30)", false},
		{"with comments", "comments(first:30) { nodes { body path } }", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmdr := &threadReadCommander{stdout: threadPage}
			nodes, res, err := readcommon.Threads(
				context.Background(), cmdr, "acme", "app", "5101", tc.withComments)
			if err != nil {
				t.Fatalf("Threads() error: %v", err)
			}
			assertThreadRead(t, nodes, res, cmdr.argv, tc)
		})
	}
}

// TestThreadsMissingCommander proves a nil commander seals the read instead
// of spawning gh directly (the old report --live bypass).
func TestThreadsMissingCommander(t *testing.T) {
	t.Parallel()
	nodes, res, err := readcommon.Threads(context.Background(), nil, "acme", "app", "5101", false)
	if err == nil {
		t.Fatal("Threads(nil commander) = nil error; want a sealed failure")
	}
	if nodes != nil || res.Stdout != "" || res.Stderr != "" {
		t.Errorf("Threads(nil commander) = (%v, %+v); want zero values", nodes, res)
	}
}
