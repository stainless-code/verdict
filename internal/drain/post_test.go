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
	postedCount  map[string]int
	forbidWrites func([]string)
	pageJSON     string
	self         string
	failRootID   string
	calls        []string
	postDelay    time.Duration
	mu           sync.Mutex
}

func (s *drainHostStub) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	joined := strings.Join(cmd.Argv, " ")
	s.recordCall(joined)
	if isPostReply(cmd.Argv) || strings.Contains(joined, "resolveReviewThread") {
		if s.forbidWrites != nil {
			s.forbidWrites(cmd.Argv)
		}
	}
	if isPostReply(cmd.Argv) {
		s.maybeDelay()
		return s.handlePostReply(cmd.Argv[4], joined)
	}
	return s.handleReadOrResolve(cmd.Argv, joined)
}

func (s *drainHostStub) recordCall(joined string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, joined)
	if s.postedCount == nil {
		s.postedCount = map[string]int{}
	}
}

func (s *drainHostStub) maybeDelay() {
	s.mu.Lock()
	delay := s.postDelay
	first := len(s.postedCount) == 0
	s.mu.Unlock()
	if delay > 0 && first {
		time.Sleep(delay)
	}
}

func (s *drainHostStub) handlePostReply(endpoint, joined string) (core.Result, error) {
	if s.failRootID != "" && strings.Contains(endpoint, "/comments/"+s.failRootID+"/replies") {
		return core.Result{ExitCode: 1, Stderr: "host rejected reply"}, nil
	}
	for rootID, info := range map[string]struct{ thread, out string }{
		"/comments/101/replies": {"PRRT_1", `{"id":501}`},
		"/comments/201/replies": {"PRRT_2", `{"id":502}`},
	} {
		if strings.Contains(endpoint, rootID) {
			s.mu.Lock()
			s.postedCount[info.thread]++
			s.mu.Unlock()
			return core.Result{Stdout: info.out}, nil
		}
	}
	return core.Result{ExitCode: 1, Stderr: "unexpected command: " + joined}, nil
}

func (s *drainHostStub) handleReadOrResolve(argv []string, joined string) (core.Result, error) {
	if len(argv) >= 4 && argv[0] == "gh" && argv[1] == "api" && argv[2] == "user" {
		return core.Result{Stdout: s.self + "\n"}, nil
	}
	if len(argv) >= 3 && argv[0] == "gh" && argv[1] == "api" && argv[2] == "graphql" {
		if strings.Contains(joined, "resolveReviewThread") {
			return core.Result{Stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_2","isResolved":true}}}}`}, nil
		}
		return core.Result{Stdout: s.pageJSON}, nil
	}
	return core.Result{ExitCode: 1, Stderr: "unexpected command: " + joined}, nil
}

func isPostReply(argv []string) bool {
	return len(argv) >= 5 && argv[0] == "gh" && argv[1] == "api" && argv[2] == "--method" && argv[3] == "POST"
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

func runDrainTest(t *testing.T, root string, cmdr core.Commander, args []string) (outStr, diagStr string, code int) {
	t.Helper()
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
	code = Run(context.Background(), r, args, req)
	return out.String(), diag.String(), code
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

func writePlanAndDrafts(t *testing.T, dir, provider string, replies []draincore.Reply, overrideDigest string) string {
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
		return digest
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
	return plan.PlanDigest
}

type localRefusalCase struct {
	name           string
	planProvider   string
	overrideDigest string
	wantCode       string
	args           []string
	replies        []draincore.Reply
	rawBadDrafts   bool
}

func TestPostLocalRefusalsMakeZeroHostCallsAndKeepThreadsUnchanged(t *testing.T) {
	t.Parallel()
	goodReplies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
	}
	secretReplies := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "key AKIAIOSFODNN7EXAMPLE"},
	}

	cases := []localRefusalCase{
		{name: "unsupported provider flag without plan", args: []string{"--provider", "forgejo", "--post"}, wantCode: "provider-unimplemented"},
		{name: "missing plan", args: []string{"--post"}, wantCode: draincore.CodeDrainFailed},
		{name: "provider flag disagrees with plan", args: []string{"--provider", "gitlab", "--post"}, planProvider: "github", replies: goodReplies, wantCode: draincore.CodeDrainFailed},
		{name: "missing drafts file", args: []string{"--post"}, planProvider: "github", wantCode: draincore.CodeDraftsInvalid},
		{name: "invalid drafts file", args: []string{"--post"}, planProvider: "github", rawBadDrafts: true, wantCode: draincore.CodeDraftsInvalid},
		{name: "stale drafts file", args: []string{"--post"}, planProvider: "github", replies: goodReplies, overrideDigest: "old-digest", wantCode: draincore.CodeDraftsStale},
		{name: "unsupported plan provider", args: []string{"--post"}, planProvider: "forgejo", replies: goodReplies, wantCode: "provider-unimplemented"},
		{name: "secret-shaped draft body", args: []string{"--post"}, planProvider: "github", replies: secretReplies, wantCode: "post-secret-shaped"},
	}

	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runLocalRefusalCase(t, tc)
		})
	}
}

