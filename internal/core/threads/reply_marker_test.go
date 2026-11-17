package threadscore

import "testing"

// TestReplyMarkerRoundTrip pins the hidden marker drain appends and the author
// label a fresh threads refresh gives the reply. A quoted marker is not the
// final line and must not make a human comment verdict-authored.
func TestReplyMarkerRoundTrip(t *testing.T) {
	thread, waiting := "PRRT_1", "456"
	marker := ReplyMarker(thread, waiting)
	if want := "<!-- verdict-reply thread=PRRT_1 to=456 -->"; marker != want {
		t.Fatalf("ReplyMarker = %q, want %q", marker, want)
	}
	body := "Here is the answer.\n\n" + marker
	if !IsVerdictBody(body) {
		t.Error("a posted reply body did not read as verdict-marked")
	}
	if got := AuthorKind("alice", false, body); got != AuthorVerdict {
		t.Errorf("AuthorKind = %q, want %q", got, AuthorVerdict)
	}
	quoted := "you wrote " + marker + "\nstill a human reply"
	if IsVerdictBody(quoted) {
		t.Error("a quoted marker read as the final line")
	}
	if got := AuthorKind("alice", false, quoted); got != AuthorHuman {
		t.Errorf("quoted AuthorKind = %q, want %q", got, AuthorHuman)
	}
	if id, to, ok := ReplyTarget(body); !ok || id != thread || to != waiting {
		t.Errorf("ReplyTarget = %q, %q, %v; want %q, %q, true", id, to, ok, thread, waiting)
	}
	if _, _, ok := ReplyTarget(quoted); ok {
		t.Error("ReplyTarget read a quoted marker")
	}
}
