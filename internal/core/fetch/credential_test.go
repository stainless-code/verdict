package fetchcore

import (
	"context"
	"strings"
	"testing"
)

// TestBitbucketTokenNeverReachesArgv proves the bearer token stays in the curl
// config stdin: the argv is the same five literals whatever the token carries.
func TestBitbucketTokenNeverReachesArgv(t *testing.T) {
	t.Parallel()
	token := "tok\"en\\"
	auth, ref := bitbucketAuthFor(map[string]string{"BITBUCKET_TOKEN": token})
	if ref != nil {
		t.Fatalf("auth refusal: %s: %s", ref.Code, ref.Message)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(testBitbucketFirst): {stdout: `{"values":[],"next":null}`},
	}}
	s, _ := newTestSession(cmdr)
	if _, _, ref := bitbucketPages(context.Background(), s, auth, testBitbucketFirst); ref != nil {
		t.Fatalf("page read refused: %s", ref.Message)
	}
	wantArgv := []string{"curl", "--config", "-", "-sfL", testBitbucketFirst}
	assertArgv(t, cmdr.calls[0], wantArgv)
	wantConfig := "header = \"Authorization: Bearer tok\\\"en\\\\\"\n"
	if cmdr.stdin[0] != wantConfig {
		t.Errorf("config = %q, want %q", cmdr.stdin[0], wantConfig)
	}
	assertNoCredential(t, cmdr.calls[0], token, "Bearer", "Authorization")
}

// TestBitbucketPasswordNeverReachesArgv proves the user/password pair travels
// as curl's quoted `user` directive, not `-u user:pass` on the argv.
func TestBitbucketPasswordNeverReachesArgv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"BITBUCKET_USERNAME": "u\"ser", "BITBUCKET_PASSWORD": "p\\ass"}
	auth, ref := bitbucketAuthFor(env)
	if ref != nil {
		t.Fatalf("auth refusal: %s: %s", ref.Code, ref.Message)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(testBitbucketFirst): {stdout: `{"values":[],"next":null}`},
	}}
	s, _ := newTestSession(cmdr)
	if _, _, ref := bitbucketPages(context.Background(), s, auth, testBitbucketFirst); ref != nil {
		t.Fatalf("page read refused: %s", ref.Message)
	}
	wantArgv := []string{"curl", "--config", "-", "-sfL", testBitbucketFirst}
	assertArgv(t, cmdr.calls[0], wantArgv)
	wantConfig := "user = \"u\\\"ser:p\\\\ass\"\n"
	if cmdr.stdin[0] != wantConfig {
		t.Errorf("config = %q, want %q", cmdr.stdin[0], wantConfig)
	}
	assertNoCredential(t, cmdr.calls[0], env["BITBUCKET_USERNAME"], env["BITBUCKET_PASSWORD"], "-u")
}

// TestForgejoTokenNeverReachesArgv proves the Forgejo read shares the same
// stdin config path, so the token is absent from the spawned argv.
func TestForgejoTokenNeverReachesArgv(t *testing.T) {
	t.Parallel()
	token := "tok\"en\\"
	auth, api, err := forgejoAuthFor(map[string]string{
		"FORGEJO_HOST": "https://example.invalid", "FORGEJO_TOKEN": token,
	}, "o/r", "7")
	if err != nil {
		t.Fatalf("auth refusal: %v", err)
	}
	page := api + "/reviews?limit=50&page=1"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoCall(page): {stdout: `[]`},
	}}
	s, _ := newTestSession(cmdr)
	if got := forgejoList(context.Background(), s, auth, api+"/reviews", "reviews"); got != `[]` {
		t.Errorf("list = %q, want the host bytes", got)
	}
	wantArgv := []string{"curl", "--config", "-", "-sf", page}
	assertArgv(t, cmdr.calls[0], wantArgv)
	wantConfig := "header = \"Authorization: token tok\\\"en\\\\\"\n"
	if cmdr.stdin[0] != wantConfig {
		t.Errorf("config = %q, want %q", cmdr.stdin[0], wantConfig)
	}
	assertNoCredential(t, cmdr.calls[0], token, "Authorization")
}

// TestBitbucketAuthRefusesLineBreak proves a credential carrying CR or LF
// refuses before any curl spawn.
func TestBitbucketAuthRefusesLineBreak(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env  map[string]string
		name string
		want string
	}{
		{
			name: "token",
			env:  map[string]string{"BITBUCKET_TOKEN": "tok\nen"},
			want: "BITBUCKET_TOKEN carries a line break — refusing to build a curl config",
		},
		{
			name: "basic",
			env:  map[string]string{"BITBUCKET_USERNAME": "user", "BITBUCKET_PASSWORD": "pa\rss"},
			want: "BITBUCKET_USERNAME/BITBUCKET_PASSWORD carry a line break — refusing to build a curl config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertLineBreakRefused(t, tc.env, tc.want)
		})
	}
}

// assertLineBreakRefused pins one refused credential gate.
func assertLineBreakRefused(t *testing.T, env map[string]string, want string) {
	t.Helper()
	auth, ref := bitbucketAuthFor(env)
	if ref == nil || ref.Code != codeAuthMissing {
		t.Fatalf("refusal = %v, want auth-missing", ref)
	}
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
	if auth.Stdin() != "" {
		t.Errorf("refused auth built a config: %q", auth.Stdin())
	}
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func assertNoCredential(t *testing.T, argv []string, secrets ...string) {
	t.Helper()
	joined := strings.Join(argv, " ")
	for _, secret := range secrets {
		if strings.Contains(joined, secret) {
			t.Errorf("argv carries %q: %q", secret, argv)
		}
	}
}
