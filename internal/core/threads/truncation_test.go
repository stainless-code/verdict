package threadscore

import (
	"strings"
	"testing"
)

func TestEmptyFeedReceipt(t *testing.T) {
	t.Parallel()
	got := (&File{Truncated: true}).Wire()
	for key, want := range map[string]any{"truncated": true, "truncation_reason": "scan_cap", "returned": int64(0), "total": nil, "next_offset": nil} {
		value, ok := got.Get(key)
		if !ok || value != want {
			t.Errorf("receipt %s = %v (present %v), want %v", key, value, ok, want)
		}
	}
}

func TestCommentExcerptDoesNotMarkFeedTruncated(t *testing.T) {
	t.Parallel()
	got := (&File{
		Provider: "github",
		PR:       7,
		HeadSHA:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Threads: []Thread{{
			ID: "t1",
			Comments: []Comment{{
				ID:        "c1",
				Body:      strings.Repeat("x", MaxCommentBytes),
				Truncated: true,
			}},
		}},
	}).Wire()
	if v, ok := got.Get("truncated"); ok {
		t.Fatalf("feed truncated = %v, want omitted when only a comment body is excerpted", v)
	}
}
