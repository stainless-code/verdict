package postcore

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// splitPostState is a settled state whose post sends a head, two follow-up
// parts, and one inline comment. last ends the final part.
func splitPostState(t *testing.T, last string) *state {
	t.Helper()
	var warnings []string
	st := postSplitState(&warnings)
	st.flags.DryRun = true
	st.event = "COMMENT"
	st.commitID = "abc"
	st.body = "HEAD text."
	st.extraParts = []string{"SECOND text.", "THIRD text. " + last}
	st.payloads = nil
	return st
}

// TestRenderedCommentsAreTheCommentsPosted proves the dry-run lists every
// comment the real post sends, in posting order and byte for byte: the digest
// covers all of them, so a confirm dialog that showed the head alone would pin
// text the operator never read.
func TestRenderedCommentsAreTheCommentsPosted(t *testing.T) { //nolint:revive // one flow asserts the labels, the texts, and the posted parity
	st := splitPostState(t, "END")
	st.buildPayloads()
	// buildPayloads derives the inline comments from the merge; this state has
	// none, so the one the real post would carry is set after it.
	st.comments = []any{jq.NewObj("path", "notes.txt", "line", jq.Number("2"), "side", "RIGHT", "body", "Inline text.")}
	got := st.renderedComments()
	want := []struct{ label, text string }{
		{"Review body, part 1 of 3", "HEAD text.\n\n<!-- verdict-review payload=" + st.phash + " head=abc -->"},
		{"Follow-up review comment, part 2 of 3", "> ℹ️ Note: review body continued (part 2/3 — split at the 65,536-char hosting limit; see findings.json for the full text).\n\nSECOND text."},
		{"Follow-up review comment, part 3 of 3", "> ℹ️ Note: review body continued (part 3/3 — split at the 65,536-char hosting limit; see findings.json for the full text).\n\nTHIRD text. END"},
		{"Inline comment on notes.txt:2", "Inline text."},
	}
	for i := 1; i < len(st.payloads); i++ {
		var posted struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(st.payloads[i]), &posted); err != nil {
			t.Fatal(err)
		}
		if posted.Body != got[i].Text {
			t.Errorf("follow-up %d shown as %q but posted as %q", i+1, got[i].Text, posted.Body)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d rendered comments, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Label != w.label || got[i].Text != w.text {
			t.Errorf("comment %d = %q / %q, want %q / %q", i+1, got[i].Label, got[i].Text, w.label, w.text)
		}
	}
}

// TestRenderedHashCoversEveryFollowUp proves a change confined to the last
// follow-up part moves the content digest the confirmed post is pinned to, and
// equal content digests equally.
func TestRenderedHashCoversEveryFollowUp(t *testing.T) {
	digest := func(last string) string {
		st := splitPostState(t, last)
		st.buildPayloads()
		return st.postedHash
	}
	one, two := digest("END-ONE"), digest("END-TWO")
	if one == "" || one == two {
		t.Fatalf("digest = %q vs %q: an edit to the last follow-up must move it", one, two)
	}
	if again := digest("END-ONE"); one != again {
		t.Errorf("digest is not stable for equal content: %q vs %q", one, again)
	}
}

// TestPostedDigestCoversTheMarkerAndTheEvent proves the pinned digest is taken
// over the final payloads, not over the body before the review marker is
// appended: the event and the head, which the content digest never covered,
// move it.
func TestPostedDigestCoversTheMarkerAndTheEvent(t *testing.T) {
	digest := func(mutate func(*state)) string {
		st := splitPostState(t, "END")
		mutate(st)
		st.buildPayloads()
		return st.postedHash
	}
	base := digest(func(*state) {})
	if got := digest(func(st *state) { st.event = "REQUEST_CHANGES" }); got == base {
		t.Error("a changed event left the pinned digest alone: it would post a different review than the one confirmed")
	}
	if got := digest(func(st *state) { st.commitID = "def" }); got == base {
		t.Error("a changed head left the pinned digest alone")
	}
}

// TestDryRunAndRealPostPinTheSameBytes proves the preview's digest is the
// digest of what a real post of the same state hands the host: the dry run
// builds the marker it will not send, so the two modes agree byte for byte.
func TestDryRunAndRealPostPinTheSameBytes(t *testing.T) {
	built := func(dry bool) *state {
		st := splitPostState(t, "END")
		st.flags.DryRun = dry
		st.buildPayloads()
		return st
	}
	dry, live := built(true), built(false)
	if dry.postedHash == "" || dry.postedHash != live.postedHash {
		t.Fatalf("dry-run pin %q, real post pin %q: the preview must pin the real bytes", dry.postedHash, live.postedHash)
	}
	if got := payloadsDigest(live.payloads); got != dry.postedHash {
		t.Errorf("digest of the payloads handed to the host = %q, want the pinned %q", got, dry.postedHash)
	}
	if !strings.Contains(live.payloads[0], "verdict-review payload=") {
		t.Fatalf("the real first payload carries no review marker: %s", live.payloads[0])
	}
}

// bundleSnapshot maps every file under dir to its bytes.
func bundleSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		out[path] = string(raw)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNoPersistDryRunWritesNothing proves the re-render a confirmed submit runs
// leaves the bundle byte for byte as it found it, while the CLI's own dry run
// (the preview) still stages findings.dryrun.json as it always did.
func TestNoPersistDryRunWritesNothing(t *testing.T) { //nolint:revive // one flow asserts the staged file, the pin, and the untouched bundle
	t.Chdir(t.TempDir())
	postBundle(t, postReview)
	dir := ".verdict/state/7"
	run := func(noPersist bool) string {
		var warnings, human []string
		req := postTestReq(postCommander(t), Flags{Approve: "false", ReviewFile: dir + "/review.json", DryRun: true}, &warnings, &human)
		req.NoPersist = noPersist
		resp, ref := Run(context.Background(), req)
		if ref != nil {
			t.Fatalf("dry-run (no persist = %v) = %v", noPersist, ref)
		}
		return resp.Data.RenderedHash
	}
	pinned := run(false)
	if _, err := os.Stat(dir + "/findings.dryrun.json"); err != nil {
		t.Fatalf("the CLI-style dry run no longer stages findings.dryrun.json: %v", err)
	}
	if err := os.Remove(dir + "/findings.dryrun.json"); err != nil {
		t.Fatal(err)
	}
	before := bundleSnapshot(t, dir)
	if again := run(true); again != pinned {
		t.Errorf("the read-only render pins %q, the persisting one %q: they must render alike", again, pinned)
	}
	after := bundleSnapshot(t, dir)
	for path, content := range after {
		if old, ok := before[path]; !ok || old != content {
			t.Errorf("the read-only render wrote %s", path)
		}
	}
	if len(after) != len(before) {
		t.Errorf("the read-only render changed the file set: %d files before, %d after", len(before), len(after))
	}
}
