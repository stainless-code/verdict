package recordcore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// mentionZWSP is the escape the recorded body must carry.
const mentionZWSP = "\u200b"

// mentionHead is the head SHA the mention fixtures were fetched at.
const mentionHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// mentionDiff changes notes.txt across one hunk, so the fixture body has a
// real reviewed range and the merge never falls back to unchecked anchors.
const mentionDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// mentionReviewJSON writes one review.json around body.
func mentionReviewJSON(t *testing.T, body string) string {
	t.Helper()
	return jq.NewObj(
		"schema_version", "1",
		"body", body,
		"approved", false,
		"comments", []any{},
	).MarshalPretty()
}

// mentionBundle writes the local fixture bundle and returns its directory.
func mentionBundle(t *testing.T, slug, body string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/" + slug
	writeFile(t, dir+"/source.json", `{"head_sha":"`+mentionHead+
		`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	writeFile(t, dir+"/full.diff", mentionDiff)
	writeFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(mentionDiff)))
	writeFile(t, dir+"/review.json", mentionReviewJSON(t, body))
	seedControls(t, dir)
	return dir
}

// mergeMentionBody runs the shared merge over one reviewed body the way the
// record and post services do, and returns the body it constructed.
func mergeMentionBody(t *testing.T, slug, body string) string {
	t.Helper()
	dir := mentionBundle(t, slug, body)
	review, err := recordcore.LoadReview(dir + "/review.json")
	if err != nil {
		t.Fatalf("load review: %v", err)
	}
	snap, ref := controls.Gate(dir, readcommon.BundleBase(dir, nil))
	if ref != nil {
		t.Fatalf("controls gate: %v", ref)
	}
	c := &collector{}
	res, merr := recordcore.Merge(testRuntime(c), &recordcore.MergeParams{
		Dir:          dir,
		Prov:         "local",
		HeadFull:     mentionHead,
		Review:       review,
		ReviewFile:   dir + "/review.json",
		Root:         ".",
		RepoRoot:     ".",
		Body:         body,
		FindingsFile: dir + "/findings.json",
		DryRun:       true,
		Effective:    core.Defaults(),
		Repo:         "acme/app",
		Pr:           slug,
		Cmd:          "record",
		Controls:     snap,
		InputChecked: true,
	})
	if merr != nil {
		t.Fatalf("Merge: %v", merr)
	}
	return res.Body
}

// mentionBody is the recorded-body shape under test: a mention in the summary
// prose, one in a section heading, an address that is not a mention, and the
// code shapes the escape must leave byte-identical.
func mentionBody() string {
	return "> [!IMPORTANT]\n> Ping @alice about the retry loop before merging.\n\n" +
		"### Ask @alice-b2\n\nWrite to user@example.com, not `@carol`:\n\n" +
		"```go\nnotify(@dave)\n```\n"
}

// TestMergeNeutralizesMentionsOnce proves the merge constructs the recorded
// body with every prose mention escaped, no code or address rewritten, and the
// result already a fixed point: re-scanning the merged body rewrites nothing.
func TestMergeNeutralizesMentionsOnce(t *testing.T) {
	merged := mergeMentionBody(t, "local-mention-merge", mentionBody())
	if strings.Contains(merged, "@alice") {
		t.Errorf("merged body still carries a live mention:\n%s", merged)
	}
	for _, want := range []string{
		"Ping @" + mentionZWSP + "alice about the retry loop",
		"### Ask @" + mentionZWSP + "alice-b2",
		"user@example.com",
		"`@carol`",
		"```go\nnotify(@dave)\n```",
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged body misses %q:\n%s", want, merged)
		}
	}
	if n := strings.Count(merged, mentionZWSP); n != 2 {
		t.Errorf("zero-width spaces = %d, want 2 (one per prose mention):\n%s", n, merged)
	}
	if again := recordcore.NeutralizeMentions(merged); again != merged {
		t.Errorf("re-scanning the recorded body changed it:\nfirst:\n%s\nsecond:\n%s", merged, again)
	}
}

// TestRecordNeutralizesBodyMentions proves the record write path stores the
// neutralized body: the section heading record derives from the agent's body
// reaches findings.json with the escape, never as a live mention.
func TestRecordNeutralizesBodyMentions(t *testing.T) {
	bundle(t, "local-mention",
		`{"schema_version":"1","body":"> [!IMPORTANT]\n> Ping @alice about the retry loop before merging.\n\n### Ask @alice-b2\n\nDetails live in the note above.\n","approved":false,"comments":[]}`)
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-mention")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	raw, err := os.ReadFile(".verdict/state/local-mention/findings.json")
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	stored := string(raw)
	if !strings.Contains(stored, "### Ask @"+mentionZWSP+"alice-b2") {
		t.Errorf("findings.json misses the escaped heading:\n%s", stored)
	}
	if strings.Contains(stored, "@alice") {
		t.Errorf("findings.json still carries a live mention:\n%s", stored)
	}
}

// mentionCommentBody is the inline finding under test: a mention in prose, an
// address that is not a mention, and a suggestion whose bytes are code.
const mentionCommentBody = "Ping @bob-abc about the retry loop; write to user@example.com.\n"

// mentionSuggestion is a code replacement: the escape must never enter it,
// because a zero-width space inside code is a silent edit to the patch.
const mentionSuggestion = "notify(@dave)\n"

// mergeMentionEntry runs the merge over a review carrying one inline finding
// and returns its entries blob.
func mergeMentionEntry(t *testing.T, slug, commentBody string) string {
	t.Helper()
	dir := mentionBundle(t, slug, "prose only")
	writeFile(t, dir+"/review.json", jq.NewObj(
		"schema_version", "1",
		"body", "prose only",
		"approved", false,
		"comments", []any{jq.NewObj(
			"path", "notes.txt",
			"heading", "top",
			"line", 3,
			"side", "RIGHT",
			"body", commentBody,
			"suggestion", mentionSuggestion,
		)},
	).MarshalPretty())
	review, err := recordcore.LoadReview(dir + "/review.json")
	if err != nil {
		t.Fatalf("load review: %v", err)
	}
	snap, ref := controls.Gate(dir, readcommon.BundleBase(dir, nil))
	if ref != nil {
		t.Fatalf("controls gate: %v", ref)
	}
	res, merr := recordcore.Merge(testRuntime(&collector{}), &recordcore.MergeParams{
		Dir: dir, Prov: "local", HeadFull: mentionHead, Review: review,
		ReviewFile: dir + "/review.json", Root: ".", RepoRoot: ".",
		Body: "prose only", FindingsFile: dir + "/findings.json", DryRun: true,
		Effective: core.Defaults(), Repo: "acme/app", Pr: slug,
		Cmd: "record", Controls: snap, InputChecked: true,
	})
	if merr != nil {
		t.Fatalf("Merge: %v", merr)
	}
	return jq.NewObj("entries", res.Entries).MarshalPretty()
}

// TestRecordNeutralizesFindingBodies proves a mention inside a finding body is
// escaped at construction. That body is published as its own inline comment and
// never passes through the review prose, so an escape applied only to the prose
// leaves a live notification. The suggestion is code and must stay verbatim.
func TestRecordNeutralizesFindingBodies(t *testing.T) {
	blob := mergeMentionEntry(t, "local-mention-finding", mentionCommentBody)
	if strings.Contains(blob, "@bob-abc") {
		t.Errorf("finding body still carries a live mention:\n%s", blob)
	}
	if !strings.Contains(blob, "Ping @"+mentionZWSP+"bob-abc about the retry loop") {
		t.Errorf("finding body misses the escaped mention:\n%s", blob)
	}
	if !strings.Contains(blob, "user@example.com") {
		t.Errorf("finding body lost the address that is not a mention:\n%s", blob)
	}
	if !strings.Contains(blob, "notify(@dave)") || strings.Contains(blob, "notify(@\u200bdave)") {
		t.Errorf("suggestion bytes changed; a suggestion is code:\n%s", blob)
	}
}

// TestRecordFindingSignatureCoversTheEscape proves the escape happens before
// the entry is signed. Merging a body that still carries the mention and one
// that arrives already escaped must produce byte-identical entries: the
// signature is computed over the escaped bytes either way. A signature taken
// before the escape would hash the raw body for the first input and the escaped
// body for the second, so the two would drift.
func TestRecordFindingSignatureCoversTheEscape(t *testing.T) {
	raw := mergeMentionEntry(t, "local-mention-sig-raw", mentionCommentBody)
	escaped := mergeMentionEntry(t, "local-mention-sig-esc",
		"Ping @"+mentionZWSP+"bob-abc about the retry loop; write to user@example.com.\n")
	if raw != escaped {
		t.Errorf("raw and pre-escaped input diverge, so the signature does not cover the escape:\nraw:\n%s\nescaped:\n%s", raw, escaped)
	}
	if again := recordcore.NeutralizeMentions(mentionCommentBody); again != "Ping @"+mentionZWSP+"bob-abc about the retry loop; write to user@example.com.\n" {
		t.Errorf("second pass changed the body: %q", again)
	}
}
