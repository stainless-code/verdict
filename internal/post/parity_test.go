// Package post_test holds the shell-parity proof for the Go post verb: the
// shared harness runs BOTH scripts/review.sh post and the Go binary on
// identical fixtures and compares exit codes, stdout/stderr (timestamps
// scrubbed only), every file written anywhere (bundle stores, probe err
// files, the post receipt, the duplicate marker), artifact presence, AND the
// exact gh calls (argv plus request bodies) the stub receives (R6). A
// per-package smart gh stub answers the read calls and the reviews POST
// deterministically per case via environment (parallel-safe: behavior rides
// on per-process env, logs land in per-side sandbox files).
package post_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once, installs the smart gh stub over the
// harness stub dir, and runs the parity suite.
func TestMain(m *testing.M) {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	harness = h
	if err := installSmartStub(harness.StubBin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		harness.Teardown()
		os.Exit(2)
	}
	code := m.Run()
	harness.Teardown()
	os.Exit(code)
}

// smartStub answers every gh shape the post path uses, driven by per-case
// environment (GH_POST_LIVE, GH_POST_ME, GH_POST_RECHECK, GH_POST_MODE),
// and logs argv plus POST request bodies into the sandbox's .gh-calls.log
// so the harness snapshot compares them byte for byte. Outputs end with a
// newline like real gh responses.
const smartStub = "#!/usr/bin/env bash\necho \"CALL: $*\" >> \"$PWD/.gh-calls.log\"\n" +
	"if [ \"${1:-}\" = \"api\" ] && [ \"${2:-}\" = \"graphql\" ]; then\n" +
	"  case \"${GH_POST_RECHECK:-fail}\" in\n" +
	"    clear) printf '%s\\n' '{\"data\":{\"repository\":{\"pullRequest\":{\"reviewThreads\":{\"nodes\":[]}}}}}'; exit 0;;\n" +
	"    open) printf '%s\\n' '{\"data\":{\"repository\":{\"pullRequest\":{\"reviewThreads\":{\"nodes\":[{\"isResolved\":false},{\"isResolved\":false},{\"isResolved\":true}]}}}}}'; exit 0;;\n" +
	"    *) echo \"evals-stub-gh: threads unreachable\" >&2; exit 1;;\n" +
	"  esac\n" +
	"fi\n" +
	"case \"$*\" in\n" +
	"  *reviews*)\n" +
	"    cat >> \"$PWD/.gh-calls.log\"\n" +
	"    echo \"\" >> \"$PWD/.gh-calls.log\"\n" +
	"    case \"${GH_POST_MODE:-ok}\" in\n" +
	"      fail) echo \"evals-stub-gh: POST rejected\" >&2; exit 1;;\n" +
	"      garbage) printf '%s' 'not json{{{'; exit 0;;\n" +
	"      empty) printf ''; exit 0;;\n" +
	"    esac\n" +
	"    printf '%s\\n' '{\"id\":101,\"state\":\"commented\",\"html_url\":\"https://example.invalid/r/101\",\"submitted_at\":\"2026-01-01T00:00:00Z\"}'\n" +
	"    exit 0;;\n" +
	"  *pulls*)\n" +
	"    if [ \"${GH_POST_LIVE:-ok}\" = \"fail\" ]; then echo \"evals-stub-gh: live lookup failed\" >&2; exit 1; fi\n" +
	"    printf '%s\\n' \"${GH_POST_LIVE:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}\"; exit 0;;\n" +
	"  *user*)\n" +
	"    if [ \"${GH_POST_ME:-ok}\" = \"fail\" ]; then echo \"evals-stub-gh: auth failed\" >&2; exit 1; fi\n" +
	"    printf '%s\\n' \"${GH_POST_ME:-someone-else}\"; exit 0;;\n" +
	"esac\necho \"evals-stub-gh: network disabled ($*)\" >&2\nexit 1\n"

// installSmartStub replaces the harness's network-dead gh stub with the
// smart post stub.
func installSmartStub(stubBin string) error {
	return os.WriteFile(filepath.Join(stubBin, "gh"), []byte(smartStub), 0o755)
}

