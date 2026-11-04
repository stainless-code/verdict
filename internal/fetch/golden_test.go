// Package fetch_test holds the golden contract for the Go fetch and
// fetch-local verbs: the shared harness runs the Go binary on hermetic
// fixtures and pins exit codes, stdout/stderr, every file written anywhere
// (bundle files, prompt dump payloads, cost receipts, chunk layouts),
// artifact presence, AND the exact provider calls the stubs receive against
// checked-in goldens. A per-package smart stub answers every provider shape
// deterministically per case via environment (parallel-safe: behavior rides
// on per-process env, logs land in per-run sandbox files).
//
// The refusal edges the goldens cannot express on their own stay pinned
// with fixed assertions on top:
//
//	G1  upstream over-file-count refusal: typed upstream-diff-too-large
//	    with the range-narrowing hint.
//	G2  paged file listing past the CLI cap: two paged files with a
//	    filesource.json ceiling note.
//	G3  secondary rate-limit backoff: a 429 with a retry-after hint
//	    retries once and completes.
//	G4  garbage provider payloads (undecodable PR.json): typed
//	    controls-unavailable (no base to read the controls from).
//
// Nonces are random, so prompt dumps additionally assert fence consistency
// inside a run, never a blanket normalization.
package fetch_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/testenv"
)

var harness *golden.Harness

// TestMain builds the Go binary once, installs the smart provider stubs
// over the harness stub dir, and runs the golden suite.
func TestMain(m *testing.M) {
	os.Exit(golden.RunMain(m, &harness, installStubs))
}

// installStubs replaces the harness's network-dead gh stub with the smart
// fetch stubs and adds the glab/cafe/curl/az provider stubs.
func installStubs(h *golden.Harness) error {
	for name, script := range map[string]string{
		"gh":   ghStub,
		"glab": glabStub,
		"cafe": cafeStub,
		"curl": curlStub,
		"az":   azStub,
	} {
		if err := h.WriteStub(name, script); err != nil {
			return err
		}
	}
	return nil
}

// ghStub answers every gh shape the fetch path uses, driven by per-case
// environment (FETCH_*), and logs argv into the sandbox's .gh-calls.log
// so the harness snapshot compares the exact calls byte for byte. Canned
// payloads live in $PWD/canned/ (identical for every run).
const ghStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.gh-calls.log\"\n" +
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
	"  *api\\ repos/*/contents/*) p=\"${2#repos/*/contents/}\"; f=\"$PWD/canned/contents/${p%%\\?*}\"\n" +
	"    if [ -f \"$f\" ]; then cat \"$f\"; exit 0; fi\n" +
	"    echo \"gh: Not Found (HTTP 404)\" >&2; exit 1;;\n" +
	"  *--jq*) cat \"$PWD/canned/stat.txt\" 2>/dev/null || printf ''; exit 0;;\n" +
	"  *pr\\ view*) if [ \"${GH_VIEW_FAIL:-0}\" = 1 ]; then echo \"gh: PR not found\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/pr.json\"; exit 0;;\n" +
	"  *pr\\ diff*) if [ \"${GH_DIFF_FAIL:-0}\" = 1 ]; then echo \"gh: diff unavailable\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *files*) cat \"$PWD\"/canned/files-page-*.json 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *api\\ graphql*) if [ \"${GH_THREADS_FAIL:-0}\" = 1 ]; then echo \"gh: threads down\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/gh-threads.json\" 2>/dev/null || printf '%s' '{\"data\":{\"repository\":{\"pullRequest\":{\"reviewThreads\":{\"pageInfo\":{\"hasNextPage\":false,\"endCursor\":null},\"nodes\":[]}}}}}'; exit 0;;\n" +
	"  *reviews*|*comments*) if [ \"${GH_THREADS_FAIL:-0}\" = 1 ]; then echo \"gh: threads down\" >&2; exit 1; fi\n" +
	"    printf '%s\\n' '[]'; exit 0;;\n" +
	"  *compare*) if [ \"${GH_COMPARE_FAIL:-0}\" = 1 ]; then echo \"gh: no common ancestor\" >&2; exit 1; fi\n" +
	"    cat \"$PWD/canned/inc.diff\"; exit 0;;\n" +
	"esac\necho \"evals-stub-gh: network disabled ($*)\" >&2\nexit 1\n"

// glabStub answers the GitLab CLI shapes from $PWD/canned/ files.
const glabStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.glab-calls.log\"\n" +
	"if [ -n \"${GLAB_FAIL_MATCH:-}\" ]; then case \"$*\" in *${GLAB_FAIL_MATCH}*)\n" +
	"  echo \"${GLAB_FAIL_TEXT:-glab stub failure}\" >&2; exit 1;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  *mr\\ view*) cat \"$PWD/canned/gl-meta.json\"; exit 0;;\n" +
	"  *--jq*) printf '%s\\n' \"${GLAB_PID:-11}\"; exit 0;;\n" +
	"  *diffs*) cat \"$PWD/canned/gl-changes.json\"; exit 0;;\n" +
	"  *changes*) cat \"$PWD/canned/gl-changes.json\"; exit 0;;\n" +
	"  *mr\\ diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  */notes*) cat \"$PWD/canned/gl-notes.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  */discussions*) cat \"$PWD/canned/gl-discussions.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"esac\necho \"glab-stub: unexpected ($*)\" >&2\nexit 1\n"

// cafeStub answers the GitCafe CLI shapes, with two-page review/comment
// reads selected by the --cursor flag.
const cafeStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.cafe-calls.log\"\n" +
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

// curlStub answers the forgejo/bitbucket HTTPS shapes from $PWD/canned/.
const curlStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.curl-calls.log\"\n" +
	"if [ -n \"${CURL_FAIL_MATCH:-}\" ]; then case \"$*\" in *${CURL_FAIL_MATCH}*)\n" +
	"  echo \"${CURL_FAIL_TEXT:-curl: (22) HTTP error}\" >&2; exit 22;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  */pulls/*\\.diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *diff_url*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  */pulls/*\\/files*) cat \"$PWD/canned/fj-files.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  */pulls/*\\/reviews\\/*\\/comments*) cat \"$PWD/canned/fj-comments.json\" 2>/dev/null || printf '%s' '[{\"id\":1}]'; exit 0;;\n" +
	"  */pulls/*\\/reviews*) cat \"$PWD/canned/fj-reviews.json\" 2>/dev/null || printf '%s' '[{\"id\":7}]'; exit 0;;\n" +
	"  */pulls/*) cat \"$PWD/canned/fj-meta.json\"; exit 0;;\n" +
	"  */diffstat*) case \"$*\" in *cursor2*) cat \"$PWD/canned/bb-page2.json\";; *) cat \"$PWD/canned/bb-page1.json\";; esac; exit 0;;\n" +
	"  */comments*) cat \"$PWD/canned/bb-comments.json\" 2>/dev/null || printf '{\"values\":[]}'; exit 0;;\n" +
	"  */diff*) cat \"$PWD/canned/full.diff\"; exit 0;;\n" +
	"  *pullrequests*) cat \"$PWD/canned/bb-meta.json\" 2>/dev/null || cat \"$PWD/canned/fj-meta.json\"; exit 0;;\n" +
	"esac\necho \"curl-stub: unexpected ($*)\" >&2\nexit 1\n"

