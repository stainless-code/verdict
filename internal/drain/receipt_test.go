package drain

import (
	"path/filepath"
	"strings"
	"testing"

	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

func TestAgentPostEmptyReplanKeepsReceipt(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	path := filepath.Join(dir, "drain.json")
	previous := &draincore.File{
		Provider: "github", PR: 7, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Posted: []draincore.Posted{{ThreadID: "PRRT_1", ReplyID: "501", Disposition: "answer"}},
		Failed: []draincore.Failed{{ThreadID: "PRRT_2", Reason: "host rejected reply"}},
	}
	if err := draincore.WritePlan(path, previous); err != nil {
		t.Fatal(err)
	}
	cmdr := &drainHostStub{
		pageJSON: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`,
		self:     "verdict-bot",
	}
	out, _, code := runDrainTest(t, root, cmdr, []string{"--agent", "nonexistent-agent", "--post"})
	got, err := draincore.ReadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Posted) != 1 || got.Posted[0].ReplyID != "501" ||
		len(got.Failed) != 1 || got.Failed[0].ThreadID != "PRRT_2" {
		t.Errorf("receipt posted=%+v failed=%+v; want reply 501 and failure PRRT_2", got.Posted, got.Failed)
	}
	if code != 1 || !strings.Contains(out, `"code": "drain-incomplete"`) {
		t.Errorf("exit = %d, out=%s; want drain-incomplete", code, out)
	}
}
