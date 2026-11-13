package schemagen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func waitSchemaStub(cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "--include"):
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\n\r\n" + `{"state":"open","head":{"sha":"abc"}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","status":"IN_PROGRESS"}],"reviews":[]}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`}, nil
	}
	return core.Result{}, errUnexpectedCall(cmd.Argv)
}

func TestRuntimePRWaitEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	req := &readycore.WaitRequest{Until: "merged", Timeout: "1s", Once: true, Repo: "acme/app", Pr: "42", RepoRoot: t.TempDir(), Runtime: &core.Runtime{Now: fixedNow, Commander: scriptCommander{run: waitSchemaStub}}}
	resp, ref := readycore.Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertServiceEnvelope(t, doc, "pr-wait", "acme/app", "42", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	assertWireFields(t, resp.Data.Wire(), map[string]any{"outcome": "condition-unmet", "until": "merged", "observations": json.Number("1")})
}

func TestRuntimeReadyWaitTimeoutEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	root := t.TempDir()
	writeFixture(t, root+"/.verdict/state/42/PR.json", `{"headRefOid":"abc"}`)
	now := fixedNow()
	req := &readycore.Request{WaitFor: "build", Timeout: "1s", Repo: "acme/app", Pr: "42", RepoRoot: root, Runtime: &core.Runtime{Now: func() time.Time { return now }, Commander: scriptCommander{run: waitSchemaStub}}}
	req.PollRuntime = readycore.PollRuntime{Now: func() time.Time { return now }, Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, Jitter: func(time.Duration) time.Duration { return 0 }}
	resp, ref := readycore.Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertServiceEnvelope(t, doc, "ready", "acme/app", "42", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	if resp.Ready || resp.Data.Wait.Outcome != "wait-timeout" {
		t.Fatalf("ready=%v outcome=%s", resp.Ready, resp.Data.Wait.Outcome)
	}
}
