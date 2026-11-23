package bitbucket_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/bitbucket"
	"github.com/stainless-code/verdict/internal/curlauth"
)

// env is the resolved environment every case starts from.
func env() map[string]string {
	return map[string]string{bitbucket.EnvToken: "test-token"}
}

// TestResolve pins the credential config and the pull-scoped base: the two
// literals a read and a write must share, with no credential in the argv.
func TestResolve(t *testing.T) {
	t.Parallel()
	auth, base, ref := bitbucket.Resolve(env(), "acme/app", "7")
	if ref != nil {
		t.Fatalf("Resolve refusal: %s: %s", ref.Code, ref.Message)
	}
	if want := "header = \"Authorization: Bearer test-token\"\n"; auth.Stdin() != want {
		t.Errorf("config = %q, want %q", auth.Stdin(), want)
	}
	if want := "https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/7"; base != want {
		t.Errorf("base = %q, want %q", base, want)
	}
}

// TestAuthTokenBeatsBasic proves the Bearer token wins when both shapes
// are set, matching the fetch gate verbatim.
func TestAuthTokenBeatsBasic(t *testing.T) {
	t.Parallel()
	e := map[string]string{
		bitbucket.EnvToken: "tok", bitbucket.EnvUsername: "u", bitbucket.EnvPassword: "p",
	}
	auth, _, ref := bitbucket.APIBase(e)
	if ref != nil {
		t.Fatalf("APIBase refusal: %s: %s", ref.Code, ref.Message)
	}
	if got := auth.Stdin(); !contains(got, "tok") {
		t.Errorf("config = %q, want the Bearer token to win", got)
	}
}

// TestAuthBasic proves the user and password pair travels as curl's
// quoted `user` directive.
func TestAuthBasic(t *testing.T) {
	t.Parallel()
	e := map[string]string{bitbucket.EnvUsername: "u", bitbucket.EnvPassword: "p"}
	auth, _, ref := bitbucket.APIBase(e)
	if ref != nil {
		t.Fatalf("APIBase refusal: %s: %s", ref.Code, ref.Message)
	}
	if want := "user = \"u:p\"\n"; auth.Stdin() != want {
		t.Errorf("config = %q, want %q", auth.Stdin(), want)
	}
}

// TestAuthGatesTheEnvironment proves a missing credential refuses with
// the published code and names the variables, before any URL is built.
func TestAuthGatesTheEnvironment(t *testing.T) {
	t.Parallel()
	for _, e := range []map[string]string{
		{},
		{bitbucket.EnvUsername: "u"},
	} {
		assertAuthMissing(t, e)
	}
}

// assertAuthMissing pins one refused credential gate.
func assertAuthMissing(t *testing.T, e map[string]string) {
	t.Helper()
	auth, base, ref := bitbucket.APIBase(e)
	if ref == nil {
		t.Fatal("APIBase accepted an environment without a credential")
	}
	if ref.Code != "auth-missing" {
		t.Errorf("code = %q, want auth-missing", ref.Code)
	}
	sentence := "bitbucket needs BITBUCKET_TOKEN (Bearer) or BITBUCKET_USERNAME+BITBUCKET_PASSWORD"
	if ref.Message != sentence {
		t.Errorf("message = %q, want %q", ref.Message, sentence)
	}
	if auth.Stdin() != "" || base != "" {
		t.Errorf("refused APIBase returned config=%q base=%q", auth.Stdin(), base)
	}
}

// TestAuthRefusesLineBreak proves a credential carrying CR or LF refuses
// before any curl spawn.
func TestAuthRefusesLineBreak(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{bitbucket.EnvToken: "tok\nen"}, "BITBUCKET_TOKEN carries a line break — refusing to build a curl config"},
		{map[string]string{bitbucket.EnvUsername: "user", bitbucket.EnvPassword: "pa\rss"}, "BITBUCKET_USERNAME/BITBUCKET_PASSWORD carry a line break — refusing to build a curl config"},
	} {
		assertBitbucketLineBreak(t, tc.env, tc.want)
	}
}

// assertBitbucketLineBreak pins one refused credential gate.
func assertBitbucketLineBreak(t *testing.T, env map[string]string, want string) {
	t.Helper()
	auth, _, ref := bitbucket.APIBase(env)
	if ref == nil || ref.Code != "auth-missing" {
		t.Fatalf("refusal = %v, want auth-missing", ref)
	}
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
	if auth.Stdin() != "" {
		t.Errorf("refused auth built a config: %q", auth.Stdin())
	}
}

