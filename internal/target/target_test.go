package target

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
)

// fakeGit answers the two offline probes inference may run and records every
// argv, so a test proves nothing else (no network verb) was asked.
type fakeGit struct {
	// onHost runs when a host CLI is invoked (a test cancels its context there).
	onHost  func()
	origin  string
	branch  string
	hostOut string
	calls   [][]string
	// hostExit scripts the host CLI's exit status.
	hostExit int
	// hostTruncated scripts an answer that hit the output cap.
	hostTruncated bool
	// noRepo and noOrigin script the two ways git cannot answer.
	noOrigin bool
	noRepo   bool
}

func (f *fakeGit) Run(_ context.Context, c *core.Command) (core.Result, error) {
	f.calls = append(f.calls, c.Argv)
	if c.Argv[0] != "git" {
		return f.host(), nil
	}
	if f.noRepo {
		return core.Result{ExitCode: 128}, nil
	}
	_, args, ok := gitcmd.Split(c.Argv)
	if !ok {
		return core.Result{ExitCode: 129}, errors.New("git call is not hook-free: " + strings.Join(c.Argv, " "))
	}
	switch "git " + strings.Join(args, " ") {
	case "git config --get remote.origin.url":
		if f.noOrigin {
			return core.Result{ExitCode: 1}, nil
		}
		return core.Result{Stdout: f.origin + "\n"}, nil
	case "git branch --show-current":
		return core.Result{Stdout: f.branch + "\n"}, nil
	}
	return core.Result{ExitCode: 129}, errors.New("unexpected git call: " + strings.Join(c.Argv, " "))
}

// host answers a gh or glab call from the scripted fields.
func (f *fakeGit) host() core.Result {
	if f.onHost != nil {
		f.onHost()
	}
	return core.Result{Stdout: f.hostOut, ExitCode: f.hostExit, StdoutTruncated: f.hostTruncated}
}

// checkout seeds .verdict/state bundles: pr number -> PR.json body.
func checkout(t *testing.T, bundles map[string]string) Deps {
	t.Helper()
	root := t.TempDir()
	for pr, body := range bundles {
		dir := filepath.Join(root, ".verdict", "state", pr)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Deps{RepoRoot: func(context.Context) (string, error) { return root, nil }}
}

func resolve(t *testing.T, g *fakeGit, d Deps, q *Request) Resolved {
	t.Helper()
	d.Commander = g
	return Resolve(t.Context(), d, q)
}

const (
	ghBundle12 = `{"headRefName":"fix-x","state":"OPEN","url":"https://github.com/acme/app/pull/12"}`
	ghBundle5  = `{"headRefName":"fix-x","state":"MERGED","url":"https://github.com/acme/app/pull/5"}`
	glBundle7  = `{"provider":"gitlab","head_ref":"fix-x","state":"opened","url":"https://gitlab.com/acme/app/-/merge_requests/7"}`
	otherBr    = `{"headRefName":"other","state":"OPEN","url":"https://github.com/acme/app/pull/3"}`
)

func TestResolveExplicitPairIsUntouched(t *testing.T) {
	g := &fakeGit{}
	got := resolve(t, g, Deps{}, &Request{Verb: "report", Repo: "acme/app", Pr: "12", Rest: []string{"--json"}})
	if got.Repo != "acme/app" || got.Pr != "12" || got.Note != "" || got.Refusal != nil || !slices.Equal(got.Rest, []string{"--json"}) {
		t.Fatalf("explicit pair changed: %+v", got)
	}
	if len(g.calls) != 0 {
		t.Fatalf("explicit pair ran git: %v", g.calls)
	}
}

type urlCase struct {
	rest []string
	name string
	repo string
	pr   string
	q    Request
}

func TestResolveURL(t *testing.T) {
	for _, tc := range []urlCase{
		{
			name: "fetch injects provider", repo: "acme/app", pr: "12",
			q:    Request{Verb: "fetch", Repo: "https://github.com/acme/app/pull/12/files", Rest: []string{"--json"}},
			rest: []string{"--provider", "github", "--json"},
		},
		{
			name: "matching explicit provider stays", repo: "acme/app", pr: "12",
			q:    Request{Verb: "threads", Repo: "https://github.com/acme/app/pull/12", Rest: []string{"--provider=github"}},
			rest: []string{"--provider", "github"},
		},
		{
			name: "gitlab nested for pr-wait", repo: "a/b/c", pr: "7",
			q:    Request{Verb: "pr-wait", Repo: "https://gitlab.com/a/b/c/-/merge_requests/7", Rest: []string{"--once"}},
			rest: []string{"--provider", "gitlab", "--once"},
		},
		{
			name: "bundle verb takes no provider flag", repo: "a/b", pr: "7",
			q:    Request{Verb: "report", Repo: "https://gitlab.com/a/b/-/merge_requests/7"},
			rest: nil,
		},
		{
			name: "provider after the sentinel is not read", repo: "acme/app", pr: "1",
			q:    Request{Verb: "drain", Repo: "https://github.com/acme/app/pull/1", Rest: []string{"--", "--provider", "gitlab"}},
			rest: []string{"--provider", "github", "--", "--provider", "gitlab"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) { checkURLCase(t, &tc) })
	}
}

func checkURLCase(t *testing.T, tc *urlCase) {
	t.Helper()
	g := &fakeGit{}
	got := resolve(t, g, Deps{}, &tc.q)
	if got.Refusal != nil || got.Repo != tc.repo || got.Pr != tc.pr || !slices.Equal(got.Rest, tc.rest) {
		t.Fatalf("got %+v; want %s#%s rest %v", got, tc.repo, tc.pr, tc.rest)
	}
	if !strings.HasPrefix(got.Note, "target: "+tc.repo+"#"+tc.pr+" (") || !strings.Contains(got.Note, "from the URL") {
		t.Fatalf("note = %q", got.Note)
	}
	if len(g.calls) != 0 {
		t.Fatalf("a URL needs no git: %v", g.calls)
	}
}

func TestResolveURLRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		q          Request
	}{
		{
			"provider conflict", "--provider says gitlab",
			Request{Verb: "fetch", Repo: "https://github.com/acme/app/pull/12", Rest: []string{"--provider", "gitlab"}},
		},
		{
			"provider conflict equals form", "--provider says forgejo",
			Request{Verb: "drain", Repo: "https://github.com/acme/app/pull/12", Rest: []string{"--provider=forgejo"}},
		},
		{
			"url plus pr", "already names its number",
			Request{Verb: "fetch", Repo: "https://github.com/acme/app/pull/12", Pr: "13"},
		},
		{"hostile url", "dot-relative", Request{Verb: "fetch", Repo: "https://github.com/acme/../x/pull/12"}},
		{"other host", "not a known code host", Request{Verb: "fetch", Repo: "https://ghe.corp.example/a/b/pull/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(t, &fakeGit{}, Deps{}, &tc.q)
			if got.Refusal == nil || got.Refusal.Code != core.CodeBadTargetURL || !strings.Contains(got.Refusal.Message, tc.want) {
				t.Fatalf("refusal = %+v; want %q", got.Refusal, tc.want)
			}
		})
	}
}

