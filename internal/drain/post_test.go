package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

type drainHostStub struct {
	mu          sync.Mutex
	pageJSON    string
	self        string
	calls       []string
	failRootID  string
	postDelay   time.Duration
	postedCount map[string]int
}

func (s *drainHostStub) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	joined := strings.Join(cmd.Argv, " ")
	s.mu.Lock()
	s.calls = append(s.calls, joined)
	delay := s.postDelay
	if delay > 0 && len(s.postedCount) == 0 && len(cmd.Argv) >= 4 && cmd.Argv[1] == "--method" && cmd.Argv[2] == "POST" {
		s.mu.Unlock()
		time.Sleep(delay)
		s.mu.Lock()
	}
	if s.postedCount == nil {
		s.postedCount = map[string]int{}
	}
	s.mu.Unlock()

	if len(cmd.Argv) >= 4 && cmd.Argv[0] == "gh" && cmd.Argv[1] == "api" && cmd.Argv[2] == "user" {
		return core.Result{Stdout: s.self + "\n"}, nil
	}
	if len(cmd.Argv) >= 3 && cmd.Argv[0] == "gh" && cmd.Argv[1] == "api" && cmd.Argv[2] == "graphql" {
		if strings.Contains(joined, "resolveReviewThread") {
			return core.Result{Stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_2","isResolved":true}}}}`}, nil
		}
		return core.Result{Stdout: s.pageJSON}, nil
	}
	if len(cmd.Argv) >= 4 && cmd.Argv[0] == "gh" && cmd.Argv[1] == "api" && cmd.Argv[2] == "--method" && cmd.Argv[3] == "POST" {
		endpoint := cmd.Argv[4]
		if s.failRootID != "" && strings.Contains(endpoint, "/comments/"+s.failRootID+"/replies") {
			return core.Result{ExitCode: 1, Stderr: "host rejected reply"}, nil
		}
		s.mu.Lock()
		switch {
		case strings.Contains(endpoint, "/comments/101/replies"):
			s.postedCount["PRRT_1"]++
			s.mu.Unlock()
			return core.Result{Stdout: `{"id":501}`}, nil
		case strings.Contains(endpoint, "/comments/201/replies"):
			s.postedCount["PRRT_2"]++
			s.mu.Unlock()
			return core.Result{Stdout: `{"id":502}`}, nil
		}
		s.mu.Unlock()
	}
	return core.Result{ExitCode: 1, Stderr: "unexpected command: " + joined}, nil
}

func (s *drainHostStub) postCount(threadID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.postedCount[threadID]
}

func (s *drainHostStub) callLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

func setupDrainBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	prJSON := `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(prJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte("diff sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func onePendingPageJSON() string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
		{"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"a.go","line":10,"startLine":null,"diffSide":"RIGHT","comments":{"totalCount":2,"nodes":[
			{"id":"101","databaseId":101,"author":{"__typename":"Bot","login":"verdict-bot"},"body":"finding\n\n<!-- verdict-finding sig=6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e -->","createdAt":"2026-11-01T00:00:00Z","url":"https://example.invalid/101"},
			{"id":"102","databaseId":102,"author":{"__typename":"User","login":"alice"},"body":"why?","createdAt":"2026-11-02T00:00:00Z","url":"https://example.invalid/102"}
		]}}
	]}}}}}`
}

