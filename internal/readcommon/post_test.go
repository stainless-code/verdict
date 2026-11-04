package readcommon

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// writePostRecord writes the case's post record, treating "" as no file.
func writePostRecord(t *testing.T, dir, record string) {
	t.Helper()
	if record == "" {
		return
	}
	writeStore(t, dir, "post.json", record)
}

// checkPostRecord compares one decoded post record against the case's shape.
func checkPostRecord(t *testing.T, m map[string]any, wantNil bool, wantID string) {
	t.Helper()
	if wantNil {
		if m != nil {
			t.Fatalf("PostRecord = %v, want nil", m)
		}
		return
	}
	if m == nil {
		t.Fatal("PostRecord = nil, want the decoded object")
	}
	if wantID == "" {
		return
	}
	rev, ok := m["review"].(map[string]any)
	if !ok {
		t.Fatalf("PostRecord review block = %v, want an object", m["review"])
	}
	if got := jq.R(rev["id"]); got != wantID {
		t.Errorf("review id = %q, want %q", got, wantID)
	}
}

// TestPostRecordReadsTheRecordObject proves the shared post.json read: a
// valid record decodes to its object, and a missing, undecodable, or
// non-object record reads nil for the callers that only inspect it.
func TestPostRecordReadsTheRecordObject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		record  string
		wantID  string
		wantNil bool
	}{
		{name: "record", record: `{"review":{"id":42}}`, wantID: "42"},
		{name: "empty-object", record: "{}"},
		{name: "null", record: "null", wantNil: true},
		{name: "array", record: "[]", wantNil: true},
		{name: "garbage", record: "not json{{{", wantNil: true},
		{name: "missing", wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writePostRecord(t, dir, tc.record)
			checkPostRecord(t, PostRecord(dir), tc.wantNil, tc.wantID)
		})
	}
}
