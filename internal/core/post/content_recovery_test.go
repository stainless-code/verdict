package postcore

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

type failingDiscussionHost struct {
	accepted []map[string]any
	attempts int
	failAt   int
}

//nolint:revive // one scripted host exchange keeps the injected failure and accepted writes together
func (host *failingDiscussionHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 {
		return core.Result{Stdout: gitlabMRJSON}, nil
	}
	if strings.HasSuffix(cmd.Argv[2], "/discussions") {
		host.attempts++
		if host.attempts == host.failAt {
			return core.Result{ExitCode: 1, Stderr: "injected failure"}, nil
		}
		raw, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return core.Result{}, err
		}
		var note map[string]any
		if err := json.Unmarshal(raw, &note); err != nil {
			return core.Result{}, err
		}
		host.accepted = append(host.accepted, note)
	}
	return core.Result{Stdout: `{"id":99,"created_at":"2026-01-01T00:00:00Z"}`}, nil
}

//nolint:revive // one sequential session proves the partial write and its recovery
func TestPartialPublishRecovery(t *testing.T) {
	t.Parallel()
	var warnings []string
	dir := t.TempDir()
	comments := []any{}
	for _, body := range []string{"first bug", "second bug", "third bug"} {
		comments = append(comments, projectComment(jq.NewObj("path", "notes.txt", "side", "RIGHT", "line", 2, "body", body), ""))
	}
	newState := func() *state {
		st := postTestState(dir, gitlabHead, core.PostFlags{}, &warnings)
		st.comments = comments
		st.event = "COMMENT"
		st.payloads = []string{postPayload("summary", "COMMENT", gitlabHead, comments)}
		return st
	}
	host := &failingDiscussionHost{failAt: 2}
	st := newState()
	if ref := st.remoteDiff(); ref != nil {
		t.Fatal(ref)
	}
	first := postOnce(t.Context(), host, "gitlab", nil, "acme/app", "7", dir, st.payloads[0])
	if first.ok || !first.partial || len(host.accepted) != 1 {
		t.Fatalf("partial = %+v, accepted %d", first, len(host.accepted))
	}
	raw, err := json.Marshal(host.accepted)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/inline.json", string(raw)); err != nil {
		t.Fatal(err)
	}
	st = newState()
	if ref := st.remoteDiff(); ref != nil {
		t.Fatal(ref)
	}
	if len(st.comments) != 2 {
		t.Fatalf("rerun missing %d, want 2", len(st.comments))
	}
	second := postOnce(t.Context(), host, "gitlab", nil, "acme/app", "7", dir, st.payloads[0])
	if !second.ok {
		t.Fatalf("rerun = %+v", second)
	}
	assertRecoveredComments(t, host)
}

func assertRecoveredComments(t *testing.T, host *failingDiscussionHost) {
	t.Helper()
	if len(host.accepted) != 3 {
		t.Fatalf("published %d comments, want exactly 3", len(host.accepted))
	}
	for i, want := range []string{"first bug", "second bug", "third bug"} {
		if host.accepted[i]["body"] != want {
			t.Fatalf("comment %d = %v, want %s", i, host.accepted[i]["body"], want)
		}
	}
}