func TestResolveRepoOnlyVerb(t *testing.T) {
	got := resolve(t, &fakeGit{origin: "git@github.com:acme/app.git"}, Deps{}, &Request{Verb: "trend", RepoOnly: true, Rest: []string{"--overlap"}})
	if got.Repo != "acme/app" || got.Pr != "" || got.Refusal != nil || !strings.Contains(got.Note, "origin remote") {
		t.Fatalf("trend from origin: %+v", got)
	}
	got = resolve(t, &fakeGit{}, Deps{}, &Request{Verb: "trend", RepoOnly: true, Repo: "https://github.com/acme/app/pull/12"})
	if got.Repo != "acme/app" || got.Pr != "" || got.Refusal != nil {
		t.Fatalf("trend from URL keeps the repo only: %+v", got)
	}
	got = resolve(t, &fakeGit{}, Deps{}, &Request{Verb: "trend", RepoOnly: true, Repo: "acme/app"})
	if got.Repo != "acme/app" || got.Note != "" {
		t.Fatalf("trend explicit: %+v", got)
	}
}

func TestResolveInfersFromOriginAndRecordedBundle(t *testing.T) {
	g := &fakeGit{origin: "git@github.com:acme/app.git", branch: "fix-x"}
	d := checkout(t, map[string]string{"12": ghBundle12, "3": otherBr})
	got := resolve(t, g, d, &Request{Verb: "post", Rest: []string{"--dry-run"}})
	if got.Refusal != nil || got.Repo != "acme/app" || got.Pr != "12" || !slices.Equal(got.Rest, []string{"--dry-run"}) {
		t.Fatalf("inferred: %+v", got)
	}
	for _, want := range []string{"acme/app#12", "(github", `branch "fix-x"`, ".verdict/state/12"} {
		if !strings.Contains(got.Note, want) {
			t.Errorf("note %q lacks %q", got.Note, want)
		}
	}
	assertOnlyLocalGit(t, g)
}

// assertOnlyLocalGit fails when inference asked git for anything but the two
// offline reads.
func assertOnlyLocalGit(t *testing.T, g *fakeGit) {
	t.Helper()
	for _, c := range g.calls {
		if sub := gitcmd.Subcommand(c); sub != "config" && sub != "branch" {
			t.Errorf("inference ran %v; only local git reads are allowed", c)
		}
	}
}

type refusalCase struct {
	name    string
	want    string
	bundles map[string]string
	g       fakeGit
}

