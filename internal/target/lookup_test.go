package target

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	ghOrigin = "git@github.com:acme/app.git"
	glOrigin = "https://gitlab.com/acme/sub/app.git"
)

// ghEntry is a same-repository PR as `gh pr list --json` answers it.
func ghEntry(num, head, url string) string {
	return `{"number":` + num + `,"headRefName":"` + head + `","url":"` + url + `",` +
		`"isCrossRepository":false,"headRepositoryOwner":{"login":"acme"},"headRepository":{"name":"app"}}`
}

// ghFork is a PR from another owner's fork that shares the branch name.
func ghFork(num, head, url string) string {
	return `{"number":` + num + `,"headRefName":"` + head + `","url":"` + url + `",` +
		`"isCrossRepository":true,"headRepositoryOwner":{"login":"stranger"},"headRepository":{"name":"app"}}`
}

// glEntry is a same-project MR as `glab mr list -F json` answers it.
func glEntry(num, head, url string) string {
	return `{"iid":` + num + `,"source_branch":"` + head + `","web_url":"` + url + `","title":"t",` +
		`"source_project_id":42,"target_project_id":42}`
}

// glFork is an MR whose source project is a fork of the target project.
func glFork(num, head, url string) string {
	return `{"iid":` + num + `,"source_branch":"` + head + `","web_url":"` + url + `","source_project_id":99,"target_project_id":42}`
}

func lookup(t *testing.T, g *fakeGit, q *Request) Resolved {
	t.Helper()
	q.HostLookup = true
	return resolve(t, g, checkout(t, nil), q)
}

type settleCase struct {
	name string
	argv string
	repo string
	note string
	g    fakeGit
}