func twoPendingPageJSON() string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
		{"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"a.go","line":10,"startLine":null,"diffSide":"RIGHT","comments":{"totalCount":2,"nodes":[
			{"id":"101","databaseId":101,"author":{"__typename":"Bot","login":"verdict-bot"},"body":"finding\n\n<!-- verdict-finding sig=6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e -->","createdAt":"2026-11-01T00:00:00Z","url":"https://example.invalid/101"},
			{"id":"102","databaseId":102,"author":{"__typename":"User","login":"alice"},"body":"why?","createdAt":"2026-11-02T00:00:00Z","url":"https://example.invalid/102"}
		]}},
		{"id":"PRRT_2","isResolved":false,"isOutdated":false,"path":"b.go","line":20,"startLine":null,"diffSide":"RIGHT","comments":{"totalCount":2,"nodes":[
			{"id":"201","databaseId":201,"author":{"__typename":"Bot","login":"verdict-bot"},"body":"finding\n\n<!-- verdict-finding sig=6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e -->","createdAt":"2026-11-01T00:00:00Z","url":"https://example.invalid/201"},
			{"id":"202","databaseId":202,"author":{"__typename":"User","login":"bob"},"body":"is this wrong?","createdAt":"2026-11-02T00:00:00Z","url":"https://example.invalid/202"}
		]}}
	]}}}}}`
}

func writePlanAndDrafts(t *testing.T, dir, provider string, replies []draincore.Reply, overrideDigest string) {
	t.Helper()
	pending := []draincore.Pending{
		{
			ThreadID: "PRRT_1", RootCommentID: "101", WaitingCommentID: "102",
			WaitingAuthor: "alice", WaitingAt: "2026-11-02T00:00:00Z", Path: "a.go",
		},
		{
			ThreadID: "PRRT_2", RootCommentID: "201", WaitingCommentID: "202",
			WaitingAuthor: "bob", WaitingAt: "2026-11-02T00:00:00Z", Path: "b.go",
		},
	}
	digest := draincore.Digest(pending)
	plan := &draincore.File{
		SchemaVersion: draincore.SchemaVersion,
		Provider:      provider,
		PR:            7,
		HeadSHA:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PlannedAt:     "2026-11-17T11:00:00Z",
		Self:          "verdict-bot",
		PlanDigest:    digest,
		Pending:       pending,
		Skipped:       []draincore.Skip{},
		Posted:        []draincore.Posted{},
		Failed:        []draincore.Failed{},
	}
	if err := draincore.WritePlan(filepath.Join(dir, draincore.KeyPlanJSON), plan); err != nil {
		t.Fatal(err)
	}
	if replies == nil {
		return
	}
	if overrideDigest != "" {
		digest = overrideDigest
	}
	raw, err := json.Marshal(draincore.RepliesFile{
		SchemaVersion: draincore.SchemaVersion,
		PlanDigest:    digest,
		Replies:       replies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, draincore.KeyDraftsJSON), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPostLocalRefusalsMakeZeroHostCallsAndKeepThreadsUnchanged(t *testing.T) {
	t.Parallel()
	goodReplies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
	}
	secretReplies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "key AKIAIOSFODNN7EXAMPLE"},
	}

	cases := []struct {
		name     string
		args     []string
		setup    func(t *testing.T, dir string)
		wantCode string
	}{
		{
			name:     "unsupported provider flag without plan",
			args:     []string{"--provider", "forgejo", "--post"},
			setup:    func(*testing.T, string) {},
			wantCode: "provider-unimplemented",
		},
		{
			name:     "missing plan",
			args:     []string{"--post"},
			setup:    func(*testing.T, string) {},
			wantCode: draincore.CodeDrainFailed,
		},
		{
			name: "provider flag disagrees with plan",
			args: []string{"--provider", "gitlab", "--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "github", goodReplies, "")
			},
			wantCode: draincore.CodeDrainFailed,
		},
		{
			name: "missing drafts file",
			args: []string{"--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "github", nil, "")
			},
			wantCode: draincore.CodeDraftsInvalid,
		},
		{
			name: "invalid drafts file",
			args: []string{"--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "github", nil, "")
				plan, err := draincore.ReadPlan(filepath.Join(dir, draincore.KeyPlanJSON))
				if err != nil {
					t.Fatal(err)
				}
				bad := `{"schema_version":"1","plan_digest":"` + plan.PlanDigest + `","replies":[{"bad":true}]}`
				if err := os.WriteFile(filepath.Join(dir, draincore.KeyDraftsJSON), []byte(bad), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: draincore.CodeDraftsInvalid,
		},
		{
			name: "stale drafts file",
			args: []string{"--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "github", goodReplies, "old-digest")
			},
			wantCode: draincore.CodeDraftsStale,
		},
		{
			name: "unsupported plan provider",
			args: []string{"--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "forgejo", goodReplies, "")
			},
			wantCode: "provider-unimplemented",
		},
		{
			name: "secret-shaped draft body",
			args: []string{"--post"},
			setup: func(t *testing.T, dir string) {
				writePlanAndDrafts(t, dir, "github", secretReplies, "")
			},
			wantCode: "post-secret-shaped",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, dir := setupDrainBundle(t)
			sentinel := []byte(`{"sentinel":true}` + "\n")
			threadsPath := filepath.Join(dir, threadscore.KeyThreadsJSON)
			if err := os.WriteFile(threadsPath, sentinel, 0o600); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, dir)

			cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot"}
			var out, diag bytes.Buffer
			r := &cli.Runner{
				Out: &out, Diag: &diag, Human: &diag,
				Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
				Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
			}
			req := Request{
				Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
				Repo:     "acme/app",
				Pr:       "7",
				RepoRoot: root,
			}
			code := Run(context.Background(), r, tc.args, req)
			if code != cli.ExitRefused {
				t.Fatalf("exit = %d, want %d (out=%s diag=%s)", code, cli.ExitRefused, out.String(), diag.String())
			}
			if calls := cmdr.callLog(); len(calls) != 0 {
				t.Errorf("local refusal made %d host call(s): %v", len(calls), calls)
			}
			gotThreads, err := os.ReadFile(threadsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotThreads, sentinel) {
				t.Errorf("threads.json modified by local refusal: got %q, want %q", gotThreads, sentinel)
			}
			var env struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v\n%s", err, out.String())
			}
			if env.Error == nil || env.Error.Code != tc.wantCode {
				t.Errorf("error.code = %+v, want %q", env.Error, tc.wantCode)
			}
		})
	}
}

func TestPostCallOrderLocalChecksThenReadsThenWrites(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	replies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
		{ThreadID: "PRRT_2", WaitingCommentID: "202", Disposition: draincore.DispositionWithdraw, Body: "Answer two."},
	}
	writePlanAndDrafts(t, dir, "github", replies, "")

	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot"}
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Diag: &diag, Human: &diag,
		Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
		Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
	}
	req := Request{
		Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
		Repo:     "acme/app",
		Pr:       "7",
		RepoRoot: root,
	}
	if code := Run(context.Background(), r, []string{"--provider", "github", "--post"}, req); code != cli.ExitOK {
		t.Fatalf("exit = %d, out=%s diag=%s", code, out.String(), diag.String())
	}
	calls := cmdr.callLog()
	if len(calls) != 5 {
		t.Fatalf("call count = %d, want 5: %v", len(calls), calls)
	}
	for i, c := range calls {
		t.Logf("host call %d: %s", i+1, c)
	}
	if !strings.Contains(calls[0], "api graphql") || strings.Contains(calls[0], "resolveReviewThread") {
		t.Errorf("call[0] = %q, want thread refresh graphql", calls[0])
	}
	if !strings.Contains(calls[1], "api user --jq .login") {
		t.Errorf("call[1] = %q, want self login", calls[1])
	}
	if !strings.Contains(calls[2], "comments/101/replies") {
		t.Errorf("call[2] = %q, want reply 101", calls[2])
	}
	if !strings.Contains(calls[3], "comments/201/replies") {
		t.Errorf("call[3] = %q, want reply 201", calls[3])
	}
	if !strings.Contains(calls[4], "resolveReviewThread") {
		t.Errorf("call[4] = %q, want resolve PRRT_2", calls[4])
	}
}

func TestPostIncompleteEmitsSingleJSONEnvelope(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	replies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
		{ThreadID: "PRRT_2", WaitingCommentID: "202", Disposition: draincore.DispositionAnswer, Body: "Answer two."},
	}
	writePlanAndDrafts(t, dir, "github", replies, "")

	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot", failRootID: "201"}
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Diag: &diag, Human: &diag,
		Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
		Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
	}
	req := Request{
		Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
		Repo:     "acme/app",
		Pr:       "7",
		RepoRoot: root,
	}
	code := Run(context.Background(), r, []string{"--post"}, req)
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (out=%s)", code, cli.ExitRefused, out.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var env struct {
		Verb  string          `json:"verb"`
		Next  string          `json:"next"`
		Data  *draincore.File `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode first envelope: %v\n%s", err, out.String())
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contained more than one JSON value (err=%v, extra=%v):\n%s", err, extra, out.String())
	}
	if env.Error == nil || env.Error.Code != draincore.CodeDrainIncomplete {
		t.Errorf("error = %+v, want %s", env.Error, draincore.CodeDrainIncomplete)
	}
	if env.Data == nil || len(env.Data.Posted) != 1 || len(env.Data.Failed) != 1 || env.Data.PostedAt == nil {
		t.Errorf("data receipt = %+v, want 1 posted, 1 failed, non-nil posted_at", env.Data)
	}
	if !strings.Contains(env.Next, "--post") {
		t.Errorf("next = %q, want --post re-run", env.Next)
	}
}