// azStub answers the Azure CLI shapes from $PWD/canned/ files.
const azStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.az-calls.log\"\n" +
	"if [ -n \"${AZ_FAIL_MATCH:-}\" ]; then case \"$*\" in *${AZ_FAIL_MATCH}*)\n" +
	"  echo \"${AZ_FAIL_TEXT:-az stub failure}\" >&2; exit 1;; esac; fi\n" +
	"case \"$*\" in\n" +
	"  *pr\\ show*) cat \"$PWD/canned/az-meta.json\"; exit 0;;\n" +
	"  *get-access-token*) printf '%s\\n' \"${AZ_TOKEN:-canned-token}\"; exit 0;;\n" +
	"  *threads*) cat \"$PWD/canned/az-threads.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *iterations*changes*) cat \"$PWD/canned/az-changes.json\" 2>/dev/null || printf '[]'; exit 0;;\n" +
	"  *iterations*) printf '%s' '3'; exit 0;;\n" +
	"esac\necho \"az-stub: unexpected ($*)\" >&2\nexit 1\n"

// Bundle heads shared by the fixtures. headB is the PR base: the pinned
// empty commit seedBase creates in every sandbox, so the default controls
// resolution (git show <base>:<path>) finds a real base object.
const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "488310afbc29fb48fbd84cf16c89d3726158a444"
	// azureRemote is the https clone URL the azure fixture advertises: it
	// shares AZURE_DEVOPS_ORG's origin, and the case's git config rewrites
	// it to the local remote so the fetch stays hermetic.
	azureRemote = "https://example.invalid/r.git"
)

// writeCanned writes one canned provider payload, failing the test on error.
func writeCanned(t *testing.T, dir, name, doc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "canned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "canned", name), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// slashJSONPath renders a fixture path as a JSON string literal with forward
// slashes. Canned payload bytes are graded, so a Windows temp path must
// neither print backslashes nor break the JSON string (an unescaped `\U` made
// the azure meta undecodable and the merge-ref gate refused the PR).
func slashJSONPath(t *testing.T, p string) string {
	t.Helper()
	raw, err := json.Marshal(strings.ReplaceAll(p, `\`, "/"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestSlashJSONPath proves the fixture path rendering is a portable JSON
// string on every OS: Windows separators become slashes and the literal
// decodes back to the slash path the root scrub expects.
func TestSlashJSONPath(t *testing.T) {
	t.Parallel()
	got := slashJSONPath(t, `C:\tmp\remote`)
	if !json.Valid([]byte(got)) {
		t.Fatalf("invalid JSON literal %q", got)
	}
	var decoded string
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if want := "C:/tmp/remote"; decoded != want {
		t.Fatalf("decoded = %q, want %q", decoded, want)
	}
}

// writeContent plants the contents-API answer for one control path at the
// PR base (the body the stub serves for repos/.../contents/<path>).
func writeContent(t *testing.T, dir, path, content string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type": "file", "encoding": "base64", "size": len(content),
		"content": base64.StdEncoding.EncodeToString([]byte(content)) + "\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "canned/contents/"+path, string(body))
}

// readCanned reads one canned payload back, failing the test on error.
func readCanned(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "canned", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// stubPR crafts the standard canned GitHub PR payload plus diff and stat.
func stubPR(t *testing.T, dir string) {
	t.Helper()
	writeCanned(t, dir, "pr.json", `{"state":"OPEN","headRefOid":"`+headA+`","baseRefOid":"`+headB+`",`+
		`"author":{"login":"teammate"},"files":[{"path":"notes.txt","additions":3,"deletions":1}],`+
		`"additions":3,"deletions":1}`)
	writeCanned(t, dir, "full.diff", "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeCanned(t, dir, "stat.txt", "notes.txt (+3/-1)\n")
	writeCanned(t, dir, "gh-threads.json", `{"data":{"repository":{"pullRequest":{"reviewThreads":{`+
		`"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[`+
		`{"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"notes.txt","line":2,"startLine":null,"diffSide":"RIGHT",`+
		`"comments":{"totalCount":2,"nodes":[`+
		`{"id":"PRRC_1","databaseId":101,"author":{"__typename":"Bot","login":"verdict[bot]"},"body":"Consider clarifying this line.`+
		`\n\n<!-- verdict-finding sig=6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e -->","createdAt":"2026-11-01T10:00:00Z","url":"https://example.invalid/c/101"},`+
		`{"id":"PRRC_2","databaseId":102,"author":{"__typename":"User","login":"mate"},"body":"Done.","createdAt":"2026-11-01T11:00:00Z","url":"https://example.invalid/c/102"}]}},`+
		`{"id":"PRRT_2","isResolved":true,"isOutdated":true,"path":"notes.txt","line":null,"startLine":null,"diffSide":"RIGHT",`+
		`"comments":{"totalCount":1,"nodes":[`+
		`{"id":"PRRC_3","databaseId":103,"author":{"__typename":"User","login":"ana"},"body":"Old anchor.","createdAt":"2026-10-01T09:00:00Z","url":"https://example.invalid/c/103"}]}}`+
		`]}}}}}`)
	writeCanned(t, dir, "inc.diff", "diff --git a/notes.txt b/notes.txt\nindex 2222222..3333333 100644\n"+
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n+new line\n+newer line\n bottom\n")
}

// promptFiles plants the trusted prompt inputs and the PR base commit
// (carrying no controls) beside the sandbox root.
func promptFiles(t *testing.T, dir string) {
	t.Helper()
	writePrompts(t, dir)
	if got := seedBase(t, dir, nil); got != headB {
		t.Fatalf("empty base commit = %s, want pinned %s", got, headB)
	}
}

// seedBase makes the sandbox a git repo whose one commit tracks the given
// review controls (force-added past .gitignore) and returns that commit: the
// PR base the canned payloads name. Controls written to the worktree after
// this call are head edits the default resolution must not read.
func seedBase(t *testing.T, dir string, controls map[string]string) string {
	t.Helper()
	runGit(t, dir, "init", "-q", "-b", "main")
	for name, content := range controls {
		writeFile(t, dir, name, content)
	}
	if len(controls) > 0 {
		runGit(t, dir, "add", "-f", ".review")
	}
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "parity-base")
	return strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

// writePrompts plants the trusted prompt inputs beside the sandbox root.
func writePrompts(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "SKILL.md", "# verdict skill (parity fixture)\n")
	writeFile(t, dir, "prompts/system.md", "# system (parity fixture)\n")
	writeFile(t, dir, "prompts/review.md", "# review (parity fixture)\n")
	writeFile(t, dir, "prompts/diagrams.md", "# diagrams (parity fixture)\n")
}

// writeFile writes one sandbox file, creating parents, failing on error.
func writeFile(t *testing.T, dir, name, doc string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nonceRe matches the trust-boundary nonce line.
var nonceRe = regexp.MustCompile(`with NONCE replaced by this run's nonce: ([0-9a-f]{16})\.`)

// requireFenceNonce asserts one dump fences open and close with its nonce.
func requireFenceNonce(t *testing.T, side, dump, nonce string) {
	t.Helper()
	for _, fence := range []string{"<<<UNTRUSTED nonce=" + nonce, "<<<END UNTRUSTED nonce=" + nonce} {
		if strings.Count(dump, fence) < 1 {
			t.Fatalf("dump %s never fences with its nonce %q", side, nonce)
		}
	}
}

// gitSetup inits a deterministic git repo with one committed file and one
// dirty change, plus the trusted prompt inputs.
func gitSetup(files map[string]string) func(t *testing.T, dir, home string) {
	return func(t *testing.T, dir, _ string) {
		t.Helper()
		writePrompts(t, dir)
		runGit(t, dir, "init", "-q", "-b", "main")
		runGit(t, dir, "config", "user.email", "parity@example.invalid")
		runGit(t, dir, "config", "user.name", "parity")
		n := 0
		for name, content := range files {
			writeFile(t, dir, name, content)
			n++
		}
		if n == 0 {
			writeFile(t, dir, "base.txt", "base\n")
		}
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-qm", "parity-base")
	}
}

// runGit runs git in dir with the pinned identity, failing the test on
// error (setup must be deterministic across sides).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := gitCmd(t, dir, args...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// gitCmd builds a git command with the pinned environment: testenv.Base
// supplies the fixed git config, and the fixed identity/dates keep commit
// hashes reproducible without per-repo config calls.
func gitCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "parity",
		"GIT_AUTHOR_EMAIL":    "parity@example.invalid",
		"GIT_COMMITTER_NAME":  "parity",
		"GIT_COMMITTER_EMAIL": "parity@example.invalid",
		"GIT_AUTHOR_DATE":     "2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE":  "2026-01-01T00:00:00Z",
	})
	return cmd
}

// TestFetchGoldenGitHub proves byte parity across the GitHub fetch shapes:
// text and JSON, the incremental compare success and failure, the API
// diff fallback, degraded thread reads, and the usage/bad-provider
// refusals.
func TestFetchGoldenGitHub(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
	}
	golden.Pin(t, harness, "happy-text", setup, nil,
		"fetch", "acme/app", "4701")
	golden.Pin(t, harness, "happy-json", setup, nil,
		"fetch", "acme/app", "4701", "--json")
	golden.Pin(t, harness, "base-sha-json", setup, map[string]string{"GH_COMPARE_FAIL": "0"},
		"fetch", "acme/app", "4701", "--base-sha", headB[:7], "--json")
	golden.Pin(t, harness, "base-sha-note", setup, map[string]string{"GH_COMPARE_FAIL": "1"},
		"fetch", "acme/app", "4701", "--base-sha", headB[:7], "--json")
	golden.Pin(t, harness, "api-fallback", setup, map[string]string{"GH_DIFF_FAIL": "1"},
		"fetch", "acme/app", "4701", "--json")
	golden.Pin(t, harness, "threads-degraded", setup, map[string]string{"GH_THREADS_FAIL": "1"},
		"fetch", "acme/app", "4701", "--json")
	golden.Pin(t, harness, "view-failed", setup, map[string]string{"GH_VIEW_FAIL": "1"},
		"fetch", "acme/app", "4701", "--json")
	golden.Pin(t, harness, "since-alias", setup, nil,
		"fetch", "acme/app", "4701", "--since", headB[:7], "--json")
	golden.Pin(t, harness, "unknown-flag", setup, nil,
		"fetch", "acme/app", "4701", "--bogus")
	golden.Pin(t, harness, "bad-provider", setup, nil,
		"fetch", "acme/app", "4701", "--provider", "gitea")
	golden.Pin(t, harness, "provider-base-refused", setup, nil,
		"fetch", "acme/app", "4701", "--provider", "gitlab", "--base-sha", headB[:7])
	// The checkout lacks the base object (a shallow clone, or another repo):
	// the controls come from the contents API at the base commit, never from
	// the head worktree.
	baseAbsent := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
		writeCanned(t, dir, "pr.json", strings.ReplaceAll(readCanned(t, dir, "pr.json"), headB, strings.Repeat("c", 40)))
		writeFile(t, dir, ".review/panel", "head-panel\n")
		writeContent(t, dir, ".review/panel", "api-base-panel\n")
	}
	golden.Pin(t, harness, "controls-base-absent", baseAbsent, nil, "fetch", "acme/app", "4701", "--json")
	// Any API failure other than a 404 refuses: it is never read as absence.
	golden.Pin(t, harness, "controls-contents-error", baseAbsent,
		map[string]string{"GH_FAIL_MATCH": "contents", "GH_FAIL_TEXT": "gh: Server Error (HTTP 500)"},
		"fetch", "acme/app", "4701", "--json")
	golden.Pin(t, harness, "empty-diff", func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
		writeCanned(t, dir, "full.diff", "")
	}, nil, "fetch", "acme/app", "4701", "--json")
}

// TestFetchGoldenShowPrompts proves the prompt-dump parity by shape plus
// nonce consistency (nonces are random per run).
func TestFetchGoldenShowPrompts(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
	}
	for _, tc := range []dumpCase{
		{name: "dump-text", args: []string{"fetch", "acme/app", "4702"}},
		{name: "dump-json", args: []string{"fetch", "acme/app", "4702", "--json"}, json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runDumpCase(t, setup, tc)
		})
	}
}

