package threadscore

import "testing"

// TestReplyMarkerRoundTrip pins the hidden marker drain appends and the ids it
// carries back to the next refresh.
func TestReplyMarkerRoundTrip(t *testing.T) {
	thread, waiting := "PRRT_1", "456"
	marker := ReplyMarker(thread, waiting)
	if want := "<!-- verdict-reply thread=PRRT_1 to=456 -->"; marker != want {
		t.Fatalf("ReplyMarker = %q, want %q", marker, want)
	}
	body := "Here is the answer.\n\n" + marker
	id, to, ok := ReplyTarget(body)
	if !ok || id != thread || to != waiting {
		t.Errorf("ReplyTarget = %q, %q, %v; want %q, %q, true", id, to, ok, thread, waiting)
	}
	if _, _, ok := ReplyTarget("you wrote " + marker + "\nstill human"); ok {
		t.Error("ReplyTarget read a quoted marker")
	}
}

// TestReplyMarkerSetsAuthorKind proves a posted reply reads as verdict while a
// marker quoted mid-body leaves the human author label alone.
func TestReplyMarkerSetsAuthorKind(t *testing.T) {
	marker := ReplyMarker("PRRT_1", "456")
	if !IsVerdictBody("answer\n\n" + marker) {
		t.Error("a posted reply body did not read as verdict-marked")
	}
	if got := AuthorKind("alice", false, "answer\n\n"+marker); got != AuthorVerdict {
		t.Errorf("AuthorKind = %q, want %q", got, AuthorVerdict)
	}
	quoted := "you wrote " + marker + "\nstill a human reply"
	if IsVerdictBody(quoted) {
		t.Error("a quoted marker read as the final line")
	}
	if got := AuthorKind("alice", false, quoted); got != AuthorHuman {
		t.Errorf("quoted AuthorKind = %q, want %q", got, AuthorHuman)
	}
}