func TestPostConcurrentRunsSerialize(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	replies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
		{ThreadID: "PRRT_2", WaitingCommentID: "202", Disposition: draincore.DispositionAnswer, Body: "Answer two."},
	}
	writePlanAndDrafts(t, dir, "github", replies, "")

	cmdr := &drainHostStub{
		pageJSON:  twoPendingPageJSON(),
		self:      "verdict-bot",
		postDelay: 80 * time.Millisecond,
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	outs := make([]bytes.Buffer, 2)
	diags := make([]bytes.Buffer, 2)

	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r := &cli.Runner{
				Out: &outs[idx], Diag: &diags[idx], Human: &diags[idx],
				Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
				Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
			}
			req := Request{
				Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
				Repo:     "acme/app",
				Pr:       "7",
				RepoRoot: root,
			}
			codes[idx] = Run(context.Background(), r, []string{"--post"}, req)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != cli.ExitOK {
			t.Fatalf("worker %d exit = %d, out=%s, diag=%s", i, code, outs[i].String(), diags[i].String())
		}
	}
	if got := cmdr.postCount("PRRT_1"); got != 1 {
		t.Errorf("PRRT_1 posts = %d, want 1", got)
	}
	if got := cmdr.postCount("PRRT_2"); got != 1 {
		t.Errorf("PRRT_2 posts = %d, want 1", got)
	}
	entries, err := ReadLedger(filepath.Join(dir, draincore.KeyLedgerJSON))
	if err != nil || len(entries) != 2 {
		t.Errorf("ledger = %+v (len %d), err=%v; want 2 entries", entries, len(entries), err)
	}
}