// dumpCase is one --show-prompts comparison.
type dumpCase struct {
	name string
	args []string
	json bool
}

// runDumpCase runs one --show-prompts leg: the dump is pinned by golden
// (nonces scrubbed) and checked for nonce consistency (every fence opens
// and closes with this run's own nonce).
func runDumpCase(t *testing.T, setup func(t *testing.T, dir, home string), tc dumpCase) {
	t.Helper()
	args := append(append([]string{}, tc.args...), "--show-prompts")
	out := golden.Pin(t, harness, tc.name, setup, nil, args...)
	var dump string
	if tc.json {
		dump = dumpFromEnvelope(t, out.Stdout)
	} else {
		dump = out.Stdout
	}
	m := nonceRe.FindStringSubmatch(dump)
	if len(m) != 2 {
		t.Fatalf("dump carries no nonce line:\n%s", dump)
	}
	requireFenceNonce(t, "go", dump, m[1])
}

// dumpFromEnvelope extracts data.prompt_dump from a --json envelope.
func dumpFromEnvelope(t *testing.T, stdout string) string {
	t.Helper()
	doc := jsonDecode(t, stdout)
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope carries no data object:\n%s", stdout)
	}
	dump, ok := data["prompt_dump"].(string)
	if !ok || dump == "" {
		t.Fatalf("envelope carries no prompt_dump:\n%s", stdout)
	}
	return dump
}

// jsonDecode decodes a --json envelope stdout.
func jsonDecode(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.NewDecoder(strings.NewReader(stdout)).Decode(&doc); err != nil {
		t.Fatalf("stdout decodes: %v\n%s", err, stdout)
	}
	return doc
}

// bigDiff builds an over-cap diff of n files with ~40KB each.
func bigDiff(n int) string {
	var sb strings.Builder
	for i := range n {
		fmt.Fprintf(&sb, "diff --git a/f%02d.txt b/f%02d.txt\n--- a/f%02d.txt\n+++ b/f%02d.txt\n@@ -1 +1 @@\n-a\n+b\n", i, i, i, i)
		for j := range 900 {
			fmt.Fprintf(&sb, " padding line %04d for file %02d with enough bytes to grow the diff\n", j, i)
		}
	}
	return sb.String()
}