// TestHostLookupSettlesOneOpenPR pins the argv each host CLI is asked, as
// literals, and the echo naming the host and branch.
func TestHostLookupSettlesOneOpenPR(t *testing.T) {
	ghOne := "[" + ghEntry("12", "fix-x", "https://github.com/acme/app/pull/12") + "]"
	glOne := "[" + glEntry("7", "fix-x", "https://gitlab.com/acme/sub/app/-/merge_requests/7") + "]"
	for _, tc := range []settleCase{
		{
			name: "github", g: fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: ghOne},
			argv: "gh pr list --repo acme/app --head=fix-x --state open --json number,headRefName,url,isCrossRepository,headRepositoryOwner,headRepository --limit 100",
			repo: "acme/app", note: `target: acme/app#12 (github, from github.com for branch "fix-x")`,
		},
		{
			name: "gitlab nested", g: fakeGit{origin: glOrigin, branch: "fix-x", hostOut: glOne},
			argv: "glab mr list -R acme/sub/app --source-branch=fix-x -F json --per-page 100",
			repo: "acme/sub/app", note: `target: acme/sub/app#7 (gitlab, from gitlab.com for branch "fix-x")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { checkSettleCase(t, &tc) })
	}
}

func checkSettleCase(t *testing.T, tc *settleCase) {
	t.Helper()
	got := lookup(t, &tc.g, &Request{Verb: "fetch"})
	if got.Refusal != nil || got.Repo != tc.repo || got.Note != tc.note {
		t.Fatalf("got %+v", got)
	}
	if last := strings.Join(tc.g.calls[len(tc.g.calls)-1], " "); last != tc.argv {
		t.Fatalf("host argv = %q, want %q", last, tc.argv)
	}
}

type lookupRefusal struct {
	name string
	out  string
	want string
	exit int
}

func TestHostLookupRefusals(t *testing.T) {
	good := ghEntry("12", "fix-x", "https://github.com/acme/app/pull/12")
	for _, tc := range []lookupRefusal{
		{"none", "[]", "has no open pull request for it", 0},
		{"two", "[" + good + "," + ghEntry("13", "fix-x", "https://github.com/acme/app/pull/13") + "]", "more than one open pull request for it (12, 13)", 0},
		{"only a fork's", "[" + ghFork("40", "fix-x", "https://github.com/acme/app/pull/40") + "]", "has no open pull request for it", 0},
		{"two same-repo beside a fork", "[" + good + "," + ghFork("40", "fix-x", "https://github.com/acme/app/pull/40") + "," +
			ghEntry("13", "fix-x", "https://github.com/acme/app/pull/13") + "]", "more than one open pull request for it (12, 13)", 0},
		{"cross flag missing", `[{"number":12,"headRefName":"fix-x","url":"https://github.com/acme/app/pull/12"}]`, "does not trust", 0},
		{"cross flag not a bool", `[{"number":12,"headRefName":"fix-x","url":"https://github.com/acme/app/pull/12","isCrossRepository":"false"}]`, "does not trust", 0},
		{"head repository missing", `[{"number":12,"headRefName":"fix-x","url":"https://github.com/acme/app/pull/12","isCrossRepository":false}]`, "does not trust", 0},
		{"host error", "", "could not be asked (gh failed)", 1},
		{"not json", "no pull requests found", "something verdict does not trust", 0},
		{"trailing data", "[]{}", "something verdict does not trust", 0},
		{"object not list", good, "something verdict does not trust", 0},
		{"wrong head", "[" + ghEntry("12", "other", "https://github.com/acme/app/pull/12") + "]", "something verdict does not trust", 0},
		{"url of another repo", "[" + ghEntry("12", "fix-x", "https://github.com/evil/app/pull/12") + "]", "something verdict does not trust", 0},
		{"url of another host", "[" + ghEntry("12", "fix-x", "https://gitlab.com/acme/app/-/merge_requests/12") + "]", "something verdict does not trust", 0},
		{"url number differs", "[" + ghEntry("12", "fix-x", "https://github.com/acme/app/pull/99") + "]", "something verdict does not trust", 0},
		{"hostile url", "[" + ghEntry("12", "fix-x", "https://github.com/acme/../x/pull/12") + "]", "something verdict does not trust", 0},
		{"fractional number", "[" + ghEntry("12.5", "fix-x", "https://github.com/acme/app/pull/12") + "]", "something verdict does not trust", 0},
		{"huge number", "[" + ghEntry("12345678901", "fix-x", "https://github.com/acme/app/pull/12345678901") + "]", "something verdict does not trust", 0},
		{"string number", `[{"number":"12","headRefName":"fix-x","url":"https://github.com/acme/app/pull/12"}]`, "something verdict does not trust", 0},
		{"one good one bad", "[" + good + "," + ghEntry("13", "other", "https://github.com/acme/app/pull/13") + "]", "something verdict does not trust", 0},
	} {
		t.Run(tc.name, func(t *testing.T) { checkLookupRefusal(t, &tc) })
	}
}

func checkLookupRefusal(t *testing.T, tc *lookupRefusal) {
	t.Helper()
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: tc.out, hostExit: tc.exit}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal == nil || got.Refusal.Code != core.CodeTargetUnresolved || !strings.Contains(got.Refusal.Message, tc.want) ||
		!strings.Contains(got.Refusal.Message, "verdict fetch acme/app <pr>") || got.Repo != "" || got.Pr != "" {
		t.Fatalf("got %+v; want refusal containing %q and the exact args", got, tc.want)
	}
}

func TestHostLookupMalformedGitlabAnswer(t *testing.T) {
	g := &fakeGit{origin: glOrigin, branch: "fix-x", hostOut: `[{"number":7,"headRefName":"fix-x","url":"https://gitlab.com/acme/sub/app/-/merge_requests/7"}]`}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "does not trust") {
		t.Fatalf("a gitlab answer carrying github's field names must be rejected: %+v", got)
	}
}

func hostCalls(g *fakeGit) int {
	n := 0
	for _, c := range g.calls {
		if c[0] == "gh" || c[0] == "glab" {
			n++
		}
	}
	return n
}

var oneOpenPR = "[" + ghEntry("12", "fix-x", "https://github.com/acme/app/pull/12") + "]"

func TestHostLookupNeverForAnOfflineVerb(t *testing.T) {
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR}
	got := resolve(t, g, checkout(t, nil), &Request{Verb: "report"})
	if got.Refusal == nil || hostCalls(g) != 0 {
		t.Fatalf("an offline verb must refuse without the host: %+v calls %v", got, g.calls)
	}
}

func TestHostLookupSkippedWhenABundleRecordsTheBranch(t *testing.T) {
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR}
	got := resolve(t, g, checkout(t, map[string]string{"3": ghBundle12}), &Request{Verb: "fetch", HostLookup: true})
	if got.Pr != "3" || hostCalls(g) != 0 {
		t.Fatalf("a recorded bundle settles it offline: %+v calls %v", got, g.calls)
	}
}

func TestHostLookupSkippedForAnExplicitRepo(t *testing.T) {
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR}
	got := resolve(t, g, checkout(t, nil), &Request{Verb: "fetch", Repo: "acme/app", HostLookup: true})
	if got.Refusal == nil || hostCalls(g) != 0 {
		t.Fatalf("an explicit repo has no provider to ask: %+v calls %v", got, g.calls)
	}
}

func TestHostLookupUnsupportedHost(t *testing.T) {
	g := &fakeGit{origin: "https://codeberg.org/acme/app.git", branch: "fix-x"}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "has no branch lookup") {
		t.Fatalf("got %+v", got)
	}
}

// TestBranchLookupArgvCoversTheHostTable pins the argv table to the shared
// host table in both directions.
func TestBranchLookupArgvCoversTheHostTable(t *testing.T) {
	for _, provider := range []string{"github", "gitlab", "forgejo", "gitcafe", "bitbucket", "azure"} {
		want := slices.Contains(core.BranchLookupProviders(), provider)
		if got := branchLookupArgv(provider, "o/r", "b") != nil; got != want {
			t.Errorf("%s: lookup argv present = %v, host table says %v", provider, got, want)
		}
	}
}

func TestBranchLookupArgvKeepsABranchOutOfFlagPosition(t *testing.T) {
	for _, provider := range core.BranchLookupProviders() {
		for _, arg := range branchLookupArgv(provider, "o/r", "--exec=evil") {
			if arg == "--exec=evil" {
				t.Errorf("%s: the branch is a standalone argument", provider)
			}
		}
	}
}

func TestHostLookupIgnoresForkPRsWithTheSameBranchName(t *testing.T) {
	// A fresh clone on main must not adopt a stranger's fork:main PR.
	fork := ghFork("1610", "main", "https://github.com/acme/app/pull/1610")
	got := lookup(t, &fakeGit{origin: ghOrigin, branch: "main", hostOut: "[" + fork + "]"}, &Request{Verb: "fetch"})
	if got.Refusal == nil || got.Pr != "" || !strings.Contains(got.Refusal.Message, "has no open pull request for it") {
		t.Fatalf("a fork's PR must not become the target: %+v", got)
	}
	mixed := "[" + fork + "," + ghEntry("12", "main", "https://github.com/acme/app/pull/12") + "]"
	got = lookup(t, &fakeGit{origin: ghOrigin, branch: "main", hostOut: mixed}, &Request{Verb: "fetch"})
	if got.Refusal != nil || got.Pr != "12" {
		t.Fatalf("only the same-repo PR counts: %+v", got)
	}
}

func TestHostLookupGitlabForkAndProjectIDs(t *testing.T) {
	url := "https://gitlab.com/acme/sub/app/-/merge_requests/"
	for _, tc := range []struct {
		name, out, want string
	}{
		{"fork only", "[" + glFork("8", "fix-x", url+"8") + "]", "has no open pull request for it"},
		{"fork beside the project's own", "[" + glFork("8", "fix-x", url+"8") + "," + glEntry("7", "fix-x", url+"7") + "]", ""},
		{"project ids missing", `[{"iid":7,"source_branch":"fix-x","web_url":"` + url + `7"}]`, "does not trust"},
	} {
		got := lookup(t, &fakeGit{origin: glOrigin, branch: "fix-x", hostOut: tc.out}, &Request{Verb: "fetch"})
		switch {
		case tc.want == "" && (got.Refusal != nil || got.Pr != "7"):
			t.Errorf("%s: %+v", tc.name, got)
		case tc.want != "" && (got.Refusal == nil || !strings.Contains(got.Refusal.Message, tc.want)):
			t.Errorf("%s: %+v, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHostLookupRejectsAFullPage(t *testing.T) {
	entries := make([]string, lookupMax)
	for i := range entries {
		entries[i] = ghFork("500", "fix-x", "https://github.com/acme/app/pull/500")
	}
	got := lookup(t, &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: "[" + strings.Join(entries, ",") + "]"}, &Request{Verb: "fetch"})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "does not trust") {
		t.Fatalf("a page that fills may hide the real PR and is not trusted: %+v", got)
	}
}

func TestHostLookupRejectsAnAnswerFromAnotherHost(t *testing.T) {
	// Same provider, same repository, different host: GITLAB_HOST maps the
	// self-hosted URL to gitlab, but origin is gitlab.com.
	env := map[string]string{"GITLAB_HOST": "git.corp.example"}
	out := "[" + glEntry("7", "fix-x", "https://git.corp.example/acme/sub/app/-/merge_requests/7") + "]"
	got := lookup(t, &fakeGit{origin: glOrigin, branch: "fix-x", hostOut: out}, &Request{Verb: "fetch", Env: env})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "does not trust") {
		t.Fatalf("an answer from another host is not the origin's PR: %+v", got)
	}
}

func TestHostLookupRejectsATruncatedAnswer(t *testing.T) {
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR, hostTruncated: true}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "could not be asked") {
		t.Fatalf("a capped answer is never parsed: %+v", got)
	}
}

func TestHostLookupComparesTheRepoCaseInsensitively(t *testing.T) {
	g := &fakeGit{origin: "git@github.com:Acme/App.git", branch: "fix-x", hostOut: oneOpenPR}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal != nil || got.Repo != "Acme/App" || got.Pr != "12" {
		t.Fatalf("origin spelling Acme/App against an acme/app answer: %+v", got)
	}
}

func TestHostLookupCanceledIsNotUnresolved(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR, onHost: cancel}
	got := Resolve(ctx, Deps{Commander: g, RepoRoot: checkout(t, nil).RepoRoot}, &Request{Verb: "fetch", HostLookup: true})
	if got.Refusal == nil || got.Refusal.Code != "canceled" {
		t.Fatalf("a cancel during the lookup must read canceled: %+v", got)
	}
}
