package threadscore

import (
	"strings"
	"testing"
)

// TestFindingMarkerRoundTrip proves the hidden marker renders and parses, and
// only as the body's final line.
func TestFindingMarkerRoundTrip(t *testing.T) {
	t.Parallel()
	sig := strings.Repeat("a", 40)
	body := "Looks wrong.\n\n" + FindingMarker(sig)
	if got, ok := FindingSig(body); !ok || got != sig {
		t.Fatalf("FindingSig = %q, %v; want %q", got, ok, sig)
	}
	if got, ok := FindingSig("Looks wrong."); ok || got != "" {
		t.Errorf("markerless body parsed as %q", got)
	}
	quoted := "quoted:\n" + FindingMarker(sig) + "\nmore prose"
	if _, ok := FindingSig(quoted); ok {
		t.Error("mid-body marker parsed as the final line")
	}
	other := "<!-- verdict-review payload=" + strings.Repeat("b", 40) + " head=" + strings.Repeat("c", 40) + " -->"
	if _, ok := FindingSig(other); ok {
		t.Error("review marker parsed as a finding marker")
	}
	if !IsVerdictBody(other) {
		t.Error("review marker did not read as verdict-authored")
	}
}

// TestAuthorKind proves verdict wins over bot, host bot flags and the [bot]
// suffix read bot, and everything else reads human.
func TestAuthorKind(t *testing.T) {
	t.Parallel()
	sig := strings.Repeat("a", 40)
	cases := []struct { //nolint:govet // test-table readability beats field alignment
		hostBot bool
		name    string
		login   string
		body    string
		want    string
	}{
		{name: "verdict marker wins", login: "renovate[bot]", hostBot: true, body: FindingMarker(sig), want: AuthorVerdict},
		{name: "host bot", login: "dependabot", hostBot: true, body: "bump", want: AuthorBot},
		{name: "suffix bot", login: "lint[bot]", hostBot: false, body: "note", want: AuthorBot},
		{name: "human", login: "ana", hostBot: false, body: "note", want: AuthorHuman},
		{name: "marker mid-body", login: "ana", hostBot: false, body: FindingMarker(sig) + "\ntail", want: AuthorHuman},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AuthorKind(tc.login, tc.hostBot, tc.body); got != tc.want {
				t.Errorf("AuthorKind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCapBody proves the 8 KiB cap, the truncated flag, and that a cut never
// splits a UTF-8 rune.
func TestCapBody(t *testing.T) {
	t.Parallel()
	short := "short"
	if got, cut := CapBody(short); got != short || cut {
		t.Errorf("short body = %q, %v", got, cut)
	}
	long := strings.Repeat("x", MaxCommentBytes) + "tail"
	got, cut := CapBody(long)
	if !cut || len(got) != MaxCommentBytes {
		t.Errorf("long body len=%d cut=%v", len(got), cut)
	}
	// Every byte of a three-byte rune cut at the boundary trims cleanly.
	body := strings.Repeat("a", MaxCommentBytes-1) + "é" + "tail"
	got, cut = CapBody(body)
	if !cut || !strings.HasPrefix(got, strings.Repeat("a", 10)) {
		t.Fatalf("rune-boundary body truncated wrong: cut=%v", cut)
	}
	if strings.ContainsRune(got[len(got)-1:], 0xfffd) {
		t.Error("cap split a UTF-8 rune")
	}
}

// TestFromGitHubThreads pins the authoritative GraphQL mapping: thread id,
// numeric comment ids, startLine range, Bot author kind, the finding link on
// a verdict root, and the capped-comment signal.
//
//nolint:revive,cyclop // the fixture matrix reads as one assertion block
func TestFromGitHubThreads(t *testing.T) {
	t.Parallel()
	sig := strings.Repeat("d", 40)
	nodes := []any{map[string]any{
		"id": "PRRT_1", "isResolved": false, "isOutdated": true,
		"path": "a.go", "line": 9, "startLine": 7, "diffSide": "RIGHT",
		"comments": map[string]any{"totalCount": 2, "nodes": []any{
			map[string]any{
				"id": "PRRC_1", "databaseId": 101,
				"author":    map[string]any{"__typename": "Bot", "login": "verdict[bot]"},
				"body":      "nit\n\n" + FindingMarker(sig),
				"createdAt": "2026-11-01T10:00:00Z",
				"url":       "https://example.invalid/c/101",
			},
			map[string]any{
				"id": "PRRC_2", "databaseId": 102,
				"author":    map[string]any{"__typename": "User", "login": "mate"},
				"body":      "done",
				"createdAt": "2026-11-01T11:00:00Z",
			},
		}},
	}}
	threads, capped := FromGitHubThreads(nodes)
	if len(threads) != 1 || capped {
		t.Fatalf("threads = %d capped=%v", len(threads), capped)
	}
	th := threads[0]
	if th.ID != "PRRT_1" || th.Path != "a.go" || th.Line == nil || *th.Line != 9 ||
		th.StartLine == nil || *th.StartLine != 7 || th.Side == nil || *th.Side != SideRight ||
		th.Resolved || !th.Outdated || th.Kind != KindReview {
		t.Errorf("thread = %+v", th)
	}
	if th.Author != "verdict[bot]" || th.AuthorKind != AuthorVerdict || th.FindingSig == nil || *th.FindingSig != sig {
		t.Errorf("root identity = %+v", th)
	}
	if th.URL == nil || *th.URL != "https://example.invalid/c/101" {
		t.Errorf("thread url = %v", th.URL)
	}
	if len(th.Comments) != 2 || th.Comments[0].ID != "101" || th.Comments[1].ID != "102" ||
		th.Comments[1].AuthorKind != AuthorHuman {
		t.Errorf("comments = %+v", th.Comments)
	}

	node0, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatal("fixture node is not an object")
	}
	node0["comments"] = map[string]any{"totalCount": 5, "nodes": []any{}}
	_, capped = FromGitHubThreads([]any{node0, map[string]any{
		"id": "PRRT_2", "path": "a.go", "diffSide": "LEFT",
		"comments": map[string]any{"totalCount": 1, "nodes": []any{
			map[string]any{"id": "c", "body": "x", "author": map[string]any{"login": "ana"}},
		}},
	}})
	if !capped {
		t.Error("dropped comments were not reported as truncated")
	}
}

// TestFromGitLab pins discussion mapping: system notes drop, resolution comes
// from the root note, and a position written at another head reads outdated.
//
//nolint:cyclop // the fixture matrix reads as one assertion block
func TestFromGitLab(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	discussions := []any{
		map[string]any{"id": "d1", "notes": []any{
			map[string]any{
				"id": 201, "body": "rename", "system": false, "resolved": false,
				"author": map[string]any{"username": "ana"},
				"position": map[string]any{
					"new_path": "a.go", "old_path": "a.go",
					"new_line": 4, "head_sha": head,
				},
			},
			map[string]any{"id": 202, "body": "fixed", "author": map[string]any{"username": "bo"}},
			map[string]any{"id": 203, "body": "label", "system": true},
		}},
		map[string]any{"id": "d2", "notes": []any{
			map[string]any{
				"id": 204, "body": "old", "resolved": true,
				"author": map[string]any{"username": "bo", "bot": true},
				"position": map[string]any{
					"old_path": "a.go", "old_line": 2,
					"head_sha": strings.Repeat("b", 40),
				},
			},
		}},
	}
	threads := FromGitLab(discussions, head)
	if len(threads) != 2 {
		t.Fatalf("threads = %d", len(threads))
	}
	first := threads[0]
	if first.ID != "d1" || first.Resolved || first.Outdated || first.Line == nil || *first.Line != 4 ||
		first.Side == nil || *first.Side != SideRight || len(first.Comments) != 2 ||
		first.Comments[1].ID != "202" {
		t.Errorf("discussion = %+v", first)
	}
	second := threads[1]
	if second.ID != "d2" || !second.Resolved || !second.Outdated ||
		second.Side == nil || *second.Side != SideLeft || second.Line == nil || *second.Line != 2 ||
		second.AuthorKind != AuthorBot {
		t.Errorf("old-side discussion = %+v", second)
	}
}

// TestFromForgejo pins resolver as resolution and a zero current position as
// the stale-anchor signal.
func TestFromForgejo(t *testing.T) {
	t.Parallel()
	reviews := []any{map[string]any{"id": 7, "body": "summary", "user": map[string]any{"login": "rev"}}}
	comments := []any{
		map[string]any{
			"id": 11, "path": "a.go", "position": 1, "original_position": 1,
			"body": "nit", "user": map[string]any{"login": "ana"}, "resolver": map[string]any{"login": "ana"},
		},
		map[string]any{
			"id": 12, "path": "a.go", "position": 0, "original_position": 3,
			"body": "old", "user": map[string]any{"login": "bo"},
		},
	}
	threads := FromForgejo(reviews, comments, "")
	if len(threads) != 3 {
		t.Fatalf("threads = %d: %+v", len(threads), threads)
	}
	if threads[0].Kind != KindIssue || threads[0].ID != "7" {
		t.Errorf("review = %+v", threads[0])
	}
	if !threads[1].Resolved || threads[1].Outdated {
		t.Errorf("resolved comment = %+v", threads[1])
	}
	if !threads[2].Outdated || threads[2].Side == nil || *threads[2].Side != SideLeft ||
		threads[2].Line == nil || *threads[2].Line != 3 {
		t.Errorf("stale comment = %+v", threads[2])
	}
}

// TestFromBitbucket pins root grouping across pages, reply order, and the
// pending/deleted drops.
func TestFromBitbucket(t *testing.T) {
	t.Parallel()
	comments := []any{
		map[string]any{
			"id": 2, "parent": map[string]any{"id": 1},
			"content": map[string]any{"raw": "reply"},
			"user":    map[string]any{"nickname": "bo"}, "created_on": "2026-11-01T01:00:00Z",
		},
		map[string]any{
			"id": 1, "content": map[string]any{"raw": "root"},
			"user":       map[string]any{"nickname": "ana"},
			"resolution": map[string]any{"user": map[string]any{"nickname": "ana"}},
			"inline":     map[string]any{"path": "a.go", "to": 4, "outdated": true},
			"links":      map[string]any{"html": map[string]any{"href": "https://example.invalid/c/1"}},
		},
		map[string]any{
			"id": 3, "parent": map[string]any{"id": 1}, "deleted": true,
			"content": map[string]any{"raw": "gone"},
		},
		map[string]any{"id": 4, "pending": true, "content": map[string]any{"raw": "draft"}},
	}
	threads := FromBitbucket(comments)
	if len(threads) != 1 {
		t.Fatalf("threads = %d: %+v", len(threads), threads)
	}
	th := threads[0]
	if th.ID != "1" || !th.Resolved || !th.Outdated || th.Path != "a.go" ||
		th.Line == nil || *th.Line != 4 || th.URL == nil || len(th.Comments) != 2 ||
		th.Comments[0].ID != "1" || th.Comments[1].ID != "2" {
		t.Errorf("thread = %+v", th)
	}
}

// TestFromAzure pins the status fold (active/pending open, settled resolved),
// system/deleted drops, path normalization, and the range split.
//
//nolint:cyclop // the fixture matrix reads as one assertion block
func TestFromAzure(t *testing.T) {
	t.Parallel()
	threadsRaw := []any{
		map[string]any{"id": 9, "status": "fixed", "threadContext": map[string]any{
			"filePath":       "/a.go",
			"rightFileStart": map[string]any{"line": 4},
			"rightFileEnd":   map[string]any{"line": 6},
		}, "comments": []any{
			map[string]any{"id": 1, "content": "system", "commentType": "system", "publishedDate": "2026-01-01T00:00:00Z"},
			map[string]any{
				"id": 2, "content": "nit", "author": map[string]any{"displayName": "ana"},
				"publishedDate": "2026-11-01T00:00:00Z",
			},
		}},
		map[string]any{"id": 10, "status": "pending", "comments": []any{
			map[string]any{
				"id": 3, "content": "question", "author": map[string]any{"displayName": "bo"},
				"publishedDate": "2026-11-01T00:00:00Z",
			},
		}},
		map[string]any{
			"id": 11, "status": "active", "threadContext": map[string]any{"filePath": "/b.go"},
			"comments": []any{map[string]any{
				"id": 4, "content": "stale",
				"author": map[string]any{"displayName": "cy"}, "publishedDate": "2026-11-01T00:00:00Z",
			}},
		},
	}
	threads := FromAzure(threadsRaw)
	if len(threads) != 3 {
		t.Fatalf("threads = %d: %+v", len(threads), threads)
	}
	first := threads[0]
	if !first.Resolved || first.Path != "a.go" || first.Line == nil || *first.Line != 6 ||
		first.StartLine == nil || *first.StartLine != 4 || len(first.Comments) != 1 {
		t.Errorf("range thread = %+v", first)
	}
	if threads[1].Resolved || threads[1].Kind != KindIssue {
		t.Errorf("pending thread = %+v", threads[1])
	}
	if !threads[2].Outdated || threads[2].Path != "b.go" || threads[2].Line != nil {
		t.Errorf("stale thread = %+v", threads[2])
	}
}

// TestFromGitCafe pins the generic review/comment mapping, resolvedAt, and
// legacy resolved/outdated flags.
func TestFromGitCafe(t *testing.T) {
	t.Parallel()
	reviews := []any{map[string]any{
		"id": "r1", "body": "summary",
		"author": map[string]any{"handle": "rev"}, "createdAt": "2026-11-01T00:00:00Z",
	}}
	comments := []any{
		map[string]any{
			"id": "c1", "path": "a.go", "line": 2, "side": "RIGHT",
			"body": "nit", "author": map[string]any{"handle": "ana"}, "resolvedAt": "2026-11-02T00:00:00Z",
		},
		map[string]any{"id": "c2", "body": "old", "outdated": true},
	}
	threads := FromGitCafe(reviews, comments)
	if len(threads) != 3 {
		t.Fatalf("threads = %d", len(threads))
	}
	if threads[0].Author != "rev" || threads[0].Kind != KindIssue {
		t.Errorf("review = %+v", threads[0])
	}
	if !threads[1].Resolved || threads[1].Kind != KindReview || threads[1].Line == nil || *threads[1].Line != 2 {
		t.Errorf("comment = %+v", threads[1])
	}
	if !threads[2].Outdated || threads[2].Kind != KindIssue {
		t.Errorf("outdated comment = %+v", threads[2])
	}
}

// TestRootlessThreadStillLabelsItsAuthorKind pins that an anchored thread
// whose comments the host did not return (empty nodes, or a payload with no
// comments key) still carries a schema-valid author_kind. Found by
// FuzzProviderMappers: the feed held author_kind "", which
// schemas/threads.schema.json rejects.
//
// Break killed: deleting the AuthorKind assignment in foldRoot's no-comment
// branch leaves author_kind "" in both rows.
func TestRootlessThreadStillLabelsItsAuthorKind(t *testing.T) {
	t.Parallel()
	for name, node := range map[string]map[string]any{
		"empty nodes": {"id": "T1", "path": "a.go", "comments": map[string]any{"totalCount": 0, "nodes": []any{}}},
		"no comments": {"id": "T2", "path": "a.go"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			threads, _ := FromGitHubThreads([]any{node})
			if len(threads) != 1 {
				t.Fatalf("threads = %d, want the anchored thread kept", len(threads))
			}
			if got := threads[0].AuthorKind; got != AuthorHuman {
				t.Fatalf("author_kind = %q, want %q", got, AuthorHuman)
			}
		})
	}
}