// TestFetchGoldenCapChunk proves the cap gate parity: the advisory refusal
// with envelope candidates, the --chunk and knob opt-ins with identical
// manifests, the degenerate single-file refusal, and cap overrides.
func TestFetchGoldenCapChunk(t *testing.T) {
	t.Parallel()
	overSetup := overCapSetup(nil)
	golden.Pin(t, harness, "over-cap-refusal", overSetup, nil,
		"fetch", "acme/app", "4703", "--json")
	chunked := golden.Pin(t, harness, "over-cap-chunk", overSetup, nil,
		"fetch", "acme/app", "4703", "--chunk", "--json")
	requireChunkBytes(t, chunked, bigDiff(14))
	golden.Pin(t, harness, "over-cap-chunk-text", overSetup, nil,
		"fetch", "acme/app", "4703", "--chunk")
	golden.Pin(t, harness, "over-cap-absent-text", overSetup, nil,
		"fetch", "acme/app", "4703")
	golden.Pin(t, harness, "over-cap-knob", overCapSetup(map[string]string{".review/chunk": "true\n"}), nil,
		"fetch", "acme/app", "4703", "--json")
	golden.Pin(t, harness, "cap-override", overSetup,
		map[string]string{"REVIEW_MAX_BYTES": "100000000"},
		"fetch", "acme/app", "4703", "--json")
	golden.Pin(t, harness, "cap-override-beats-chunk", overSetup,
		map[string]string{"REVIEW_MAX_BYTES": "100000000"},
		"fetch", "acme/app", "4703", "--chunk", "--json")
	golden.Pin(t, harness, "bad-max-bytes", overSetup,
		map[string]string{"REVIEW_MAX_BYTES": "nope"},
		"fetch", "acme/app", "4703", "--json")
	golden.Pin(t, harness, "bad-chunk-knob", overCapSetup(map[string]string{".review/chunk": "maybe\n"}), nil,
		"fetch", "acme/app", "4703", "--json")
	singleSetup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "pr.json", `{"state":"OPEN","headRefOid":"`+headA+`","baseRefOid":"`+headB+`",`+
			`"author":{"login":"teammate"},"files":[{"path":"huge.txt","additions":1,"deletions":1}],"additions":1,"deletions":1}`)
		var sb strings.Builder
		sb.WriteString("diff --git a/huge.txt b/huge.txt\n--- a/huge.txt\n+++ b/huge.txt\n@@ -1 +1 @@\n-a\n+b\n")
		for j := range 20000 {
			fmt.Fprintf(&sb, "padding line %05d with enough bytes to grow the single file\n", j)
		}
		writeCanned(t, dir, "full.diff", sb.String())
		writeCanned(t, dir, "stat.txt", "huge.txt (+1/-1)\n")
	}
	golden.Pin(t, harness, "single-file-degenerate", singleSetup, nil,
		"fetch", "acme/app", "4704", "--chunk", "--json")
}

// overCapSetup plants an over-cap PR whose base commit tracks controls: the
// diff is generated from bigDiff, so its bytes are reproducible from code and
// no golden needs to store the input twice.
func overCapSetup(controls map[string]string) func(t *testing.T, dir, home string) {
	return func(t *testing.T, dir, _ string) {
		t.Helper()
		writePrompts(t, dir)
		base := seedBase(t, dir, controls)
		writeCanned(t, dir, "pr.json", `{"state":"OPEN","headRefOid":"`+headA+`","baseRefOid":"`+base+`",`+
			`"author":{"login":"teammate"},"files":[{"path":"f00.txt","additions":1,"deletions":1}],"additions":1,"deletions":1}`)
		writeCanned(t, dir, "full.diff", bigDiff(14))
		writeCanned(t, dir, "stat.txt", "f00.txt (+1/-1)\n")
	}
}

// requireChunkBytes proves the digest format still grades the bytes: chunk
// bodies above the golden threshold are pinned as size+sha256, so the same run
// is also compared byte-for-byte against the generated diff it was cut from,
// including the copy the bundle keeps.
func requireChunkBytes(t *testing.T, out golden.Outputs, diff string) {
	t.Helper()
	if got := out.Files["C/.review/4703/full.diff"]; got != diff {
		t.Fatalf("bundle full.diff = %d bytes, want the fetched %d-byte diff", len(got), len(diff))
	}
	chunked := joinChunks(t, out.Files)
	if chunked != diff {
		t.Fatalf("chunks join to %d bytes, want the %d-byte diff", len(chunked), len(diff))
	}
}

// joinChunks returns the written chunk bodies in order, requiring at least one
// non-empty chunk.
func joinChunks(t *testing.T, files map[string]string) string {
	t.Helper()
	var joined strings.Builder
	for i := 1; ; i++ {
		body, ok := files[fmt.Sprintf("C/.review/4703/chunks/chunk-%02d.diff", i)]
		if !ok {
			break
		}
		if body == "" {
			t.Fatalf("chunk-%02d.diff is empty", i)
		}
		joined.WriteString(body)
	}
	if joined.Len() == 0 {
		t.Fatal("chunked fetch wrote no chunk files")
	}
	return joined.String()
}

// TestFetchGoldenBudget proves the cost-control plane parity: unset plane,
// within-budget, over-budget refusal, consented receipt, bad budget, and
// unreadable history.
func TestFetchGoldenBudget(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
	}
	costed := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
		writeFile(t, dir, ".review/1/cost.json",
			`[{"effort":"standard","est_usd":2.00,"models":[]},{"effort":"standard","est_usd":4.00,"models":[]}]`)
	}
	golden.Pin(t, harness, "budget-unset", setup, nil,
		"fetch", "acme/app", "4705", "--json")
	golden.Pin(t, harness, "budget-within", costed,
		map[string]string{"REVIEW_BUDGET_USD": "10"},
		"fetch", "acme/app", "4705", "--json")
	golden.Pin(t, harness, "budget-over-refused", costed,
		map[string]string{"REVIEW_BUDGET_USD": "0.50"},
		"fetch", "acme/app", "4705", "--json")
	golden.Pin(t, harness, "budget-over-consented", costed,
		map[string]string{"REVIEW_BUDGET_USD": "0.50"},
		"fetch", "acme/app", "4705", "--consent-over-budget", "--json")
	golden.Pin(t, harness, "budget-unknown", setup,
		map[string]string{"REVIEW_BUDGET_USD": "0.50"},
		"fetch", "acme/app", "4705", "--json")
	golden.Pin(t, harness, "bad-budget", setup,
		map[string]string{"REVIEW_BUDGET_USD": "free"},
		"fetch", "acme/app", "4705", "--json")
	golden.Pin(t, harness, "budget-unreadable", func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
		writeFile(t, dir, ".review/1/cost.json", `{"effort":"standard","est_usd":3.00}`)
		writeFile(t, dir, ".review/2/cost.json", `not json`)
	}, map[string]string{"REVIEW_BUDGET_USD": "10"},
		"fetch", "acme/app", "4705", "--json")
}