func runLocalRefusalCase(t *testing.T, tc *localRefusalCase) {
	t.Helper()
	root, dir := setupDrainBundle(t)
	sentinel := []byte(`{"sentinel":true}` + "\n")
	threadsPath := filepath.Join(dir, threadscore.KeyThreadsJSON)
	if err := os.WriteFile(threadsPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	prepareLocalRefusalFixture(t, dir, tc)
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot"}
	out, diag, code := runDrainTest(t, root, cmdr, tc.args)
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (out=%s diag=%s)", code, cli.ExitRefused, out, diag)
	}
	if calls := cmdr.callLog(); len(calls) != 0 {
		t.Errorf("local refusal made %d host call(s): %v", len(calls), calls)
	}
	assertSentinelBytes(t, threadsPath, sentinel)
	assertRefusalCode(t, out, tc.wantCode)
}

func prepareLocalRefusalFixture(t *testing.T, dir string, tc *localRefusalCase) {
	t.Helper()
	if tc.planProvider == "" {
		return
	}
	digest := writePlanAndDrafts(t, dir, tc.planProvider, tc.replies, tc.overrideDigest)
	if !tc.rawBadDrafts {
		return
	}
	bad := `{"schema_version":"1","plan_digest":"` + digest + `","replies":[{"bad":true}]}`
	if err := os.WriteFile(filepath.Join(dir, draincore.KeyDraftsJSON), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSentinelBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("threads.json modified by local refusal: got %q, want %q", got, want)
	}
}

func assertRefusalCode(t *testing.T, out, wantCode string) {
	t.Helper()
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
	if env.Error == nil || env.Error.Code != wantCode {
		t.Errorf("error.code = %+v, want %q", env.Error, wantCode)
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
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--provider", "github", "--post"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, out=%s diag=%s", code, out, diag)
	}
	assertHostCallOrder(t, cmdr.callLog())
}

func assertHostCallOrder(t *testing.T, calls []string) {
	t.Helper()
	if len(calls) != 5 {
		t.Fatalf("call count = %d, want 5: %v", len(calls), calls)
	}
	for i, c := range calls {
		t.Logf("host call %d: %s", i+1, c)
	}
	wants := []string{"api graphql", "api user --jq .login", "comments/101/replies", "comments/201/replies", "resolveReviewThread"}
	for i, want := range wants {
		if !strings.Contains(calls[i], want) {
			t.Errorf("call[%d] = %q, want substring %q", i, calls[i], want)
		}
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
	out, _, code := runDrainTest(t, root, cmdr, []string{"--post"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (out=%s)", code, cli.ExitRefused, out)
	}
	assertSingleIncompleteEnvelope(t, out)
}

func assertSingleIncompleteEnvelope(t *testing.T, out string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var env struct {
		Data  *draincore.File `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Verb string `json:"verb"`
		Next string `json:"next"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode first envelope: %v\n%s", err, out)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contained more than one JSON value (err=%v, extra=%v):\n%s", err, extra, out)
	}
	if env.Error == nil || env.Error.Code != "drain-incomplete" {
		t.Errorf("error = %+v, want %s", env.Error, "drain-incomplete")
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
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot", postDelay: 80 * time.Millisecond}
	codes, outs, diags := runConcurrentPosts(t, root, cmdr)
	for i, code := range codes {
		if code != cli.ExitOK {
			t.Fatalf("worker %d exit = %d, out=%s, diag=%s", i, code, outs[i], diags[i])
		}
	}
	if cmdr.postCount("PRRT_1") != 1 || cmdr.postCount("PRRT_2") != 1 {
		t.Errorf("post counts = (1:%d, 2:%d), want (1, 1)", cmdr.postCount("PRRT_1"), cmdr.postCount("PRRT_2"))
	}
	entries, err := ReadLedger(filepath.Join(dir, draincore.KeyLedgerJSON))
	if err != nil || len(entries) != 2 {
		t.Errorf("ledger = %+v (len %d), err=%v; want 2 entries", entries, len(entries), err)
	}
}

func runConcurrentPosts(t *testing.T, root string, cmdr *drainHostStub) ([]int, []string, []string) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	outs := make([]string, 2)
	diags := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			outs[idx], diags[idx], codes[idx] = runDrainTest(t, root, cmdr, []string{"--post"})
		}(i)
	}
	close(start)
	wg.Wait()
	return codes, outs, diags
}
