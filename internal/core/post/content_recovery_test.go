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
	readBody string
	accepted []map[string]any
	attempts int
	failAt   int
	readFail bool
}

//nolint:revive // one scripted host exchange keeps the injected failure and accepted writes together
func (host *failingDiscussionHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if strings.HasSuffix(cmd.Argv[2], "/discussions?per_page=100") {
		if host.readFail {
			return core.Result{ExitCode: 1, Stderr: "read unavailable"}, nil
		}
		if host.readBody != "" {
			return core.Result{Stdout: host.readBody}, nil
		}
		raw, err := json.Marshal([]any{map[string]any{"notes": host.accepted}})
		if err != nil {
			return core.Result{}, err
		}
		return core.Result{Stdout: string(raw)}, nil
	}
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
	st.remoteInline = remoteObjects(dir, "inline.json")
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

func TestReviewRetryWithoutFetch(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", `{"schema_version":"1","body":"> ⚠️ Three findings need attention.","approved":false,"comments":[{"path":"notes.txt","line":2,"body":"first bug"},{"path":"notes.txt","line":3,"body":"second bug"},{"path":"notes.txt","line":2,"body":"third bug"}]}`)
	host := &failingDiscussionHost{failAt: 2}
	var warnings, human []string
	req := postTestReq(host, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	if _, ref := Run(t.Context(), req); ref == nil || ref.Code != codePostFailed {
		t.Fatalf("first run = %v, want partial failure", ref)
	}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if len(host.accepted) != 3 {
		t.Fatalf("published %d comments, want exactly 3", len(host.accepted))
	}
	assertProducedRecovery(t, host)
}

//nolint:revive // each failing read must independently prove zero writes
func TestLivePublicationFailsClosed(t *testing.T) {
	for _, body := range []string{"failure", "{bad json}", "{}"} {
		t.Run(body, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGitLabBundle(t, "gitlab", gitlabBundleReview)
			host := &failingDiscussionHost{readFail: body == "failure", readBody: body}
			var warnings, human []string
			req := postTestReq(host, core.PostFlags{Approve: "false", Force: true, ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
			if _, ref := Run(t.Context(), req); ref == nil || ref.Code != codePostFailed {
				t.Fatalf("read failure = %v", ref)
			}
			if host.attempts != 0 || exists(".verdict/state/7/post.json") {
				t.Fatal("live read failure wrote to host or receipt")
			}
		})
	}
}

func assertProducedRecovery(t *testing.T, host *failingDiscussionHost) {
	t.Helper()
	for i, want := range []string{"first bug", "second bug", "third bug"} {
		body, ok := host.accepted[i]["body"].(string)
		if !ok || !strings.HasPrefix(body, want+"\n\n<!-- verdict-finding sig=") {
			t.Fatalf("published body %d = %q", i, body)
		}
	}
}