// TestFetchGoldenLocal proves fetch-local parity across every diff mode:
// worktree, staged, range (.. and ...), branch base, stash, paths filters,
// plus the slug/mode refusals and advisory warnings.
func TestFetchGoldenLocal(t *testing.T) {
	t.Parallel()
	base := gitSetup(map[string]string{"notes.txt": "top\nold line\nbottom\n"})
	golden.Pin(t, harness, "local-worktree", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
	}, nil, "fetch-local", "acme/app", "demo", "--json")
	golden.Pin(t, harness, "local-worktree-text", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
	}, nil, "fetch-local", "acme/app", "demo")
	golden.Pin(t, harness, "local-staged", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
		runGit(t, dir, "add", "notes.txt")
	}, nil, "fetch-local", "acme/app", "demo", "--staged", "--json")
	golden.Pin(t, harness, "local-range", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
		runGit(t, dir, "commit", "-qm", "second", "notes.txt")
	}, nil,
		"fetch-local", "acme/app", "demo", "--range", "HEAD~1..HEAD", "--json")
	golden.Pin(t, harness, "local-range3", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
		runGit(t, dir, "commit", "-qm", "second", "notes.txt")
	}, nil, "fetch-local", "acme/app", "demo", "--range", "HEAD~1...HEAD", "--json")
	golden.Pin(t, harness, "local-branch", func(t *testing.T, dir, _ string) {
		t.Helper()
		gitSetup(map[string]string{"notes.txt": "top\nold line\nbottom\n"})(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nfeature line\nbottom\n")
		runGit(t, dir, "commit", "-qm", "second", "notes.txt")
	}, nil, "fetch-local", "acme/app", "demo", "--base", "HEAD~1", "--json")
	golden.Pin(t, harness, "local-stash", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nstashed line\nbottom\n")
		runGit(t, dir, "stash", "-q")
	}, nil, "fetch-local", "acme/app", "demo", "--stash", "--json")
	golden.Pin(t, harness, "local-paths", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
		writeFile(t, dir, "other.txt", "changed\n")
		runGit(t, dir, "add", "other.txt")
		runGit(t, dir, "commit", "-qm", "other")
	}, nil, "fetch-local", "acme/app", "demo", "--json", "--", "notes.txt")
	golden.Pin(t, harness, "local-paths-empty", base, nil,
		"fetch-local", "acme/app", "demo", "--", "missing.txt", "--json")
	golden.Pin(t, harness, "local-untracked", func(t *testing.T, dir, _ string) {
		t.Helper()
		base(t, dir, "")
		writeFile(t, dir, "new1.txt", "untracked\n")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
	}, nil, "fetch-local", "acme/app", "demo", "--json")
	golden.Pin(t, harness, "local-slug-number", base, nil,
		"fetch-local", "acme/app", "4351")
	// local-slug-empty pins the typed refusal: `fetch-local acme/app .`
	// sanitizes to nothing and refuses bad-slug. The retired script agreed
	// on GNU sed but kept the dashes on macOS BSD sed — a platform-dependent
	// quirk, so a cross-implementation case could never pin it. The typed
	// refusal is the spec; the unit test pins the sanitizer itself.
	t.Run("local-slug-empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		home := filepath.Join(t.TempDir(), "home")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		base(t, dir, "")
		b := goldenRun(t, harness, "local-slug-empty", base, nil, "fetch-local", "acme/app", ".", "--json")
		if b.Status != 1 {
			t.Fatalf("exit = %d, want 1", b.Status)
		}
		if code(jsonDecode(t, b.Stdout)) != "bad-slug" {
			t.Fatalf("code = %s, want bad-slug", code(jsonDecode(t, b.Stdout)))
		}
	})
	golden.Pin(t, harness, "local-slug-prefix", base, nil,
		"fetch-local", "acme/app", "local-demo")
	golden.Pin(t, harness, "local-modes-exclusive", base, nil,
		"fetch-local", "acme/app", "demo", "--staged", "--range", "a..b")
	golden.Pin(t, harness, "local-bad-range", base, nil,
		"fetch-local", "acme/app", "demo", "--range", "nope..HEAD", "--json")
	golden.Pin(t, harness, "local-bad-base", base, nil,
		"fetch-local", "acme/app", "demo", "--base", "nope", "--json")
	golden.Pin(t, harness, "local-bad-stash", base, nil,
		"fetch-local", "acme/app", "demo", "--stash", "9", "--json")
	golden.Pin(t, harness, "local-unknown-flag", base, nil,
		"fetch-local", "acme/app", "demo", "--bogus")
	golden.Pin(t, harness, "local-not-a-repo", func(t *testing.T, dir, _ string) {
		t.Helper()
		writePrompts(t, dir)
	}, nil, "fetch-local", "acme/app", "demo", "--json")
}

// TestFetchGoldenLocalShowPrompts proves fetch-local's human-mode prompt
// dump parity, plain and chunked: the dump owns stdout, progress moves to
// stderr, and every fenced section matches past the per-run nonce.
func TestFetchGoldenLocalShowPrompts(t *testing.T) {
	t.Parallel()
	dirty := func(t *testing.T, dir, _ string) {
		t.Helper()
		gitSetup(map[string]string{"notes.txt": "top\nold line\nbottom\n"})(t, dir, "")
		writeFile(t, dir, "notes.txt", "top\nnew line\nbottom\n")
	}
	chunked := func(t *testing.T, dir, _ string) {
		t.Helper()
		base, changed := bigLocalPair()
		gitSetup(map[string]string{"a.txt": base, "b.txt": base, "c.txt": base})(t, dir, "")
		writeFile(t, dir, "a.txt", changed)
		writeFile(t, dir, "b.txt", changed)
		writeFile(t, dir, "c.txt", changed)
	}
	t.Run("local-dump-text", func(t *testing.T) {
		t.Parallel()
		runDumpCase(t, dirty, dumpCase{
			name: "local-dump-text",
			args: []string{"fetch-local", "acme/app", "demo"},
		})
	})
	t.Run("local-dump-chunk", func(t *testing.T) {
		t.Parallel()
		runDumpCase(t, chunked, dumpCase{
			name: "local-dump-chunk",
			args: []string{"fetch-local", "acme/app", "demo", "--chunk"},
		})
	})
}

// bigLocalPair builds one over-cap worktree content pair: three files whose
// +/- diff slices stay under the cap, so the chunk plan can slice by file.
func bigLocalPair() (base, changed string) {
	var b, c strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "base line %05d with enough padding to grow the diff\n", i)
		fmt.Fprintf(&c, "changed line %05d with enough padding to grow the diff\n", i)
	}
	return b.String(), c.String()
}