func TestResolveInferenceRefusals(t *testing.T) {
	origin := "git@github.com:acme/app.git"
	for _, tc := range []refusalCase{
		{"no recorded bundle", "verdict post acme/app <pr>", map[string]string{"3": otherBr}, fakeGit{origin: origin, branch: "fix-x"}},
		{"empty store", "no bundle under .verdict/state", nil, fakeGit{origin: origin, branch: "fix-x"}},
		{"two bundles", "5 (merged), 12 (open)", map[string]string{"12": ghBundle12, "5": ghBundle5}, fakeGit{origin: origin, branch: "fix-x"}},
		{"detached head", "HEAD is detached", map[string]string{"12": ghBundle12}, fakeGit{origin: origin}},
		{
			"unknown origin host", "verdict post <owner/repo> <pr>", nil,
			fakeGit{origin: "https://ghe.corp.example/acme/app.git", branch: "fix-x"},
		},
		{
			"bundle of another repo ignored", "no bundle under .verdict/state",
			map[string]string{"12": ghBundle12},
			fakeGit{origin: "git@github.com:acme/other.git", branch: "fix-x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) { checkRefusalCase(t, &tc) })
	}
}

func checkRefusalCase(t *testing.T, tc *refusalCase) {
	t.Helper()
	got := resolve(t, &tc.g, checkout(t, tc.bundles), &Request{Verb: "post"})
	if got.Refusal == nil || got.Refusal.Code != core.CodeTargetUnresolved || !strings.Contains(got.Refusal.Message, tc.want) {
		t.Fatalf("refusal = %+v; want %q (resolved %+v)", got.Refusal, tc.want, got)
	}
	if got.Repo != "" || got.Pr != "" {
		t.Fatalf("a refusal must not leave a half target: %+v", got)
	}
}

func TestResolveNoLocalStateRefuses(t *testing.T) {
	for _, g := range []fakeGit{{noRepo: true}, {noOrigin: true}} {
		got := resolve(t, &g, Deps{}, &Request{Verb: "fetch"})
		if got.Refusal == nil || got.Refusal.Code != core.CodeTargetUnresolved ||
			!strings.Contains(got.Refusal.Message, "verdict fetch <owner/repo> <pr>, or a pull request URL") ||
			got.Repo != "" || got.Pr != "" {
			t.Fatalf("git cannot answer, so the refusal names the args: %+v", got)
		}
	}
}

func TestResolveIncompleteExplicitTargetRefuses(t *testing.T) {
	for _, tc := range []struct {
		want string
		q    Request
	}{
		{"verdict fetch acme/app <pr>", Request{Verb: "fetch", Repo: "acme/app"}},
		{"verdict trend <owner/repo>", Request{Verb: "trend", RepoOnly: true}},
	} {
		got := resolve(t, &fakeGit{noRepo: true}, Deps{}, &tc.q)
		if got.Refusal == nil || !strings.Contains(got.Refusal.Message, tc.want) {
			t.Fatalf("%+v: refusal %+v, want %q", tc.q, got.Refusal, tc.want)
		}
	}
}

func TestResolveNumberOnlyReadsRepoFromOrigin(t *testing.T) {
	got := resolve(t, &fakeGit{origin: "https://gitlab.com/acme/sub/app.git"}, Deps{}, &Request{Verb: "report", Repo: "7"})
	if got.Refusal != nil || got.Repo != "acme/sub/app" || got.Pr != "7" || !strings.Contains(got.Note, "gitlab") {
		t.Fatalf("number only: %+v", got)
	}
}

func TestResolveRepoOnlyInfersPrFromBundle(t *testing.T) {
	g := &fakeGit{branch: "fix-x"}
	d := checkout(t, map[string]string{"7": glBundle7})
	got := resolve(t, g, d, &Request{Verb: "report", Repo: "acme/app"})
	if got.Refusal != nil || got.Repo != "acme/app" || got.Pr != "7" || !strings.Contains(got.Note, `branch "fix-x"`) {
		t.Fatalf("repo + inferred pr: %+v", got)
	}
	for _, c := range g.calls {
		if c[1] == "config" {
			t.Errorf("an explicit repo must not read origin: %v", c)
		}
	}
}

func TestResolveNumberOutsideACheckoutAsksForTheRepo(t *testing.T) {
	got := resolve(t, &fakeGit{noRepo: true}, Deps{}, &Request{Verb: "report", Repo: "12"})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "pull request 12 has no <owner/repo>") ||
		!strings.Contains(got.Refusal.Message, "verdict report <owner/repo> 12") {
		t.Fatalf("a bare number must ask for the repository, not the number: %+v", got)
	}
}

func TestResolveRefusalsSpellTheVerbsOwnNumberArgument(t *testing.T) {
	q := &Request{Verb: "run", PrArg: "<pr|local-name>"}
	for name, g := range map[string]*fakeGit{
		"no git":         {noRepo: true},
		"unrecorded":     {origin: ghOrigin, branch: "fix-x"},
		"unmappable url": {origin: "https://ghe.corp.example/a/b.git", branch: "fix-x"},
	} {
		got := resolve(t, g, checkout(t, nil), q)
		if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "<pr|local-name>") {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestResolveCanceledContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := Resolve(ctx, Deps{Commander: &fakeGit{origin: ghOrigin, branch: "x"}, RepoRoot: checkout(t, nil).RepoRoot}, &Request{Verb: "fetch"})
	if got.Refusal == nil || got.Refusal.Code != "canceled" {
		t.Fatalf("a canceled context must not read as an unresolved target: %+v", got)
	}
}