// Bundle heads shared by the fixtures.
const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// mkPR crafts a GitHub-shaped PR bundle: OPEN state on the fixture head and
// a one-hunk diff anchoring notes.txt lines 1-3.
func mkPR(t *testing.T, dir, slug string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	pr := `{"state":"OPEN","headRefOid":"` + headA + `","baseRefOid":"` + headB + `","author":{"login":"teammate"}}`
	if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(pr), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePostJSON writes the bundle's post.json record.
func writePostJSON(t *testing.T, dir, slug, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".review", slug, "post.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeBundleFile writes one bundle file, failing the test on error.
func writeBundleFile(t *testing.T, dir, slug, name, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".review", slug, name), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkLocalBundle crafts a local-* bundle with source and diff.
func mkLocalBundle(t *testing.T, dir, slug string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBundleFile(t, dir, slug, "source.json", `{"head_sha":"`+headA+`"}`)
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	writeBundleFile(t, dir, slug, "full.diff", diff)
}

// writeReview writes the bundle's review.json input.
func writeReview(t *testing.T, dir, slug, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".review", slug, "review.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cleanReview is a non-binding review carrying the given body and comments
// JSON (comments defaults to []).
func cleanReview(body, comments string) string {
	if comments == "" {
		comments = "[]"
	}
	return `{"schema_version":"1","body":` + jsonString(body) + `,"approved":false,"comments":` + comments + `}`
}

// jsonString renders a JSON string literal for fixture bodies.
func jsonString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// commentOn builds one review comment on notes.txt.
func commentOn(line, side, body string) string {
	return `{"path":"notes.txt","line":` + line + `,"side":"` + side + `","body":` + jsonString(body) + `,"rigor":"walked"}`
}

// setupClean builds the standard COMMENT bundle (slug 4351).
func setupClean(t *testing.T, dir, _ string) {
	t.Helper()
	mkPR(t, dir, "4351")
	writeReview(t, dir, "4351", cleanReview("> ✅ Looks good — minor note below.", "["+commentOn("2", "RIGHT", "Tighten this bound.")+"]"))
}

// TestPostParityHappy proves byte parity across the success shapes: COMMENT
// and APPROVE posts, dry runs, binding, rechecks, drift, and authorship.
func TestPostParityHappy(t *testing.T) {
	t.Parallel()
	harness.RunBoth(t, "comment-json", setupClean, nil, false,
		"post", "acme/app", "4351", "--json")
	harness.RunBoth(t, "comment-text", setupClean, nil, false,
		"post", "acme/app", "4351")
	harness.RunBoth(t, "dry-run-json", setupClean, nil, false,
		"post", "acme/app", "4351", "--dry-run", "--json")
	harness.RunBoth(t, "dry-run-text", setupClean, nil, false,
		"post", "acme/app", "4351", "--dry-run")

	approve := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4352")
		writeReview(t, dir, "4352", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	}
	harness.RunBoth(t, "approve-clear", approve,
		map[string]string{"GH_POST_RECHECK": "clear"}, false,
		"post", "acme/app", "4352", "--bind", "--json")
	harness.RunBoth(t, "approve-text", approve,
		map[string]string{"GH_POST_RECHECK": "clear"}, false,
		"post", "acme/app", "4352", "--bind")
	harness.RunBoth(t, "approve-unbound", approve, nil, false,
		"post", "acme/app", "4352", "--json")

	requestChanges := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4353")
		writeReview(t, dir, "4353", cleanReview("> [!CAUTION] Blocking issue below.", "["+commentOn("2", "RIGHT", "This line is wrong.")+"]"))
	}
	harness.RunBoth(t, "request-changes-bind", requestChanges, nil, false,
		"post", "acme/app", "4353", "--request-changes", "--bind", "--json")
	harness.RunBoth(t, "request-changes-dry-run", requestChanges, nil, false,
		"post", "acme/app", "4353", "--request-changes", "--bind", "--dry-run", "--json")

	harness.RunBoth(t, "unbound-downgrade", requestChanges, nil, false,
		"post", "acme/app", "4353", "--request-changes", "--json")
}

// TestPostParityGuards proves the approve-blocked gates (opens and live)
// with --force overrides.
func TestPostParityGuards(t *testing.T) {
	t.Parallel()
	runBlockedCases(t)
	runRecheckCases(t)
	runFinalizerCases(t)
}

// runBlockedCases grades approve-blocked on open and deferred findings.
func runBlockedCases(t *testing.T) {
	t.Helper()
	blocked := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4354")
		writeReview(t, dir, "4354", cleanReview("> ✅ Ship it, maybe.", "["+commentOn("2", "RIGHT", "Open thread here.")+"]"))
	}
	harness.RunBoth(t, "blocked-opens", blocked, nil, false,
		"post", "acme/app", "4354", "--bind", "--approve=true", "--json")
	harness.RunBoth(t, "blocked-opens-dry-run", blocked, nil, false,
		"post", "acme/app", "4354", "--bind", "--approve=true", "--dry-run", "--json")
	harness.RunBoth(t, "force-overrides", blocked,
		map[string]string{"GH_POST_RECHECK": "clear"}, false,
		"post", "acme/app", "4354", "--bind", "--approve=true", "--force", "--json")

	deferred := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4355")
		entry := `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Deferred thread here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":null,"kb":[],"sig":"` + strings.Repeat("a", 64) + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
		writeBundleFile(t, dir, "4355", "findings.json", "["+entry+"]")
		writeReview(t, dir, "4355", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[],"triage":[{"sig":"`+strings.Repeat("a", 64)+`","status":"deferred","rationale":"deferring this until the next pass for more evidence here"}]}`)
	}
	harness.RunBoth(t, "blocked-deferred", deferred, nil, false,
		"post", "acme/app", "4355", "--bind", "--json")
}

// runRecheckCases grades the live-thread recheck: more live opens than local
// opens refuses, unreachable degrades to COMMENT.
func runRecheckCases(t *testing.T) {
	t.Helper()
	liveOpen := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4356")
		writeReview(t, dir, "4356", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	}
	harness.RunBoth(t, "recheck-open-refuses", liveOpen,
		map[string]string{"GH_POST_RECHECK": "open"}, false,
		"post", "acme/app", "4356", "--bind", "--json")
	harness.RunBoth(t, "recheck-open-force", liveOpen,
		map[string]string{"GH_POST_RECHECK": "open"}, false,
		"post", "acme/app", "4356", "--bind", "--force", "--json")
	harness.RunBoth(t, "recheck-unreachable", liveOpen,
		map[string]string{"GH_POST_RECHECK": "unreachable"}, false,
		"post", "acme/app", "4356", "--bind", "--json")

	// Malformed live threads (garbage/nodata/scalarnode payloads) also
	// downgrade on both sides, but the probe err file carries jq's own
	// stderr text, which varies by jq version (location suffixes) — so
	// those shapes stay out of byte parity. Their behavior (downgrade to
	// COMMENT, reason in warnings and body, posted once) is graded by
	// evals/cases/post-refusals.sh, and the Go probe format by
	// TestPostRecheckMalformed in the record package.
}

// runFinalizerCases grades the double-post and finalizer refusals: the same
// SHA refuses, new SHAs and --force proceed, unreadable records warn
// through.
func runFinalizerCases(t *testing.T) {
	t.Helper()
	posted := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4357")
		writeReview(t, dir, "4357", cleanReview("> ✅ Looks good — reworded body, same SHA.", ""))
		writePostJSON(t, dir, "4357", `{"schema_version":"1","review":{"id":101},"head":"`+headA+`","posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`)
	}
	harness.RunBoth(t, "already-posted", posted, nil, false,
		"post", "acme/app", "4357", "--json")
	harness.RunBoth(t, "already-posted-force", posted,
		map[string]string{"GH_POST_RECHECK": "clear"}, false,
		"post", "acme/app", "4357", "--force", "--json")

	rearmed := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4358")
		writeReview(t, dir, "4358", cleanReview("> ✅ Looks good on the new SHA.", ""))
		writePostJSON(t, dir, "4358", `{"schema_version":"1","review":{"id":99},"head":"`+headB+`","posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`)
	}
	harness.RunBoth(t, "new-sha-rearms", rearmed,
		map[string]string{"GH_POST_LIVE": headA}, false,
		"post", "acme/app", "4358", "--json")

	unreadable := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4359")
		writeReview(t, dir, "4359", cleanReview("> ✅ Looks good — record was garbage.", ""))
		writePostJSON(t, dir, "4359", "not json in the record")
	}
	harness.RunBoth(t, "unreadable-record-proceeds", unreadable, nil, false,
		"post", "acme/app", "4359", "--json")

	headless := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4360")
		writeReview(t, dir, "4360", cleanReview("> ✅ Looks good — record has no head.", ""))
		writePostJSON(t, dir, "4360", `{"schema_version":"1"}`)
	}
	harness.RunBoth(t, "headless-record-proceeds", headless, nil, false,
		"post", "acme/app", "4360", "--json")
}