// TestFetchGoldenProviders proves the --provider backends byte parity: the
// normalized PR.json shape, the thread splits, the advisory manifests, and
// the auth/mode refusals.
func TestFetchGoldenProviders(t *testing.T) {
	t.Parallel()
	gitlab := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "gl-meta.json", `{"iid":5,"title":"MR five","description":"body","target_branch":"main",`+
			`"source_branch":"feat","diff_refs":{"base_sha":"`+headB+`","head_sha":"`+headA+`"},`+
			`"author":{"username":"mate"},"web_url":"https://example.invalid/5","state":"opened","draft":false}`)
		writeCanned(t, dir, "gl-changes.json", `{"changes":[{"old_path":"a.txt","new_path":"a.txt","new_file":false,`+
			`"deleted_file":false,"renamed_file":false}]}`)
		writeCanned(t, dir, "full.diff", "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n"+
			"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n")
		writeCanned(t, dir, "gl-notes.json", `[{"type":"DiffNote"},{"type":"Note"}]`)
		writeCanned(t, dir, "gl-discussions.json", `[{"id":"d1","notes":[`+
			`{"id":201,"body":"Please rename.","author":{"username":"ana"},"created_at":"2026-11-01T10:00:00Z",`+
			`"resolved":false,"resolvable":true,"position":{"position_type":"text","new_path":"a.txt","old_path":"a.txt",`+
			`"new_line":1,"old_line":null,"head_sha":"`+headA+`"}},`+
			`{"id":202,"body":"Fixed.","author":{"username":"verdict"},"created_at":"2026-11-01T11:00:00Z"},`+
			`{"id":203,"system":true,"body":"added label"}]},`+
			`{"id":"d2","notes":[{"id":204,"body":"Old note","author":{"username":"bo"},"created_at":"2026-10-01T09:00:00Z",`+
			`"resolved":true,"resolvable":true,"position":{"position_type":"text","new_path":"a.txt","old_path":"a.txt",`+
			`"new_line":1,"old_line":null,"head_sha":"`+headB+`"}}]},`+
			`{"id":"d3","notes":[{"id":205,"body":"LGTM","author":{"username":"rev"},"created_at":"2026-11-02T09:00:00Z"}]}]`)
	}
	golden.Pin(t, harness, "gitlab-happy", gitlab, nil,
		"fetch", "acme/app", "4706", "--provider", "gitlab", "--json")
	golden.Pin(t, harness, "gitlab-text", gitlab, nil,
		"fetch", "acme/app", "4706", "--provider", "gitlab")
	golden.Pin(t, harness, "gitlab-base-refused", gitlab, nil,
		"fetch", "acme/app", "4706", "--provider", "gitlab", "--base-sha", headB[:7])

	gitcafe := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "cafe-meta.json", `{"data":{"resource":{"number":7,"title":"PR seven",`+
			`"description":"body","targetBranch":"main","sourceBranch":"feat","observedBaseOid":"`+headB+`",`+
			`"sourceOid":"`+headA+`","author":{"handle":"mate"},"state":"open","draft":false},"url":"https://example.invalid/7"}}`)
		writeCanned(t, dir, "cafe-items.json", `[{"path":"a.txt","patch":"@@ -1 +1 @@\n-a\n+b\n",`+
			`"status":"modified","additions":1,"deletions":1}]`)
		writeCanned(t, dir, "cafe-rev1.json", `{"data":{"items":[{"id":"r1","body":"Summary note",`+
			`"author":{"handle":"rev"},"createdAt":"2026-11-01T00:00:00Z"}]}}`)
		writeCanned(t, dir, "cafe-cmt1.json", `{"data":{"items":[{"id":"c1","path":"a.txt","line":1,"side":"RIGHT",`+
			`"body":"Nit","author":{"handle":"ana"},"createdAt":"2026-11-01T00:00:00Z","resolved":true}]}}`)
	}
	// A cursor-less gitcafe page is a real provider shape: the call is made
	// unconditionally, and the golden pins the provider diff bytes verbatim
	// (no added or stripped trailing newline).
	golden.Pin(t, harness, "gitcafe-happy", gitcafe, nil,
		"fetch", "acme/app", "4707", "--provider", "gitcafe", "--json")

	forgejo := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "fj-meta.json", `{"index":9,"title":"PR nine","body":"body","base":{"label":"main",`+
			`"sha":"`+headB+`"},"head":{"label":"feat","sha":"`+headA+`"},"user":{"login":"mate"},`+
			`"html_url":"https://example.invalid/9","state":"open","diff_url":"https://example.invalid/9.diff"}`)
		writeCanned(t, dir, "full.diff", "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n"+
			"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n")
		writeCanned(t, dir, "fj-files.json", `[{"filename":"a.txt","status":"modified","additions":1,"deletions":1}]`)
		writeCanned(t, dir, "fj-reviews.json", `[{"id":7,"user":{"login":"rev"},"body":"Summary note",`+
			`"submitted_at":"2026-11-01T00:00:00Z","html_url":"https://example.invalid/r/7"}]`)
		writeCanned(t, dir, "fj-comments.json", `[{"id":11,"path":"a.txt","position":1,"original_position":1,`+
			`"body":"Nit","user":{"login":"ana"},"created_at":"2026-11-01T00:00:00Z","resolver":{"login":"ana"},`+
			`"html_url":"https://example.invalid/c/11"},`+
			`{"id":12,"path":"a.txt","position":0,"original_position":2,"body":"Old spot","user":{"login":"bo"},`+
			`"created_at":"2026-10-01T00:00:00Z","resolver":null,"html_url":"https://example.invalid/c/12"}]`)
	}
	golden.Pin(t, harness, "forgejo-happy", forgejo,
		map[string]string{"FORGEJO_HOST": "https://example.invalid", "FORGEJO_TOKEN": "tok"},
		"fetch", "acme/app", "4708", "--provider", "forgejo", "--json")
	golden.Pin(t, harness, "forgejo-auth-missing", forgejo, nil,
		"fetch", "acme/app", "4708", "--provider", "forgejo", "--json")

	bitbucket := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "bb-meta.json", `{"id":11,"title":"PR eleven","summary":{"raw":"body"},`+
			`"destination":{"branch":{"name":"main"},"commit":{"hash":"`+headB+`"}},`+
			`"source":{"branch":{"name":"feat"},"commit":{"hash":"`+headA+`"}},`+
			`"author":{"nickname":"mate"},"links":{"html":{"href":"https://example.invalid/11"}},`+
			`"state":"OPEN","draft":false}`)
		writeCanned(t, dir, "full.diff", "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n"+
			"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n")
		writeCanned(t, dir, "bb-page1.json", `{"values":[{"new":{"path":"a.txt"},"old":{"path":"a.txt"},`+
			`"status":"modified","lines_added":1,"lines_removed":1}],"next":null}`)
		writeCanned(t, dir, "bb-comments.json", `{"values":[`+
			`{"id":1,"content":{"raw":"General note"},"user":{"nickname":"rev"},"created_on":"2026-11-01T00:00:00Z",`+
			`"links":{"html":{"href":"https://example.invalid/c/1"}}},`+
			`{"id":2,"content":{"raw":"Inline nit"},"user":{"nickname":"ana"},"created_on":"2026-11-01T00:00:00Z",`+
			`"inline":{"path":"a.txt","to":1,"outdated":false},"resolution":{"user":{"nickname":"ana"}},`+
			`"links":{"html":{"href":"https://example.invalid/c/2"}}},`+
			`{"id":3,"parent":{"id":2},"content":{"raw":"Reply"},"user":{"nickname":"bo"},"created_on":"2026-11-01T01:00:00Z"},`+
			`{"id":4,"deleted":true,"content":{"raw":"gone"}},`+
			`{"id":5,"pending":true,"content":{"raw":"draft"}}],"next":null}`)
	}
	golden.Pin(t, harness, "bitbucket-happy", bitbucket,
		map[string]string{"BITBUCKET_TOKEN": "tok"},
		"fetch", "acme/app", "4709", "--provider", "bitbucket", "--json")
	golden.Pin(t, harness, "bitbucket-auth-missing", bitbucket, nil,
		"fetch", "acme/app", "4709", "--provider", "bitbucket", "--json")

	azure := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "az-meta.json", `{"pullRequestId":13,"title":"PR thirteen","description":"body",`+
			`"targetRefName":"refs/heads/main","sourceRefName":"refs/heads/feat",`+
			`"lastMergeTargetCommit":{"commitId":"`+headB+`"},"lastMergeSourceCommit":{"commitId":"`+headA+`"},`+
			`"createdBy":{"uniqueName":"mate"},"url":"https://example.invalid/13","status":"active","isDraft":false,`+
			`"repository":{"remoteUrl":"https://example.invalid/r.git"}}`)
		writeCanned(t, dir, "az-threads.json", `[`+
			`{"id":9,"status":"fixed","threadContext":{"filePath":"/a.txt","rightFileStart":{"line":1},"rightFileEnd":{"line":1}},`+
			`"comments":[{"id":1,"content":"system","commentType":"system","publishedDate":"2026-01-01T00:00:00Z"},`+
			`{"id":2,"content":"Nit","author":{"displayName":"ana"},"publishedDate":"2026-11-01T00:00:00Z"}]},`+
			`{"id":10,"status":"active","comments":[{"id":3,"content":"Question","author":{"displayName":"bo"},`+
			`"publishedDate":"2026-11-01T00:00:00Z"}]},`+
			`{"id":11,"isDeleted":true,"comments":[{"id":4,"content":"gone","publishedDate":"2026-11-01T00:00:00Z"}]}]`)
		writeCanned(t, dir, "az-changes.json", `[]`)
	}
	golden.Pin(t, harness, "azure-auth-missing", azure, nil,
		"fetch", "acme/app", "4710", "--provider", "azure", "--json")
}

// goldenRun runs the Go binary in a fresh sandbox, pins the scrubbed result
// against the checked-in golden, and returns the outputs for extra
// assertions (setup writes identical fixtures per run).
func goldenRun(t *testing.T, h *golden.Harness, name string, setup func(t *testing.T, dir, home string), extraEnv map[string]string, args ...string) golden.Outputs {
	t.Helper()
	return golden.Pin(t, h, name, setup, extraEnv, args...)
}

