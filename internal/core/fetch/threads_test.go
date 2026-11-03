package fetchcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestGithubThreadRecords proves review-comment replies collapse under their
// root, review bodies land as conversation comments, and REST leaves the
// resolution flag false.
func TestGithubThreadRecords(t *testing.T) {
	t.Parallel()
	reviews := `[{"id":1,"user":{"login":"rev"},"body":"summary"}]`
	inline := `[{"id":10,"path":"a.go","line":3,"side":"RIGHT","body":"root",` +
		`"user":{"login":"ana"},"in_reply_to_id":null},` +
		`{"id":11,"path":"a.go","line":3,"side":"RIGHT","body":"reply",` +
		`"user":{"login":"bo"},"in_reply_to_id":10}]`
	got := githubThreadRecords(reviews, inline)
	if len(got) != 2 {
		t.Fatalf("threads = %d, want 2: %+v", len(got), got)
	}
	root := got[0]
	if root.id != "10" || root.path != "a.go" || root.line == nil || *root.line != 3 ||
		root.side != "RIGHT" || root.author != "ana" || root.kind != threadKindReview || root.resolved {
		t.Errorf("root = %+v", root)
	}
	if got[1].kind != threadKindIssue || got[1].body != "summary" {
		t.Errorf("review thread = %+v", got[1])
	}
}

// TestGitlabThreadRecords proves system notes drop, DiffNotes carry position
// and resolution, and plain notes stay conversation comments.
func TestGitlabThreadRecords(t *testing.T) {
	t.Parallel()
	notes := `[{"id":1,"system":true,"body":"moved to review"},` +
		`{"id":2,"type":"DiffNote","resolved":true,"body":"nit",` +
		`"author":{"username":"ana"},"position":{"new_path":"a.go","new_line":4}},` +
		`{"id":3,"body":"hello","author":{"username":"bo"}}]`
	got := gitlabThreadRecords(notes)
	if len(got) != 2 {
		t.Fatalf("threads = %d, want 2: %+v", len(got), got)
	}
	if got[0].id != "2" || !got[0].resolved || got[0].path != "a.go" ||
		got[0].line == nil || *got[0].line != 4 || got[0].side != "RIGHT" {
		t.Errorf("diff note = %+v", got[0])
	}
	if got[1].kind != threadKindIssue || got[1].path != "" || got[1].line != nil {
		t.Errorf("issue note = %+v", got[1])
	}
}

// TestBitbucketThreadRecords proves deleted/pending comments and replies
// drop, the root keeps the inline position, and the resolution object is the
// thread state.
func TestBitbucketThreadRecords(t *testing.T) {
	t.Parallel()
	comments := []any{
		map[string]any{
			"id": jq.Number("1"), "content": map[string]any{"raw": "root"},
			"user": map[string]any{"nickname": "ana"}, "resolution": map[string]any{"user": "bo"},
			"inline": map[string]any{"path": "a.go", "to": jq.Number("4")},
		},
		map[string]any{
			"id": jq.Number("2"), "parent": map[string]any{"id": jq.Number("1")},
			"content": map[string]any{"raw": "reply"},
		},
		map[string]any{"id": jq.Number("3"), "deleted": true, "content": map[string]any{"raw": "gone"}},
		map[string]any{"id": jq.Number("4"), "pending": true, "content": map[string]any{"raw": "draft"}},
	}
	got := bitbucketThreadRecords(comments)
	if len(got) != 1 {
		t.Fatalf("threads = %d, want 1: %+v", len(got), got)
	}
	if got[0].id != "1" || !got[0].resolved || got[0].path != "a.go" ||
		got[0].line == nil || *got[0].line != 4 || got[0].side != "RIGHT" {
		t.Errorf("root = %+v", got[0])
	}
}

// TestForgejoThreadRecords proves review bodies and inline comments land in
// one feed and a null position marks the anchor outdated.
func TestForgejoThreadRecords(t *testing.T) {
	t.Parallel()
	reviews := `[{"id":1,"user":{"login":"ana"},"body":"summary"}]`
	inline := `[{"id":7,"path":"a.go","line":2,"side":"LEFT","position":null,` +
		`"original_position":5,"body":"old","user":{"login":"bo"}}]`
	got := forgejoThreadRecords(reviews, inline)
	if len(got) != 2 {
		t.Fatalf("threads = %d, want 2: %+v", len(got), got)
	}
	if got[1].kind != threadKindReview || got[1].side != "LEFT" || !got[1].outdated {
		t.Errorf("inline = %+v", got[1])
	}
}

// TestAzureThreadRecords proves deleted/system comments drop, status folds
// to resolved, and a left-side context reads LEFT.
func TestAzureThreadRecords(t *testing.T) {
	t.Parallel()
	raw := `[{"id":9,"status":"fixed","threadContext":{"filePath":"a.go",` +
		`"leftFileStart":{"line":2}},"comments":[` +
		`{"content":"system","commentType":"system","publishedDate":"2026-01-01"},` +
		`{"id":1,"content":"root","author":{"displayName":"ana"},"publishedDate":"2026-01-01"}]},` +
		`{"id":10,"isDeleted":true,"comments":[{"content":"gone","publishedDate":"2026-01-01"}]}]`
	got := azureThreadRecords(raw)
	if len(got) != 1 {
		t.Fatalf("threads = %d, want 1: %+v", len(got), got)
	}
	if got[0].id != "9" || !got[0].resolved || got[0].path != "a.go" ||
		got[0].line == nil || *got[0].line != 2 || got[0].side != "LEFT" ||
		got[0].author != "ana" {
		t.Errorf("thread = %+v", got[0])
	}
}

// TestGitcafeThreadRecords proves the best-effort mapper keeps generic
// path/body/resolution fields and drops empty comments.
func TestGitcafeThreadRecords(t *testing.T) {
	t.Parallel()
	revs := `[{"id":1,"body":"summary","resolved":true}]`
	cmts := `[{"id":2,"path":"a.go","line":3,"side":"RIGHT","body":"nit"},` +
		`{"id":3,"body":""}]`
	got := gitcafeThreadRecords(revs, cmts)
	if len(got) != 2 {
		t.Fatalf("threads = %d, want 2: %+v", len(got), got)
	}
	if !got[0].resolved || got[0].kind != threadKindIssue {
		t.Errorf("review = %+v", got[0])
	}
	if got[1].path != "a.go" || got[1].kind != threadKindReview {
		t.Errorf("comment = %+v", got[1])
	}
}