// TestPostParityDrift proves the stale-fetch behavior: drift warns and posts
// once as COMMENT, binding APPROVE downgrades, and gh-down warns through.
func TestPostParityDrift(t *testing.T) {
	t.Parallel()
	harness.RunBoth(t, "drift-comment", setupClean,
		map[string]string{"GH_POST_LIVE": headB}, false,
		"post", "acme/app", "4351", "--json")

	approve := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4361")
		writeReview(t, dir, "4361", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	}
	harness.RunBoth(t, "drift-approve-downgrade", approve,
		map[string]string{"GH_POST_LIVE": headB}, false,
		"post", "acme/app", "4361", "--bind", "--json")

	harness.RunBoth(t, "live-down-proceeds", setupClean,
		map[string]string{"GH_POST_LIVE": "fail"}, false,
		"post", "acme/app", "4351", "--json")
	harness.RunBoth(t, "live-down-approve", approve,
		map[string]string{"GH_POST_LIVE": "fail", "GH_POST_RECHECK": "clear"}, false,
		"post", "acme/app", "4361", "--bind", "--json")

	self := func(t *testing.T, dir, _ string) {
		t.Helper()
		bdir := filepath.Join(dir, ".review", "4362")
		if err := os.MkdirAll(bdir, 0o755); err != nil {
			t.Fatal(err)
		}
		pr := `{"state":"OPEN","headRefOid":"` + headA + `","baseRefOid":"` + headB + `","author":{"login":"teammate"}}`
		if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(pr), 0o644); err != nil {
			t.Fatal(err)
		}
		diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
		if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
			t.Fatal(err)
		}
		writeReview(t, dir, "4362", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	}
	harness.RunBoth(t, "self-authored-downgrade", self,
		map[string]string{"GH_POST_RECHECK": "clear", "GH_POST_ME": "teammate"}, false,
		"post", "acme/app", "4362", "--bind", "--json")
	harness.RunBoth(t, "auth-unknown-proceeds", self,
		map[string]string{"GH_POST_RECHECK": "clear", "GH_POST_ME": "fail"}, false,
		"post", "acme/app", "4362", "--bind", "--json")
}

