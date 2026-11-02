package fetch_test

// Go contract for the fetch and fetch-local bundle builders (go-port 08):
// the bash-vs-Go parity suite is retired with the shell, so the provider
// backends and the Go-owned edge set (audits 18/20/21) are pinned here
// with fixed expectations instead of a differential oracle. Every leg
// calls fetch.Run directly with canned provider stubs; no network, no
// checkout reads beyond the planted root inputs.
//
// The headline pin is the gitcafe full.diff: provider diff bytes land
// VERBATIM (no added or stripped trailing newline). The bash side added
// one trailing newline on Linux (GNU tooling), so the differential proof
// could never be green there; the Go verbatim behavior is the contract.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/fetch"
)

// contractStubs plants the provider CLI stubs first on PATH: each answers
// its shapes from $PWD/canned/ files (identical fixtures per leg).
func contractStubs(t *testing.T, stubBin string) {
	t.Helper()
	for name, script := range map[string]string{
		"gh":   contractGHStub,
		"glab": contractGlabStub,
		"cafe": contractCafeStub,
		"curl": contractCurlStub,
		"az":   contractAZStub,
	} {
		if err := os.WriteFile(filepath.Join(stubBin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

const contractGHStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.gh-calls.log\"\n" +
	"matched=0\n" +
	"if [ -n \"${GH_FAIL_MATCH:-}\" ]; then case \"$*\" in *${GH_FAIL_MATCH}*) matched=1;; esac; fi\n" +
	"if [ \"$matched\" = 1 ]; then echo \"${GH_FAIL_TEXT:-evals-stub-gh: network disabled}\" >&2; exit 1; fi\n" +
	"limited=0\n" +
	"if [ -n \"${GH_RATELIMIT_MATCH:-}\" ]; then case \"$*\" in *${GH_RATELIMIT_MATCH}*) limited=1;; esac; fi\n" +
	"if [ \"$limited\" = 1 ]; then\n" +
	"  count=$(cat \"$PWD/.gh-429-count\" 2>/dev/null || echo 0)\n" +
	"  echo $((count + 1)) > \"$PWD/.gh-429-count\"\n" +
	"  budget=$(cat \"$PWD/.gh-429-budget\" 2>/dev/null || echo 0)\n" +
	"  if [ \"$count\" -lt \"$budget\" ]; then echo \"HTTP 429: You have exceeded a secondary rate limit. retry-after: 0, slow down\" >&2; exit 1; fi\n" +
	"fi\n" +
	"case \"$*\" in\n" +
	"  *--jq*) cat \"$PWD/canned/stat.txt\" 2>/dev/null || printf ''; exit 0;;\n" +
	"  *pr\\ view*) if [ \"${GH_VIEW_FAIL:-0}\" = 1 ]; then echo \"gh: PR not found\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/pr.json\"; exit 0;;\n" +
	"  *pr\\ diff*) if [ \"${GH_DIFF_FAIL:-0}\" = 1 ]; then echo \"gh: diff unavailable\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *files*) cat \"$PWD\"/canned/files-page-*.json 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *reviews*|*comments*) if [ \"${GH_THREADS_FAIL:-0}\" = 1 ]; then echo \"gh: threads down\" >&2; exit 1; fi\n" +
	"    printf '%s\\n' '[]'; exit 0;;\n" +
	"  *compare*) if [ \"${GH_COMPARE_FAIL:-0}\" = 1 ]; then echo \"gh: no common ancestor\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/inc.diff\"; exit 0;;\n" +
	"esac\necho \"evals-stub-gh: network disabled ($*)\" >&2\nexit 1\n"

const contractGlabStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.glab-calls.log\"\n" +
	"if [ -n \"${GLAB_FAIL_MATCH:-}\" ]; then case \"$*\" in *${GLAB_FAIL_MATCH}*)\n" +
	"  echo \"${GLAB_FAIL_TEXT:-glab stub failure}\" >&2; exit 1;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  *mr\\ view*) cat \"$PWD/canned/gl-meta.json\"; exit 0;;\n" +
	"  *--jq*) printf '%s\\n' \"${GLAB_PID:-11}\"; exit 0;;\n" +
	"  *diffs*) cat \"$PWD/canned/gl-changes.json\"; exit 0;;\n" +
	"  *changes*) cat \"$PWD/canned/gl-changes.json\"; exit 0;;\n" +
	"  *mr\\ diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *note\\ list*) cat \"$PWD/canned/gl-notes.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"esac\necho \"glab-stub: unexpected ($*)\" >&2\nexit 1\n"

const contractCafeStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.cafe-calls.log\"\n" +
	"if [ -n \"${CAFE_FAIL_MATCH:-}\" ]; then case \"$*\" in *${CAFE_FAIL_MATCH}*)\n" +
	"  echo \"${CAFE_FAIL_TEXT:-cafe stub failure}\" >&2; exit 1;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  *pr\\ view*) cat \"$PWD/canned/cafe-meta.json\"; exit 0;;\n" +
	"  *pr\\ diff*) cat \"$PWD/canned/cafe-items.json\"; exit 0;;\n" +
	"  *reviews*) case \"$*\" in *--cursor*) cat \"$PWD/canned/cafe-rev2.json\" 2>/dev/null || printf '{}';;\n" +
	"    *) cat \"$PWD/canned/cafe-rev1.json\" 2>/dev/null || printf '{}';; esac; exit 0;;\n" +
	"  *comments*) case \"$*\" in *--cursor*) cat \"$PWD/canned/cafe-cmt2.json\" 2>/dev/null || printf '{}';;\n" +
	"    *) cat \"$PWD/canned/cafe-cmt1.json\" 2>/dev/null || printf '{}';; esac; exit 0;;\n" +
	"esac\necho \"cafe-stub: unexpected ($*)\" >&2\nexit 1\n"

const contractCurlStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.curl-calls.log\"\n" +
	"if [ -n \"${CURL_FAIL_MATCH:-}\" ]; then case \"$*\" in *${CURL_FAIL_MATCH}*)\n" +
	"  echo \"${CURL_FAIL_TEXT:-curl: (22) HTTP error}\" >&2; exit 22;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  */pulls/*\\.diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *diff_url*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  */pulls/*\\/files*) cat \"$PWD/canned/fj-files.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  */pulls/*\\/reviews\\/*\\/comments*) printf '%s' '[{\"id\":1}]'; exit 0;;\n" +
	"  */pulls/*\\/reviews*) printf '%s' '[{\"id\":7}]'; exit 0;;\n" +
	"  */pulls/*) cat \"$PWD/canned/fj-meta.json\"; exit 0;;\n" +
	"  */diffstat*) case \"$*\" in *cursor2*) cat \"$PWD/canned/bb-page2.json\";; *) cat \"$PWD/canned/bb-page1.json\";; esac; exit 0;;\n" +
	"  */comments*) cat \"$PWD/canned/bb-comments.json\" 2>/dev/null || printf '{\"values\":[]}'; exit 0;;\n" +
	"  */diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	// The bitbucket backend reads .../pullrequests/<id> for the meta (the
	// retired parity stub never matched this shape, so both sides failed
	// identically there; this arm gives the backend real coverage). It
	// sorts last so the diffstat/comments/diff arms win first.
	"  *pullrequests*) cat \"$PWD/canned/bb-meta.json\" 2>/dev/null || cat \"$PWD/canned/fj-meta.json\"; exit 0;;\n" +
	"esac\necho \"curl-stub: unexpected ($*)\" >&2\nexit 1\n"

const contractAZStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.az-calls.log\"\n" +
	"if [ -n \"${AZ_FAIL_MATCH:-}\" ]; then case \"$*\" in *${AZ_FAIL_MATCH}*)\n" +
	"  echo \"${AZ_FAIL_TEXT:-az stub failure}\" >&2; exit 1;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  *pr\\ show*) cat \"$PWD/canned/az-meta.json\"; exit 0;;\n" +
	"  *get-access-token*) printf '%s\\n' \"${AZ_TOKEN:-canned-token}\"; exit 0;;\n" +
	"  *threads*) cat \"$PWD/canned/az-threads.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *iterations*changes*) cat \"$PWD/canned/az-changes.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *iterations*) printf '%s' '3'; exit 0;;\n" +
	"esac\necho \"az-stub: unexpected ($*)\" >&2\nexit 1\n"

// contractFixture is one canned provider payload.
type contractFixture struct {
	name string
	doc  string
}

// contractSetup builds one hermetic fetch sandbox: cwd with canned/
// payloads plus the bundle inputs fetch reads beside the run, a private
// HOME, provider stubs first on PATH, and a planted tool root carrying
// the version and prompt inputs. It returns dir, home, and the request env.
func contractSetup(t *testing.T, fixtures []contractFixture, extraEnv map[string]string) (dir, home string, env map[string]string) {
	t.Helper()
	dir = t.TempDir()
	home = filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "canned"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, "canned", f.name), []byte(f.doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, doc := range map[string]string{
		"package.json":        `{"version":"0.0.0-contract"}`,
		"SKILL.md":            "contract skill",
		"prompts/system.md":   "system",
		"prompts/review.md":   "review",
		"prompts/diagrams.md": "diagrams",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stubBin := t.TempDir()
	contractStubs(t, stubBin)
	t.Chdir(dir)
	t.Setenv("PATH", stubBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	env = map[string]string{"HOME": home}
	for k, v := range extraEnv {
		env[k] = v
		// Stubs read the process environment (they are child
		// processes), while fetch reads the request map: overlay
		// both, mirroring the retired harness childEnv.
		t.Setenv(k, v)
	}
	return dir, home, env
}

// contractRun executes fetch.Run in the sandbox, returning exit code plus
// captured stdout/stderr. args are the post-positional flags.
func contractRun(t *testing.T, dir, home string, env map[string]string, repo, pr string, jsonMode bool, args ...string) (int, string, string) {
	t.Helper()
	var out, derr strings.Builder
	r := &cli.Runner{
		Cmd: "fetch", Repo: repo, Pr: pr,
		Out: &out, Diag: &derr, Human: &out,
		Now: time.Now, Effective: cli.Defaults(),
		JSONMode: jsonMode, EnvNoColor: true,
	}
	if jsonMode {
		r.Human = &derr
	}
	code := fetch.Run(context.Background(), r, args, fetch.Request{Env: env, Repo: repo, Pr: pr, Root: dir})
	_ = home
	return code, out.String(), derr.String()
}

// contractPR reads the fetched PR.json shape.
func contractPR(t *testing.T, dir, pr string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", pr, "PR.json"))
	if err != nil {
		t.Fatalf("read PR.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode PR.json: %v", err)
	}
	return doc
}

// contractEnvelope decodes a --json envelope stdout.
func contractEnvelope(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout)
	}
	return doc
}

// contractErrCode reads the envelope error code ("" when error is null).
func contractErrCode(doc map[string]any) string {
	e, ok := doc["error"].(map[string]any)
	if !ok || e == nil {
		return ""
	}
	c, _ := e["code"].(string)
	return c
}

// Bundle heads shared by the fixtures.
const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// stubPRFixtures crafts the standard canned GitHub PR payload plus diff
// and stat, mirroring the retired parity setup.
func stubPRFixtures() []contractFixture {
	return []contractFixture{
		{"pr.json", `{"state":"OPEN","headRefOid":"` + headA + `","baseRefOid":"` + headB + `",` +
			`"author":{"login":"teammate"},"files":[{"path":"notes.txt","additions":3,"deletions":1}],` +
			`"additions":3,"deletions":1}`},
		{"full.diff", "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"},
		{"stat.txt", "notes.txt (+3/-1)\n"},
		{"inc.diff", "diff --git a/notes.txt b/notes.txt\nindex 2222222..3333333 100644\n" +
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n+new line\n+newer line\n bottom\n"},
	}
}

// contractFullDiff reads the fetched full.diff bytes.
func contractFullDiff(t *testing.T, dir, pr string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", pr, "full.diff"))
	if err != nil {
		t.Fatalf("read full.diff: %v", err)
	}
	return string(raw)
}

// TestFetchContractGitcafe proves the gitcafe backend end to end, with the
// headline verbatim pin: the canned patch (trailing newline included)
// lands byte-exact in full.diff — no added or stripped newline.
func TestFetchContractGitcafe(t *testing.T) {
	patch := "@@ -1 +1 @@\n-a\n+b\n"
	dir, home, env := contractSetup(t, []contractFixture{
		{"cafe-meta.json", `{"data":{"resource":{"number":7,"title":"PR seven",` +
			`"description":"body","targetBranch":"main","sourceBranch":"feat","observedBaseOid":"` + headB + `",` +
			`"sourceOid":"` + headA + `","author":{"handle":"mate"},"state":"open","draft":false},"url":"https://example.invalid/7"}}`},
		{"cafe-items.json", `[{"path":"a.txt","patch":"` + jsonEscape(patch) + `",` +
			`"status":"modified","additions":1,"deletions":1}]`},
		{"cafe-rev1.json", `{"data":{"items":[]}}`},
		{"cafe-cmt1.json", `{"data":{"items":[]}}`},
	}, nil)
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4707", true,
		"--provider", "gitcafe", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	pr := contractPR(t, dir, "4707")
	if pr["provider"] != "gitcafe" || pr["number"] != float64(7) {
		t.Fatalf("PR.json provider/number = %v/%v, want gitcafe/7", pr["provider"], pr["number"])
	}
	wantDiff := "diff --git a/a.txt b/a.txt\n" + patch
	if got := contractFullDiff(t, dir, "4707"); got != wantDiff {
		t.Fatalf("full.diff:\n%q\nwant verbatim:\n%q", got, wantDiff)
	}
}

// jsonEscape renders s as a JSON string body (no surrounding quotes).
func jsonEscape(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw[1 : len(raw)-1])
}

// TestFetchContractGitlab proves the gitlab backend shape: normalized
// PR.json plus the verbatim served diff.
func TestFetchContractGitlab(t *testing.T) {
	diff := "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n" +
		"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"
	dir, home, env := contractSetup(t, []contractFixture{
		{"gl-meta.json", `{"iid":5,"title":"MR five","description":"body","target_branch":"main",` +
			`"source_branch":"feat","diff_refs":{"base_sha":"` + headB + `","head_sha":"` + headA + `"},` +
			`"author":{"username":"mate"},"web_url":"https://example.invalid/5","state":"opened","draft":false}`},
		{"gl-changes.json", `{"changes":[{"old_path":"a.txt","new_path":"a.txt","new_file":false,` +
			`"deleted_file":false,"renamed_file":false}]}`},
		{"full.diff", diff},
		{"gl-notes.json", `[{"type":"DiffNote"},{"type":"Note"}]`},
	}, nil)
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4706", true,
		"--provider", "gitlab", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	pr := contractPR(t, dir, "4706")
	if pr["provider"] != "gitlab" || pr["number"] != float64(5) || pr["state"] != "open" {
		t.Fatalf("PR.json = %v, want gitlab/5/open", pr)
	}
	if got := contractFullDiff(t, dir, "4706"); got != diff {
		t.Fatalf("full.diff:\n%q\nwant:\n%q", got, diff)
	}
}

// TestFetchContractForgejo proves the forgejo backend shape and its
// missing-credential refusal.
func TestFetchContractForgejo(t *testing.T) {
	fixtures := []contractFixture{
		{"fj-meta.json", `{"index":9,"title":"PR nine","body":"body","base":{"label":"main",` +
			`"sha":"` + headB + `"},"head":{"label":"feat","sha":"` + headA + `"},"user":{"login":"mate"},` +
			`"html_url":"https://example.invalid/9","state":"open","diff_url":"https://example.invalid/9.diff"}`},
		{"full.diff", "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n" +
			"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"},
		{"fj-files.json", `[{"filename":"a.txt","status":"modified","additions":1,"deletions":1}]`},
	}
	dir, home, env := contractSetup(t, fixtures,
		map[string]string{"FORGEJO_HOST": "https://example.invalid", "FORGEJO_TOKEN": "tok"})
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4708", true,
		"--provider", "forgejo", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	pr := contractPR(t, dir, "4708")
	if pr["provider"] != "forgejo" || pr["number"] != float64(9) {
		t.Fatalf("PR.json provider/number = %v/%v, want forgejo/9", pr["provider"], pr["number"])
	}
	dir2, home2, env2 := contractSetup(t, fixtures, nil)
	code, stdout, _ = contractRun(t, dir2, home2, env2, "acme/app", "4708", true,
		"--provider", "forgejo", "--json")
	if code != 1 || contractErrCode(contractEnvelope(t, stdout)) != "auth-missing" {
		t.Fatalf("auth-missing exit = %d code = %q, want 1/auth-missing\n%s", code, contractErrCode(contractEnvelope(t, stdout)), stdout)
	}
}

// TestFetchContractBitbucket proves the bitbucket backend shape, the
// diffstat pager across two pages, and the missing-credential refusal.
func TestFetchContractBitbucket(t *testing.T) {
	fixtures := []contractFixture{
		{"bb-meta.json", `{"id":11,"title":"PR eleven","summary":{"raw":"body"},` +
			`"destination":{"branch":{"name":"main"},"commit":{"hash":"` + headB + `"}},` +
			`"source":{"branch":{"name":"feat"},"commit":{"hash":"` + headA + `"}},` +
			`"author":{"nickname":"mate"},"links":{"html":{"href":"https://example.invalid/11"}},` +
			`"state":"OPEN","draft":false}`},
		{"full.diff", "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n" +
			"diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-c\n+d\n"},
		{"bb-page1.json", `{"values":[{"new":{"path":"a.txt"},"old":{"path":"a.txt"},` +
			`"status":"modified","lines_added":1,"lines_removed":0}],"next":"https://example.invalid/diffstat?cursor=cursor2"}`},
		{"bb-page2.json", `{"values":[{"new":{"path":"b.txt"},"old":{"path":"b.txt"},` +
			`"status":"modified","lines_added":1,"lines_removed":0}],"next":null}`},
		{"bb-comments.json", `{"values":[]}`},
	}
	dir, home, env := contractSetup(t, fixtures, map[string]string{"BITBUCKET_TOKEN": "tok"})
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4709", true,
		"--provider", "bitbucket", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	pr := contractPR(t, dir, "4709")
	if pr["provider"] != "bitbucket" || pr["number"] != float64(11) {
		t.Fatalf("PR.json provider/number = %v/%v, want bitbucket/11", pr["provider"], pr["number"])
	}
	files, _ := pr["files"].([]any)
	var paths []string
	for _, f := range files {
		if m, ok := f.(map[string]any); ok {
			paths = append(paths, m["path"].(string))
		}
	}
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "b.txt" {
		t.Fatalf("paged files = %v, want [a.txt b.txt]", paths)
	}
	dir2, home2, env2 := contractSetup(t, fixtures, nil)
	code, stdout, _ = contractRun(t, dir2, home2, env2, "acme/app", "4709", true,
		"--provider", "bitbucket", "--json")
	if code != 1 || contractErrCode(contractEnvelope(t, stdout)) != "auth-missing" {
		t.Fatalf("auth-missing exit = %d, want 1/auth-missing\n%s", code, stdout)
	}
}

// TestFetchContractAzure proves the azure backend against a local git
// remote (hermetic: the stub serves the remote's real SHAs back), plus the
// missing-credential refusal.
func TestFetchContractAzure(t *testing.T) {
	remote := t.TempDir()
	runContractGit(t, remote, "init", "-q")
	runContractGit(t, remote, "config", "user.email", "contract@example.invalid")
	runContractGit(t, remote, "config", "user.name", "contract")
	if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runContractGit(t, remote, "add", "a.txt")
	runContractGit(t, remote, "commit", "-qm", "one")
	baseSHA := runContractGit(t, remote, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runContractGit(t, remote, "commit", "-qam", "two")
	headSHA := runContractGit(t, remote, "rev-parse", "HEAD")
	dir, home, env := contractSetup(t, []contractFixture{
		{"az-meta.json", `{"pullRequestId":16,"title":"PR sixteen","description":"body",` +
			`"targetRefName":"refs/heads/main","sourceRefName":"refs/heads/feat",` +
			`"lastMergeTargetCommit":{"commitId":"` + baseSHA + `"},"lastMergeSourceCommit":{"commitId":"` + headSHA + `"},` +
			`"createdBy":{"uniqueName":"mate"},"url":"https://example.invalid/16","status":"active","isDraft":false,` +
			`"repository":{"remoteUrl":"` + remote + `"}}`},
		{"az-threads.json", `[{"id":1}]`},
		{"az-changes.json", `[{"item":{"path":"/a.txt"},"changeType":"edit"}]`},
	}, map[string]string{
		"AZURE_DEVOPS_ORG":     "https://example.invalid",
		"AZURE_DEVOPS_EXT_PAT": "tok",
	})
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4716", true,
		"--provider", "azure", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	pr := contractPR(t, dir, "4716")
	if pr["provider"] != "azure" || pr["number"] != float64(16) {
		t.Fatalf("PR.json provider/number = %v/%v, want azure/16", pr["provider"], pr["number"])
	}
	dir2, home2, env2 := contractSetup(t, []contractFixture{
		{"az-meta.json", `{"pullRequestId":16}`},
	}, nil)
	code, stdout, _ = contractRun(t, dir2, home2, env2, "acme/app", "4716", true,
		"--provider", "azure", "--json")
	if code != 1 || contractErrCode(contractEnvelope(t, stdout)) != "auth-missing" {
		t.Fatalf("auth-missing exit = %d, want 1/auth-missing\n%s", code, stdout)
	}
}

// runContractGit runs git in dir, failing the test on error.
func runContractGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var out strings.Builder
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// TestFetchContractUpstreamDiffTooLarge pins the Go-owned over-file-count
// edge (audit 20/D1): an undecodable oversized upstream diff refuses typed
// upstream-diff-too-large with the range-narrowing hint, leaving full.diff
// empty.
func TestFetchContractUpstreamDiffTooLarge(t *testing.T) {
	dir, home, env := contractSetup(t, stubPRFixtures(), map[string]string{
		"GH_DIFF_FAIL":  "1",
		"GH_FAIL_MATCH": "api repos",
		"GH_FAIL_TEXT":  "HTTP 406: this diff is too large to be rendered",
	})
	code, stdout, _ := contractRun(t, dir, home, env, "acme/app", "4711", true, "--json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stdout)
	}
	doc := contractEnvelope(t, stdout)
	if contractErrCode(doc) != "upstream-diff-too-large" {
		t.Fatalf("code = %q, want upstream-diff-too-large\n%s", contractErrCode(doc), stdout)
	}
	msg, _ := doc["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "300") || !strings.Contains(msg, "--base-sha") {
		t.Fatalf("message names no limit/hint:\n%s", msg)
	}
	var hint string
	if _, after, ok := strings.Cut(msg, "\n"); ok {
		hint = after
	}
	if want := "try: re-fetch narrowed to a range via --base-sha/--since, or narrow with a paths filter, then re-run"; hint != want {
		t.Fatalf("hint = %q, want %q", hint, want)
	}
	if got := contractFullDiff(t, dir, "4711"); got != "" {
		t.Fatalf("full.diff = %q, want empty", got)
	}
}

// TestFetchContractGarbage pins the garbage-upstream edge (D4): an
// undecodable PR payload refuses typed fetch-failed.
func TestFetchContractGarbage(t *testing.T) {
	fixtures := stubPRFixtures()
	for i, f := range fixtures {
		if f.name == "pr.json" {
			fixtures[i].doc = `not json{{{`
		}
	}
	dir, home, env := contractSetup(t, fixtures, nil)
	code, stdout, _ := contractRun(t, dir, home, env, "acme/app", "4714", true, "--json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stdout)
	}
	if got := contractErrCode(contractEnvelope(t, stdout)); got != "fetch-failed" {
		t.Fatalf("code = %q, want fetch-failed\n%s", got, stdout)
	}
}

// TestFetchContractBudget pins the spend gate: an over-budget fetch
// refuses typed budget-exceeded, and --consent-over-budget proceeds while
// recording the receipt.
func TestFetchContractBudget(t *testing.T) {
	costed := stubPRFixtures()
	dir, home, env := contractSetup(t, costed, map[string]string{"REVIEW_BUDGET_USD": "0.50"})
	costPath := filepath.Join(dir, ".review", "1", "cost.json")
	if err := os.MkdirAll(filepath.Dir(costPath), 0o755); err != nil {
		t.Fatal(err)
	}
	costDoc := `[{"effort":"standard","est_usd":2.00,"models":[]},{"effort":"standard","est_usd":4.00,"models":[]}]`
	if err := os.WriteFile(costPath, []byte(costDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := contractRun(t, dir, home, env, "acme/app", "4705", true, "--json")
	if code != 1 || contractErrCode(contractEnvelope(t, stdout)) != "budget-exceeded" {
		t.Fatalf("over-budget exit = %d, want 1/budget-exceeded\n%s", code, stdout)
	}
	dir2, home2, env2 := contractSetup(t, costed, map[string]string{"REVIEW_BUDGET_USD": "0.50"})
	costPath2 := filepath.Join(dir2, ".review", "1", "cost.json")
	if err := os.MkdirAll(filepath.Dir(costPath2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(costPath2, []byte(costDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := contractRun(t, dir2, home2, env2, "acme/app", "4705", true,
		"--consent-over-budget", "--json")
	if code != 0 {
		t.Fatalf("consented exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir2, ".review", "4705", "budget.json")); err != nil {
		t.Fatalf("consented run wrote no budget.json receipt: %v", err)
	}
}

// TestFetchContractPaging pins the paged file listing past the hosting CLI
// cap (audit 18/D2): a 100-file listing pages to two files with a
// filesource.json ceiling note.
func TestFetchContractPaging(t *testing.T) {
	var files strings.Builder
	files.WriteString("[")
	for i := range 100 {
		if i > 0 {
			files.WriteString(",")
		}
		fmt.Fprintf(&files, `{"path":"f%02d.txt","additions":1,"deletions":1}`, i)
	}
	files.WriteString("]")
	dir, home, env := contractSetup(t, []contractFixture{
		{"pr.json", `{"state":"OPEN","headRefOid":"` + headA + `","baseRefOid":"` + headB + `",` +
			`"author":{"login":"teammate"},"files":` + files.String() + `,"additions":100,"deletions":100}`},
		{"full.diff", "diff --git a/f00.txt b/f00.txt\n--- a/f00.txt\n+++ b/f00.txt\n@@ -1 +1 @@\n-a\n+b\n"},
		{"stat.txt", "f00.txt (+1/-1)\n"},
		{"files-page-a.json", `[{"filename":"f00.txt","additions":1,"deletions":1},{"filename":"f101.txt","additions":2,"deletions":0}]`},
	}, nil)
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4712", true, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "4712", "filesource.json"))
	if err != nil {
		t.Fatalf("no filesource.json: %v", err)
	}
	var note map[string]any
	if err := json.Unmarshal(raw, &note); err != nil {
		t.Fatalf("filesource.json decodes: %v", err)
	}
	if note["source"] != "paged" || note["provider_ceiling"] != float64(3000) || note["count"] != float64(2) {
		t.Fatalf("filesource.json = %v, want paged/3000/2", note)
	}
	pr := contractPR(t, dir, "4712")
	filesOut, _ := pr["files"].([]any)
	if len(filesOut) != 2 {
		t.Fatalf("files = %d, want 2 (paged)", len(filesOut))
	}
	calls := 0
	for line := range strings.Lines(string(contractRead(t, filepath.Join(dir, ".gh-calls.log")))) {
		if strings.Contains(line, "/files") {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("files calls = %d, want 1", calls)
	}
}

// twoDigits renders i as two digits.
func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// contractRead reads a file, failing the test on error.
func contractRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestFetchContractRateLimit pins the secondary-rate-limit backoff
// (audit 21/D3): a 429 with a retry-after hint retries once and completes.
func TestFetchContractRateLimit(t *testing.T) {
	dir, home, env := contractSetup(t, stubPRFixtures(), map[string]string{
		"GH_RATELIMIT_MATCH": "pr view",
	})
	if err := os.WriteFile(filepath.Join(dir, ".gh-429-budget"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := contractRun(t, dir, home, env, "acme/app", "4713", true, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "transient GitHub error — retry 1/2, bundle untouched") {
		t.Fatalf("stderr names no retry:\n%s", stderr)
	}
	calls := 0
	for line := range strings.Lines(string(contractRead(t, filepath.Join(dir, ".gh-calls.log")))) {
		if strings.Contains(line, "pr view") && !strings.Contains(line, "--jq") {
			calls++
		}
	}
	if calls != 2 {
		t.Fatalf("pr view calls = %d, want 2 (one retry)", calls)
	}
}