// TestFetchGoldenUpstreamTooLarge pins the over-file-count refusal: both
// diff reads fail with the host's own refusal text, so the fetch refuses
// typed upstream-diff-too-large with the range-narrowing hint.
func TestFetchGoldenUpstreamTooLarge(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
	}
	env := map[string]string{"GH_DIFF_FAIL": "1", "GH_FAIL_MATCH": "api repos", "GH_FAIL_TEXT": "HTTP 406: this diff is too large to be rendered"}
	b := goldenRun(t, harness, "upstream-too-large", setup, env, "fetch", "acme/app", "4711", "--json")
	if b.Status != 1 {
		t.Fatalf("exit = %d, want 1", b.Status)
	}
	db := jsonDecode(t, b.Stdout)
	if code(db) != "upstream-diff-too-large" {
		t.Fatalf("code = %v, want upstream-diff-too-large", code(db))
	}
	if msg := message(db); !strings.Contains(msg, "300") || !strings.Contains(msg, "--base-sha") {
		t.Fatalf("go message names no limit/hint: %q", msg)
	}
	if hint(db) != "try: re-fetch narrowed to a range via --base-sha/--since, or narrow with a paths filter, then re-run" {
		t.Fatalf("go hint = %q", hint(db))
	}
	// The truncated upstream leaves full.diff empty.
	requireEmptyFullDiff(t, b)
}

// requireEmptyFullDiff asserts the leg left an empty full.diff.
func requireEmptyFullDiff(t *testing.T, outs golden.Outputs) {
	t.Helper()
	requireSideFullDiff(t, "go", outs)
}

// requireSideFullDiff asserts one leg left an empty full.diff.
func requireSideFullDiff(t *testing.T, side string, outs golden.Outputs) {
	t.Helper()
	for k, v := range outs.Files {
		if !strings.HasSuffix(k, ".review/4711/full.diff") {
			continue
		}
		if v != "" {
			t.Fatalf("%s full.diff = %q, want empty", side, v)
		}
		return
	}
	t.Fatalf("%s wrote no full.diff", side)
}

// code reads the envelope error code.
func code(doc map[string]any) string {
	if e, ok := doc["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

// message reads the envelope error message.
func message(doc map[string]any) string {
	if e, ok := doc["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	return ""
}

// hint reads the trailing try-line of the envelope error message.
func hint(doc map[string]any) string {
	msg := message(doc)
	if _, after, ok := strings.CutLast(msg, "\n"); ok {
		return after
	}
	return ""
}

// TestFetchGoldenFilePaging pins the paged file listing: a file list
// arriving exactly at the CLI cap pages the provider listing and writes
// filesource.json instead of keeping the truncated list.
func TestFetchGoldenFilePaging(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "pr.json", `{"state":"OPEN","headRefOid":"`+headA+`","baseRefOid":"`+headB+`",`+
			`"author":{"login":"teammate"},"files":`+pagingFiles()+`,"additions":100,"deletions":100}`)
		writeCanned(t, dir, "full.diff", "diff --git a/f00.txt b/f00.txt\n--- a/f00.txt\n+++ b/f00.txt\n@@ -1 +1 @@\n-a\n+b\n")
		writeCanned(t, dir, "stat.txt", "f00.txt (+1/-1)\n")
		items := `[{"filename":"f00.txt","additions":1,"deletions":1},{"filename":"f101.txt","additions":2,"deletions":0}]`
		writeCanned(t, dir, "files-page-a.json", items)
	}
	b := goldenRun(t, harness, "file-paging", setup, nil, "fetch", "acme/app", "4712", "--json")
	if b.Status != 0 {
		t.Fatalf("exit = %d, want 0\n%s", b.Status, b.Stderr)
	}
	db := jsonDecode(t, b.Stdout)
	if n := dataFiles(db); n != float64(2) {
		t.Fatalf("files = %v, want 2 (paged)", n)
	}
	// The Go side pages the listing and notes the paged ceiling.
	requirePagedNote(t, b.Files)
	// Paging speaks exactly one files call past the standard reads.
	if calls := countFilesCalls(b.Files); calls != 1 {
		t.Fatalf("go files calls = %d, want 1", calls)
	}
}

// pagingFiles renders the 100-file PR.json file array.
func pagingFiles() string {
	var files strings.Builder
	files.WriteString("[")
	for i := range 100 {
		if i > 0 {
			files.WriteString(",")
		}
		fmt.Fprintf(&files, `{"path":"f%02d.txt","additions":1,"deletions":1}`, i)
	}
	files.WriteString("]")
	return files.String()
}

// requirePagedNote asserts the Go leg noted the paged ceiling.
func requirePagedNote(t *testing.T, files map[string]string) {
	t.Helper()
	found := false
	for k, v := range files {
		if !strings.HasSuffix(k, "filesource.json") {
			continue
		}
		found = true
		checkPagedNote(t, v)
	}
	if !found {
		t.Fatal("go wrote no filesource.json")
	}
}

// checkPagedNote asserts one ceiling note names the paged source.
func checkPagedNote(t *testing.T, v string) {
	t.Helper()
	var note map[string]any
	if err := json.Unmarshal([]byte(v), &note); err != nil {
		t.Fatalf("filesource.json decodes: %v", err)
	}
	if note["source"] != "paged" || note["provider_ceiling"] != float64(3000) || note["count"] != float64(2) {
		t.Fatalf("filesource.json = %v", note)
	}
}

// countFilesCalls counts the provider files calls in the call log.
func countFilesCalls(files map[string]string) int {
	calls := 0
	for k, v := range files {
		if !strings.HasSuffix(k, ".gh-calls.log") {
			continue
		}
		calls += countMatchingLines(v, "/files")
	}
	return calls
}

// countMatchingLines counts the lines containing a fragment.
func countMatchingLines(v, fragment string) int {
	calls := 0
	for line := range strings.SplitSeq(v, "\n") {
		if strings.Contains(line, fragment) {
			calls++
		}
	}
	return calls
}

// dataFiles reads the envelope file count.
func dataFiles(doc map[string]any) any {
	if d, ok := doc["data"].(map[string]any); ok {
		return d["files"]
	}
	return nil
}

// TestFetchGoldenRateLimitRetry pins the transient 429: with a retry-after
// hint the fetch waits once and completes instead of failing.
func TestFetchGoldenRateLimitRetry(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		stubPR(t, dir)
	}
	env := map[string]string{"GH_RATELIMIT_MATCH": "pr view"}
	budgetSetup := func(t *testing.T, dir, _ string) {
		t.Helper()
		setup(t, dir, "")
		writeFile(t, dir, ".gh-429-budget", "1")
	}
	b := goldenRun(t, harness, "ratelimit-retry", budgetSetup, env, "fetch", "acme/app", "4713", "--json")
	if b.Status != 0 {
		t.Fatalf("exit = %d, want 0\n%s", b.Status, b.Stderr)
	}
	if !strings.Contains(b.Stderr, "transient GitHub error — retry 1/2, bundle untouched") {
		t.Fatalf("go stderr names no retry:\n%s", b.Stderr)
	}
	if calls := countPrViewCalls(b.Files); calls != 2 {
		t.Fatalf("go pr view calls = %d, want 2 (one retry)", calls)
	}
}

// countPrViewCalls counts the non-jq pr view calls in the call log.
func countPrViewCalls(files map[string]string) int {
	calls := 0
	for k, v := range files {
		if strings.HasSuffix(k, ".gh-calls.log") {
			calls += countPrViewLines(v)
		}
	}
	return calls
}

// countPrViewLines counts the pr view calls in one call log.
func countPrViewLines(log string) int {
	calls := 0
	for line := range strings.SplitSeq(log, "\n") {
		if isPrViewCall(line) {
			calls++
		}
	}
	return calls
}

// isPrViewCall reports a non-jq pr view call-log line.
func isPrViewCall(line string) bool {
	return strings.Contains(line, "pr view") && !strings.Contains(line, "--jq")
}

// TestFetchGoldenUndecodablePR pins the garbage PR read: undecodable bytes
// refuse typed fetch-failed with one envelope.
func TestFetchGoldenUndecodablePR(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "pr.json", `not json{{{`)
		writeCanned(t, dir, "full.diff", "diff --git a/a b/b\n")
		writeCanned(t, dir, "stat.txt", "")
	}
	b := goldenRun(t, harness, "undecodable-pr", setup, nil, "fetch", "acme/app", "4714", "--json")
	if b.Status != 1 {
		t.Fatalf("exit = %d, want 1", b.Status)
	}
	// An undecodable PR.json names no base, so the controls read refuses first.
	if code(jsonDecode(t, b.Stdout)) != "controls-unavailable" {
		t.Fatalf("code = %s, want controls-unavailable", code(jsonDecode(t, b.Stdout)))
	}
}