// TestPostParityAnchors proves the keep-biased partition: unknown paths and
// out-of-hunk anchors move into the body, ambiguous anchors keep, and
// suggestion fences demote.
func TestPostParityAnchors(t *testing.T) {
	t.Parallel()
	unknownDry := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4363")
		writeReview(t, dir, "4363", cleanReview("> ✅ Looks good — one bad anchor below.", "["+commentOn("2", "RIGHT", "Tighten this bound.")+","+`{"path":"missing.txt","line":2,"side":"RIGHT","body":"Outside the diff entirely.","rigor":"walked"}`+"]"))
	}
	harness.RunBoth(t, "unknown-path-dry-run", unknownDry, nil, false,
		"post", "acme/app", "4363", "--dry-run", "--json")
	harness.RunBoth(t, "unknown-path-posts", unknownDry, nil, false,
		"post", "acme/app", "4363", "--json")

	outside := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4364")
		writeReview(t, dir, "4364", cleanReview("> ✅ Looks good — drifting anchor below.", "["+commentOn("99", "RIGHT", "Far outside the hunks.")+"]"))
	}
	harness.RunBoth(t, "outside-hunk-moves", outside, nil, false,
		"post", "acme/app", "4364", "--dry-run", "--json")

	nonNumeric := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4365")
		writeReview(t, dir, "4365", cleanReview("> ✅ Looks good — odd anchor below.", `[{"path":"notes.txt","line":"abc","side":"RIGHT","body":"Non-numeric anchor keeps.","rigor":"walked"}]`))
	}
	harness.RunBoth(t, "non-numeric-keeps", nonNumeric, nil, false,
		"post", "acme/app", "4365", "--json")

	leftOld := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4366")
		writeReview(t, dir, "4366", cleanReview("> ✅ Looks good — deleted line note.", "["+commentOn("2", "LEFT", "Removed line context.")+"]"))
	}
	harness.RunBoth(t, "left-old-keeps", leftOld, nil, false,
		"post", "acme/app", "4366", "--json")

	fence := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4367")
		writeReview(t, dir, "4367", cleanReview("> ✅ Looks good — fenced suggestion below.", `[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Use this instead.","suggestion":"`+"```"+"\ncode\n"+"```"+`","rigor":"walked"}]`))
	}
	harness.RunBoth(t, "suggestion-fence-demotes", fence, nil, false,
		"post", "acme/app", "4367", "--dry-run", "--json")

	diagram := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4368")
		body := "> ✅ marker plus enough length to pass the guard ok.\n\n### Flow\n\n```mermaid\nflowchart TD\n    A --> B\n```\n"
		writeReview(t, dir, "4368", cleanReview(body, ""))
	}
	harness.RunBoth(t, "diagram-dry-run", diagram, nil, false,
		"post", "acme/app", "4368", "--dry-run", "--json")
	harness.RunBoth(t, "diagram-posts", diagram, nil, false,
		"post", "acme/app", "4368", "--json")
}

