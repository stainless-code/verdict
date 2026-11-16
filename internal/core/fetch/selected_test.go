package fetchcore

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	selectedSigOne = "0123456789abcdef0123456789abcdef01234567"
	selectedSigTwo = "1123456789abcdef0123456789abcdef01234567"
)

// reactionComments is the review-comment payload GitHub returns: verdict's
// root comment per finding (body built from the real marker), one reply, and
// one human comment that carries no marker.
func reactionComments() string {
	return `[` +
		reactionComment("1001", "verdict-bot", "Fix the boundary."+"\n\n"+threadscore.FindingMarker(selectedSigOne), "") + `,` +
		reactionComment("1002", "verdict-bot", "A reply quoting a marker."+"\n\n"+threadscore.FindingMarker(selectedSigTwo), "1001") + `,` +
		reactionComment("1003", "alice", "Looks wrong to me.", "") + `]`
}

func reactionComment(id, login, body, replyTo string) string {
	reply := `null`
	if replyTo != "" {
		reply = `"` + replyTo + `"`
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return `{"id":` + id + `,"pull_request_review_id":7,"path":"a.txt",` +
		`"in_reply_to_id":` + reply + `,"body":` + string(raw) + `,` +
		`"user":{"login":"` + login + `","type":"User"}}`
}

// reaction scripts one comment's slurped 👍 reaction page.
func reaction(id, login string) (string, scriptResult) {
	return "gh\x00api\x00repos/o/r/pulls/comments/" + id + "/reactions\x00--paginate\x00--slurp",
		scriptResult{stdout: `[[{"content":"+1","user":{"login":"` + login + `","type":"User"}}]]`}
}

// fetchSelectedBundle runs one fetch with the given comment payload and
// reaction calls scripted, returning the bundle dir.
func fetchSelectedBundle(t *testing.T, comments string, reactions map[string]scriptResult) string {
	t.Helper()
	t.Chdir(t.TempDir())
	runSelectedFetch(t, comments, reactions)
	return filepath.Join(".verdict", "state", "7")
}

// runSelectedFetch fetches into the current directory's bundle.
func runSelectedFetch(t *testing.T, comments string, reactions map[string]scriptResult) {
	t.Helper()
	cmdr := fetchHappyCommander(t)
	cmdr.out["gh\x00api\x00repos/o/r/pulls/7/comments\x00--paginate"] = scriptResult{stdout: comments}
	maps.Copy(cmdr.out, reactions)
	var human, diag bytes.Buffer
	var streamed []string
	if _, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed)); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func readSelected(t *testing.T, dir string) (map[string]any, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "selected.json"))
	if err != nil {
		return nil, false
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		t.Fatalf("selected.json does not parse: %v", derr)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("selected.json is not an object: %s", raw)
	}
	return m, true
}

// TestFetchWritesSelectedSigs pins the selection fetch records: only the root
// verdict comment reacted 👍 selects its sig, and the file carries the
// provenance fix scopes the round with.
func TestFetchWritesSelectedSigs(t *testing.T) {
	oneKey, oneRes := reaction("1001", "alice")
	twoKey, twoRes := reaction("1002", "alice")
	dir := fetchSelectedBundle(t, reactionComments(),
		map[string]scriptResult{oneKey: oneRes, twoKey: twoRes})
	got, ok := readSelected(t, dir)
	if !ok {
		t.Fatalf("no selected.json beside the bundle: %v", dir)
	}
	if sigs := strings.TrimSpace(jq.PrettyOrdered(got["sigs"])); sigs != `[
  "`+selectedSigOne+`"
]` {
		t.Errorf("sigs = %s; want only the reacted root comment's sig (a reply never counts)", sigs)
	}
	want := map[string]string{
		"schema_version": `"1"`,
		"repo":           `"o/r"`,
		"pr":             `"7"`,
		"head_sha":       `"` + fetchTestHead + `"`,
		"selected_at":    `"2026-11-02T12:00:00Z"`,
	}
	for key, value := range want {
		if got := strings.TrimSpace(jq.PrettyOrdered(got[key])); got != value {
			t.Errorf("selected.json %s = %s; want %s", key, got, value)
		}
	}
}

// TestFetchSelectedSkipsBotsAndSelf pins the exclusion rules: a bot's 👍 and
// verdict's own 👍 on its comment select nothing, so no empty claim lands.
func TestFetchSelectedSkipsBotsAndSelf(t *testing.T) {
	botKey, botRes := reaction("1001", "automation[bot]")
	selfKey, selfRes := reaction("1001", "verdict-bot")
	dir := fetchSelectedBundle(t, reactionComments(),
		map[string]scriptResult{botKey: botRes, selfKey: selfRes})
	if _, ok := readSelected(t, dir); ok {
		t.Fatalf("selected.json exists although no human reacted 👍 (bots and self never select)")
	}
}

// TestFetchSelectedDropsStaleClaim proves a re-fetch after the reactions were
// removed stops claiming a scope: the previous selection is deleted, never
// left behind to scope a fix round nobody asked for.
func TestFetchSelectedDropsStaleClaim(t *testing.T) {
	oneKey, oneRes := reaction("1001", "alice")
	dir := fetchSelectedBundle(t, reactionComments(), map[string]scriptResult{oneKey: oneRes})
	if _, ok := readSelected(t, dir); !ok {
		t.Fatal("first fetch wrote no selection")
	}
	emptyKey, _ := reaction("1001", "alice")
	runSelectedFetch(t, reactionComments(), map[string]scriptResult{emptyKey: {stdout: `[[]]`}})
	if _, ok := readSelected(t, dir); ok {
		t.Fatalf("stale selected.json survived a fetch that saw no 👍")
	}
}

// TestFetchSelectedKeepsClaimOnFailedRead proves a failed reaction read never
// narrows a selection the operator is working from.
func TestFetchSelectedKeepsClaimOnFailedRead(t *testing.T) {
	oneKey, oneRes := reaction("1001", "alice")
	dir := fetchSelectedBundle(t, reactionComments(), map[string]scriptResult{oneKey: oneRes})
	if _, ok := readSelected(t, dir); !ok {
		t.Fatal("first fetch wrote no selection")
	}
	runSelectedFetch(t, reactionComments(), map[string]scriptResult{oneKey: {fail: true}})
	if _, ok := readSelected(t, dir); !ok {
		t.Fatalf("a failed reaction read deleted the working selection")
	}
}

var _ = core.SelectedFile{}