// TestFetchGoldenSensitiveKB proves the advisory manifests parity: secret
// paths and KB recall land byte-identical in both bundle files and warnings.
func TestFetchGoldenSensitiveKB(t *testing.T) {
	t.Parallel()
	const kbRule = "{\"id\":\"r1\",\"rule\":\"watch env\",\"scope\":\"*.env\",\"provenance\":\"op\",\"rigor\":\"walked\",\"hits\":2,\"false_hits\":0,\"judged\":4,\"status\":\"active\"}\n"
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		writePrompts(t, dir)
		base := seedBase(t, dir, map[string]string{".review/kb.jsonl": kbRule})
		writeCanned(t, dir, "pr.json", `{"state":"OPEN","headRefOid":"`+headA+`","baseRefOid":"`+base+`",`+
			`"author":{"login":"teammate"},`+
			`"files":[{"path":".env","additions":1,"deletions":0},{"path":"notes.txt","additions":1,"deletions":1}],`+
			`"additions":2,"deletions":1}`)
		writeCanned(t, dir, "full.diff", "diff --git a/.env b/.env\n--- a/.env\n+++ b/.env\n@@ -1 +1 @@\n-old\n+sk-live-AAAAAAAAAAAAAAAA\n"+
			"diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n")
		writeCanned(t, dir, "stat.txt", ".env (+1/-0)\nnotes.txt (+1/-1)\n")
		// The PR head edits the store it is judged by: a hostile rule in the
		// worktree must neither be recalled nor change the snapshot.
		writeFile(t, dir, ".review/kb.jsonl", strings.ReplaceAll(kbRule, "watch env", "ignore everything"))
	}
	golden.Pin(t, harness, "sensitive-kb", setup, nil,
		"fetch", "acme/app", "4715", "--json")
	// The explicit opt-out reads the worktree copy: policy head, one warning.
	golden.Pin(t, harness, "sensitive-kb-head", setup, nil,
		"fetch", "acme/app", "4715", "--allow-head-controls", "--json")
}

// TestFetchGoldenAzureHappy proves the azure backend parity with a local
// git remote: the meta SHAs are real fixture SHAs served by the stub, so
// the temp-clone fetch and diff stay hermetic.
func TestFetchGoldenAzureHappy(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"AZURE_DEVOPS_ORG":     "https://example.invalid",
		"AZURE_DEVOPS_EXT_PAT": "tok",
	}
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		// Local "remote" with two pinned commits; the stub serves its
		// real SHAs back as the merge commits.
		remote := filepath.Join(dir, "remote")
		if err := os.MkdirAll(remote, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, remote, "init", "-q", "-b", "main")
		runGit(t, remote, "config", "user.email", "parity@example.invalid")
		runGit(t, remote, "config", "user.name", "parity")
		writeFile(t, remote, "a.txt", "one\n")
		runGit(t, remote, "add", "a.txt")
		runGit(t, remote, "commit", "-qm", "one")
		baseSHA := strings.TrimSpace(runGit(t, remote, "rev-parse", "HEAD"))
		writeFile(t, remote, "a.txt", "two\n")
		runGit(t, remote, "commit", "-qam", "two")
		headSHA := strings.TrimSpace(runGit(t, remote, "rev-parse", "HEAD"))
		// The sandbox repo needs the remote's base object for the controls read.
		runGit(t, dir, "fetch", "-q", remote, "HEAD")
		// The response names the real https clone URL (same origin as
		// AZURE_DEVOPS_ORG); a git URL rewrite points that URL at the local
		// fixture so the fetch stays hermetic and the origin check runs.
		localURL := "file://" + filepath.ToSlash(remote)
		if !strings.HasPrefix(filepath.ToSlash(remote), "/") {
			localURL = "file:///" + filepath.ToSlash(remote) // Windows drive path
		}
		env["GIT_CONFIG_COUNT"] = "1"
		env["GIT_CONFIG_KEY_0"] = "url." + localURL + ".insteadOf"
		env["GIT_CONFIG_VALUE_0"] = azureRemote
		writeCanned(t, dir, "az-meta.json", `{"pullRequestId":16,"title":"PR sixteen","description":"body",`+
			`"targetRefName":"refs/heads/main","sourceRefName":"refs/heads/feat",`+
			`"lastMergeTargetCommit":{"commitId":"`+baseSHA+`"},"lastMergeSourceCommit":{"commitId":"`+headSHA+`"},`+
			`"createdBy":{"uniqueName":"mate"},"url":"https://example.invalid/16","status":"active","isDraft":false,`+
			`"repository":{"remoteUrl":"`+azureRemote+`"}}`)
		writeCanned(t, dir, "az-threads.json", `[`+
			`{"id":9,"status":"fixed","threadContext":{"filePath":"/a.txt","rightFileStart":{"line":1},"rightFileEnd":{"line":1}},`+
			`"comments":[{"id":1,"content":"system","commentType":"system","publishedDate":"2026-01-01T00:00:00Z"},`+
			`{"id":2,"content":"Nit","author":{"displayName":"ana"},"publishedDate":"2026-11-01T00:00:00Z"}]},`+
			`{"id":10,"status":"active","comments":[{"id":3,"content":"Question","author":{"displayName":"bo"},`+
			`"publishedDate":"2026-11-01T00:00:00Z"}]}]`)
		writeCanned(t, dir, "az-changes.json", `[{"item":{"path":"/a.txt"},"changeType":"edit"}]`)
	}
	golden.Pin(t, harness, "azure-happy", setup, env,
		"fetch", "acme/app", "4716", "--provider", "azure", "--json")
}

// TestFetchGoldenBitbucketPaging proves the bitbucket diffstat pager: two
// stub pages concatenate into one file list.
func TestFetchGoldenBitbucketPaging(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, dir, _ string) {
		t.Helper()
		promptFiles(t, dir)
		writeCanned(t, dir, "bb-meta.json", `{"id":17,"title":"PR seventeen","summary":{"raw":"body"},`+
			`"destination":{"branch":{"name":"main"},"commit":{"hash":"`+headB+`"}},`+
			`"source":{"branch":{"name":"feat"},"commit":{"hash":"`+headA+`"}},`+
			`"author":{"nickname":"mate"},"links":{"html":{"href":"https://example.invalid/17"}},`+
			`"state":"OPEN","draft":false}`)
		writeCanned(t, dir, "full.diff", "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"+
			"diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-c\n+d\n")
		writeCanned(t, dir, "bb-page1.json", `{"values":[{"new":{"path":"a.txt"},"old":{"path":"a.txt"},`+
			`"status":"modified","lines_added":1,"lines_removed":1}],"next":"https://api.bitbucket.org/2.0/repos/acme/app/pullrequests/4717/diffstat?cursor=cursor2"}`)
		// The stub serves page2 only when the URL carries cursor2.
		writeCanned(t, dir, "bb-page2.json", `{"values":[{"new":{"path":"b.txt"},"old":{"path":"b.txt"},`+
			`"status":"modified","lines_added":1,"lines_removed":0}],"next":null}`)
		writeCanned(t, dir, "bb-comments.json", `{"values":[]}`)
	}
	golden.Pin(t, harness, "bitbucket-paging", setup,
		map[string]string{"BITBUCKET_TOKEN": "tok"},
		"fetch", "acme/app", "4717", "--provider", "bitbucket", "--json")
}