// TestPostParityRefusals proves every typed refusal matches: bundle gates,
// agent-input shapes, the merge guards, POST failures, and usage errors.
func TestPostParityRefusals(t *testing.T) {
	t.Parallel()
	runRefusalBundleCases(t)
	runRefusalInputCases(t)
	runRefusalStoreCases(t)
}

// runRefusalBundleCases grades the bundle gates: locals, providers, closed
// PRs, and missing inputs.
func runRefusalBundleCases(t *testing.T) {
	t.Helper()
	localBundle := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-r1")
		writeReview(t, dir, "local-r1", cleanReview("> ✅ Looks good.", ""))
	}
	harness.RunBoth(t, "local-refuses", localBundle, nil, false,
		"post", "acme/app", "local-r1", "--json")

	provider := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4369")
		pr := `{"schema_version":"1","provider":"gitlab","number":4369,"state":"open","head_sha":"` + headA + `"}`
		writeBundleFile(t, dir, "4369", "PR.json", pr)
		writeReview(t, dir, "4369", cleanReview("> ✅ Looks good.", ""))
	}
	harness.RunBoth(t, "provider-refuses", provider, nil, false,
		"post", "acme/app", "4369", "--json")

	closed := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4370")
		pr := `{"state":"CLOSED","headRefOid":"` + headA + `","baseRefOid":"` + headB + `","author":{"login":"teammate"}}`
		writeBundleFile(t, dir, "4370", "PR.json", pr)
		writeReview(t, dir, "4370", cleanReview("> ✅ Looks good.", ""))
	}
	harness.RunBoth(t, "closed-refuses", closed, nil, false,
		"post", "acme/app", "4370", "--json")

	missingReview := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4371")
	}
	harness.RunBoth(t, "missing-review", missingReview, nil, false,
		"post", "acme/app", "4371", "--json")

	missingDiff := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4372")
		if err := os.Remove(filepath.Join(dir, ".review", "4372", "full.diff")); err != nil {
			t.Fatal(err)
		}
		writeReview(t, dir, "4372", cleanReview("> ✅ Looks good.", ""))
	}
	harness.RunBoth(t, "missing-diff", missingDiff, nil, false,
		"post", "acme/app", "4372", "--json")
}