// TestDecodePull pins the head read: the source commit hash is the live
// head the drift check compares.
func TestDecodePull(t *testing.T) {
	t.Parallel()
	pull := bitbucket.DecodePull(`{"source":{"commit":{"hash":"abc123"}},"links":{"html":{"href":"https://bitbucket.org/a/b/pr/7"}}}`)
	if got := pull.Source.Commit.Hash; got != "abc123" {
		t.Errorf("head = %q, want abc123", got)
	}
	if got := bitbucket.DecodePullHTML(`{"links":{"html":{"href":"https://bitbucket.org/a/b/pr/7"}}}`); got != "https://bitbucket.org/a/b/pr/7" {
		t.Errorf("html = %q, want the PR page", got)
	}
	if got := bitbucket.DecodePull("not json").Source.Commit.Hash; got != "" {
		t.Errorf("garbage head = %q, want empty", got)
	}
}

// TestDecodeUser pins the nickname precedence: nickname wins over the
// other spellings the host publishes.
func TestDecodeUser(t *testing.T) {
	t.Parallel()
	user, ok := bitbucket.DecodeUser(`{"nickname":"ana","username":"ana2","display_name":"Ana"}`)
	if !ok || user.Login() != "ana" {
		t.Errorf("login = %q, want ana", user.Login())
	}
	user, ok = bitbucket.DecodeUser(`{"username":"ana2"}`)
	if !ok || user.Login() != "ana2" {
		t.Errorf("login = %q, want ana2", user.Login())
	}
	if _, ok := bitbucket.DecodeUser("not json"); ok {
		t.Error("garbage user decoded")
	}
}

// TestDecodeComments pins the page shape: values carries the rows, and a
// page without values is unreadable rather than empty.
func TestDecodeComments(t *testing.T) {
	t.Parallel()
	rows, ok := bitbucket.DecodeComments(`{"values":[{"id":1,"content":{"raw":"first"}}],"next":null}`)
	if !ok || len(rows) != 1 || bitbucket.CommentBody(rows[0]) != "first" {
		t.Fatalf("rows = %v, ok = %v", rows, ok)
	}
	if _, ok := bitbucket.DecodeComments(`[]`); ok {
		t.Error("a bare array decoded as a page")
	}
	if _, ok := bitbucket.DecodeComments(`{}`); ok {
		t.Error("a page without values decoded")
	}
}

// TestCommentHelpers pins the row reads the duplicate check uses.
func TestCommentHelpers(t *testing.T) {
	t.Parallel()
	row := map[string]any{
		"id": float64(42), "content": map[string]any{"raw": "hi"},
		"user": map[string]any{"nickname": "ana"}, "inline": map[string]any{"path": "a.go", "to": float64(2)},
	}
	if !bitbucket.IsInline(row) || bitbucket.CommentBody(row) != "hi" ||
		bitbucket.CommentAuthor(row) != "ana" || bitbucket.CommentID(row) != "42" {
		t.Errorf("row helpers = inline %v body %q author %q id %q", bitbucket.IsInline(row),
			bitbucket.CommentBody(row), bitbucket.CommentAuthor(row), bitbucket.CommentID(row))
	}
	plain := map[string]any{"id": "7"}
	if bitbucket.IsInline(plain) {
		t.Error("a row without inline read as inline")
	}
}

// TestSameOrigin pins the next-URL gate: same-origin follows, foreign
// refuses.
func TestSameOrigin(t *testing.T) {
	t.Parallel()
	base := "https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/7"
	next := base + "/comments?pagelen=100&cursor=2"
	if !bitbucket.SameOrigin(base, next) {
		t.Error("same-origin next refused")
	}
	if bitbucket.SameOrigin(base, "https://evil.invalid/comments") {
		t.Error("foreign next followed")
	}
	if bitbucket.SameOrigin(base, "") {
		t.Error("empty next followed")
	}
}

// TestCredentialNeverOnArgv proves the token stays in the curl config
// stdin, not in the spawned argv.
func TestCredentialNeverOnArgv(t *testing.T) {
	t.Parallel()
	auth, _, ref := bitbucket.APIBase(env())
	if ref != nil {
		t.Fatalf("APIBase refusal: %v", ref)
	}
	argv, stdin := auth.StdinArgv("-sf", "https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/7")
	if want := []string{"--config", "-", "-sf", "https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/7"}; !equal(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if want := "header = \"Authorization: Bearer test-token\"\n"; stdin != want {
		t.Errorf("stdin = %q, want %q", stdin, want)
	}
	if _, ok := any(auth).(curlauth.Credential); !ok {
		t.Error("auth is not a curl credential")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
