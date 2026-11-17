package readycore_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func writeTriggerBundle(t *testing.T, cfg string) (string, string) {
	t.Helper()
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
	}
	dir := writeReadyBundle(t, rs)
	write(t, filepath.Join(".", ".verdict", "config.json"), cfg)
	write(t, filepath.Join(dir, "PR.json"), `{"provider":"github","number":5201,"title":"fix: retry",`+
		`"baseRefName":"main","isDraft":false,"author":{"login":"ana"},"labels":[{"name":"deps"}],`+
		`"files":[{"path":"src/retry.go"}],"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	return dir, rs.BaseSHA
}

func triggerRequest(bundle string) *readycore.Request {
	req := &readycore.Request{Filter: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Commander: &readyCommander{head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		Repo:    "acme/app", Pr: "5201",
	}
	return req
}

func TestReadyFilterRunsMatchingPR(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["src/**"],"authors":["ana"]}}`
	dir, _ := writeTriggerBundle(t, cfg)
	write(t, filepath.Join(".", ".verdict", "config.json"),
		`{"schema_version":"1","trigger":{"paths_include":["never/**"]}}`)
	req := triggerRequest(dir)
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("matching PR refused: %v", ref)
	}
	if resp.Data == nil || !resp.Data.Ready {
		t.Fatalf("matching PR did not reach readiness: %+v", resp.Data)
	}
	stored, found, serr := config.ReadRunState(dir)
	if serr != nil || !found || stored.Trigger == nil {
		t.Fatalf("run receipt has no trigger decision (found=%v err=%v)", found, serr)
	}
	if !stored.Trigger.Run || stored.Trigger.Reason != "matched" {
		t.Errorf("receipt = %+v, want run/matched", stored.Trigger)
	}
}

func TestReadyFilterRefusesFilteredOutPR(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"paths_include":["protected/**"]}}`
	dir, _ := writeTriggerBundle(t, cfg)
	cmdr := &countingCommander{readyCommander: readyCommander{head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	req := triggerRequest(dir)
	req.Runtime.Commander = cmdr
	_, ref := readycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "filtered-out" {
		t.Fatalf("refusal = %+v, want filtered-out", ref)
	}
	if !strings.Contains(ref.Message, "paths_include") {
		t.Errorf("refusal misses the failing predicate: %q", ref.Message)
	}
	if cmdr.calls != 0 {
		t.Errorf("host reads after filter = %d, want 0", cmdr.calls)
	}
	stored, found, serr := config.ReadRunState(dir)
	if serr != nil || !found || stored.Trigger == nil {
		t.Fatalf("run receipt has no trigger decision (found=%v err=%v)", found, serr)
	}
	if stored.Trigger.Run || stored.Trigger.Reason != "paths_include" {
		t.Errorf("receipt = %+v, want filtered/paths_include", stored.Trigger)
	}
}

func TestReadyFilterRefusesUnknownBaseConfigKey(t *testing.T) {
	cfg := `{"schema_version":"1","trigger":{"bogus":true}}`
	dir, _ := writeTriggerBundle(t, cfg)
	_, ref := readycore.Run(context.Background(), triggerRequest(dir))
	if ref == nil || ref.Code != config.CodeBadConfig || !strings.Contains(ref.Message, "trigger.bogus") {
		t.Fatalf("refusal = %+v, want bad-config naming trigger.bogus", ref)
	}
}

type countingCommander struct {
	readyCommander
	calls int
}

func (c *countingCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	c.calls++
	return c.readyCommander.Run(ctx, cmd)
}