// runRefusalInputCases grades malformed agent input: corrupt and non-object
// reviews, unknown schemas, degenerate bodies, and bad flags.
func runRefusalInputCases(t *testing.T) {
	t.Helper()
	corruptReview := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4373")
		writeReview(t, dir, "4373", "{broken")
	}
	harness.RunBoth(t, "corrupt-review", corruptReview, nil, false,
		"post", "acme/app", "4373", "--json")

	arrayReview := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4374")
		writeReview(t, dir, "4374", `[]`)
	}
	harness.RunBoth(t, "array-review", arrayReview, nil, false,
		"post", "acme/app", "4374", "--json")

	badVersion := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4375")
		writeReview(t, dir, "4375", `{"schema_version":"2","body":"> ✅ Looks good with a new schema.","approved":false,"comments":[]}`)
	}
	harness.RunBoth(t, "bad-schema-version", badVersion, nil, false,
		"post", "acme/app", "4375", "--json")

	degenerate := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4376")
		writeReview(t, dir, "4376", cleanReview("short", ""))
	}
	harness.RunBoth(t, "degenerate-body", degenerate, nil, false,
		"post", "acme/app", "4376", "--json")

	placeholder := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4377")
		writeReview(t, dir, "4377", cleanReview("test placeholder draft for now", ""))
	}
	harness.RunBoth(t, "placeholder-body", placeholder, nil, false,
		"post", "acme/app", "4377", "--json")

	harness.RunBoth(t, "bad-approve", setupClean, nil, false,
		"post", "acme/app", "4351", "--approve=yes", "--json")

	badTriage := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4378")
		writeReview(t, dir, "4378", `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[],"triage":[{"sig":"`+strings.Repeat("b", 64)+`","status":"bogus","rationale":"this rationale is long enough to pass the floor"}]}`)
	}
	harness.RunBoth(t, "bad-triage-status", badTriage, nil, false,
		"post", "acme/app", "4378", "--json")
}

// runRefusalStoreCases grades the merge guards and POST failures: exotic
// priors refuse, failed posts arm nothing, and unknown flags usage-error.
func runRefusalStoreCases(t *testing.T) {
	t.Helper()
	exoticPrior := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4379")
		writeReview(t, dir, "4379", cleanReview("> ✅ Looks good.", ""))
		writeBundleFile(t, dir, "4379", "findings.json", "[5]")
	}
	harness.RunBoth(t, "exotic-prior", exoticPrior, nil, false,
		"post", "acme/app", "4379", "--json")

	nullPrior := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4380")
		writeReview(t, dir, "4380", cleanReview("> ✅ Looks good.", ""))
		writeBundleFile(t, dir, "4380", "findings.json", "[null]")
	}
	harness.RunBoth(t, "null-prior", nullPrior, nil, false,
		"post", "acme/app", "4380", "--json")

	nonListPrior := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4381")
		writeReview(t, dir, "4381", cleanReview("> ✅ Looks good.", ""))
		writeBundleFile(t, dir, "4381", "findings.json", `{"sig":"x"}`)
	}
	harness.RunBoth(t, "non-list-prior", nonListPrior, nil, false,
		"post", "acme/app", "4381", "--json")

	harness.RunBoth(t, "post-fails", setupClean,
		map[string]string{"GH_POST_MODE": "fail"}, false,
		"post", "acme/app", "4351", "--json")
	harness.RunBoth(t, "post-garbage", setupClean,
		map[string]string{"GH_POST_MODE": "garbage"}, false,
		"post", "acme/app", "4351", "--json")
	harness.RunBoth(t, "post-empty", setupClean,
		map[string]string{"GH_POST_MODE": "empty"}, false,
		"post", "acme/app", "4351", "--json")

	harness.RunBoth(t, "unknown-flag", setupClean, nil, false,
		"post", "acme/app", "4351", "--bogus")

	customReview := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPR(t, dir, "4382")
		writeReview(t, dir, "4382", cleanReview("> ✅ Looks good.", ""))
		writeBundleFile(t, dir, "4382", "custom.json", cleanReview("> ✅ Custom review file path.", ""))
	}
	harness.RunBoth(t, "custom-review-file", customReview, nil, false,
		"post", "acme/app", "4382", "--review-file", ".review/4382/custom.json", "--dry-run", "--json")
	harness.RunBoth(t, "missing-custom-review-file", customReview, nil, false,
		"post", "acme/app", "4382", "--review-file", ".review/4382/nope.json", "--json")
}

// tsRe scrubs timestamps the way the shared harness does.
var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

func scrubTS(s string) string {
	return tsRe.ReplaceAllString(s, "0000-00-00T00:00:00Z")
}

