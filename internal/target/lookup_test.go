package target

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	ghOrigin = "git@github.com:acme/app.git"
	glOrigin = "https://gitlab.com/acme/sub/app.git"
)

func ghEntry(num, head, url string) string {
	return `{"number":` + num + `,"headRefName":"` + head + `","url":"` + url + `"}`
}

func glEntry(num, head, url string) string {
	return `{"iid":` + num + `,"source_branch":"` + head + `","web_url":"` + url + `","title":"t"}`
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
			argv: "gh pr list --repo acme/app --head=fix-x --state open --json number,headRefName,url --limit 2",
			repo: "acme/app", note: `target: acme/app#12 (github, from github.com for branch "fix-x")`,
		},
		{
			name: "gitlab nested", g: fakeGit{origin: glOrigin, branch: "fix-x", hostOut: glOne},
			argv: "glab mr list -R acme/sub/app --source-branch=fix-x -F json --per-page 2",
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
