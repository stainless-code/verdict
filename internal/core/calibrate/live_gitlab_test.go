package calibratecore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// gitlabJoinEntry builds one inline entry carrying the sig post stamps
// into the discussion body.
func gitlabJoinEntry() *jq.Obj {
	return jq.NewObj(
		"kind", "inline",
		"path", "notes.txt",
		"side", "RIGHT",
		"line", 2,
		"anchored", true,
		"body", "Fixed body here with enough substance.",
		"suggestion", nil,
		"inBounds", true,
		"rigor", "walked",
		"severity", "critical",
		"sig", sigJoin,
		"sig_version", "1",
		"firstSeen", "aaaaaaa",
		"lastSeen", "aaaaaaa",
		"status", "open",
		"rationale", "",
		"history", []any{},
	)
}

const sigJoin = "c111111111111111111111111111111111111111"

// gitlabJoinThread builds the live discussion post wrote for the entry:
// the marker-stamped body on the anchored path, resolved.
func gitlabJoinThread(body string, resolved bool) map[string]any {
	return map[string]any{
		"id": "d1",
		"notes": []any{map[string]any{
			"id":       101,
			"type":     "DiffNote",
			"body":     body,
			"resolved": resolved,
			"system":   false,
			"position": map[string]any{
				"new_path": "notes.txt",
				"new_line": 2,
			},
		}},
	}
}

// TestGitLabJoinSettlesMarkerThread proves the GitLab join settles a
// finding against the discussion post wrote: the marker-stamped body plus
// the anchored path match, and a resolved thread on a changed file reads
// fixed.
func TestGitLabJoinSettlesMarkerThread(t *testing.T) {
	t.Parallel()
	host, ok := liveHostFor("gitlab")
	if !ok {
		t.Fatal("no gitlab live host in the table")
	}
	entry := gitlabJoinEntry()
	posted := host.posted(entry)
	want := "Fixed body here with enough substance.\n\n<!-- verdict-finding sig=" + sigJoin + " -->"
	if posted != want {
		t.Fatalf("posted body = %q, want the marker-stamped discussion body", posted)
	}
	thread := gitlabJoinThread(posted, true)
	if !host.match(thread, posted, "notes.txt") {
		t.Fatal("live discussion does not match the posted finding")
	}
	if !host.resolved(thread) {
		t.Fatal("resolved discussion reads unresolved")
	}
	got := joinOne(entry, []any{thread}, []any{"notes.txt"}, host)
	if got.result != outcomeFixed {
		t.Fatalf("outcome = %q, want fixed", got.result)
	}
	if got.detail != "thread resolved + cited file changed since review head" {
		t.Errorf("detail = %q, want the changed-file evidence", got.detail)
	}
}

// TestGitLabJoinOpenThread proves an unresolved live discussion keeps the
// finding open.
func TestGitLabJoinOpenThread(t *testing.T) {
	t.Parallel()
	host, ok := liveHostFor("gitlab")
	if !ok {
		t.Fatal("no gitlab live host in the table")
	}
	entry := gitlabJoinEntry()
	posted := host.posted(entry)
	got := joinOne(entry, []any{gitlabJoinThread(posted, false)}, []any{}, host)
	if got.result != statusOpen {
		t.Fatalf("outcome = %q, want open", got.result)
	}
	if got.detail != "thread still open" {
		t.Errorf("detail = %q, want the open-thread evidence", got.detail)
	}
}