// TestPostDoublePost proves exactly-one-write across sequential posts: the
// first identical post succeeds once, the replay refuses duplicate-payload
// with no second POST, and the record stays byte-identical.
func TestPostDoublePost(t *testing.T) {
	t.Parallel()
	env := map[string]string{"GH_POST_RECHECK": "clear"}
	dirA := t.TempDir()
	homeA := filepath.Join(t.TempDir(), "home")
	dirB := t.TempDir()
	homeB := filepath.Join(t.TempDir(), "home")
	for _, d := range []string{homeA, homeB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setupClean(t, dirA, homeA)
	setupClean(t, dirB, homeB)
	args := []string{"post", "acme/app", "4351", "--json"}
	firstA := harness.Run(t, false, dirA, homeA, env, args...)
	firstB := harness.Run(t, true, dirB, homeB, env, args...)
	if firstA.Status != 0 || firstB.Status != 0 {
		t.Fatalf("first post exits bash=%d go=%d", firstA.Status, firstB.Status)
	}
	postsAfterFirst := countReviewPosts(t, dirA)
	secondA := harness.Run(t, false, dirA, homeA, env, args...)
	secondB := harness.Run(t, true, dirB, homeB, env, args...)
	compareRuns(t, secondA, secondB, dirA, homeA, dirB, homeB)
	assertReplayRefused(t, "bash", secondA)
	assertReplayRefused(t, "go", secondB)
	assertNoSecondPost(t, dirA, "bash", postsAfterFirst)
	assertNoSecondPost(t, dirB, "go", postsAfterFirst)
}

// assertReplayRefused asserts the replay refused duplicate-payload.
func assertReplayRefused(t *testing.T, side string, out parity.Outputs) {
	t.Helper()
	if out.Status != 1 {
		t.Errorf("%s replay exits %d, want 1", side, out.Status)
	}
	if !strings.Contains(out.Stderr, "duplicate-payload") && !strings.Contains(out.Stdout, "duplicate-payload") {
		t.Errorf("%s replay misses duplicate-payload", side)
	}
}

// assertNoSecondPost asserts the replay made no second reviews POST.
func assertNoSecondPost(t *testing.T, dir, side string, want int) {
	t.Helper()
	if got := countReviewPosts(t, dir); got != want {
		t.Errorf("%s replay posted again: %d reviews calls, want %d", side, got, want)
	}
}

// countReviewPosts counts reviews-POST calls in the side's gh-calls log.
func countReviewPosts(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".gh-calls.log"))
	if err != nil {
		return 0
	}
	n := 0
	for line := range strings.Lines(string(raw)) {
		if strings.HasPrefix(line, "CALL: ") && strings.Contains(line, "/reviews ") {
			n++
		}
	}
	return n
}

// compareRuns compares two sequential runs: exit, scrubbed streams, and
// every file (timestamps and sandbox roots scrubbed).
func compareRuns(t *testing.T, a, b parity.Outputs, dirA, homeA, dirB, homeB string) {
	t.Helper()
	if a.Status != b.Status {
		t.Errorf("exit: bash=%d go=%d", a.Status, b.Status)
	}
	roots := []string{dirA, homeA, dirB, homeB}
	scrub := func(s string) string {
		s = scrubTS(s)
		for _, r := range roots {
			s = strings.ReplaceAll(s, r, "ROOT")
		}
		return s
	}
	if scrub(a.Stdout) != scrub(b.Stdout) {
		t.Errorf("stdout differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stdout, b.Stdout)
	}
	if scrub(a.Stderr) != scrub(b.Stderr) {
		t.Errorf("stderr differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stderr, b.Stderr)
	}
	compareRunFiles(t, a, b, scrub)
}

// compareRunFile asserts one file present on both sides with equal bytes.
func compareRunFile(t *testing.T, k string, va string, b parity.Outputs, scrub func(string) string, seen map[string]bool) {
	t.Helper()
	seen[k] = true
	vb, ok := b.Files[k]
	if !ok {
		t.Errorf("file presence differs: %s bash=present go=absent", k)
		return
	}
	if scrub(va) != scrub(vb) {
		t.Errorf("file differs: %s\n--- bash ---\n%s\n--- go ---\n%s", k, va, vb)
	}
}

// compareRunFiles compares every written file of two sequential runs.
func compareRunFiles(t *testing.T, a, b parity.Outputs, scrub func(string) string) {
	t.Helper()
	seen := map[string]bool{}
	for k, va := range a.Files {
		compareRunFile(t, k, va, b, scrub, seen)
	}
	for k := range b.Files {
		if !seen[k] {
			t.Errorf("file presence differs: %s bash=absent go=present", k)
		}
	}
}
