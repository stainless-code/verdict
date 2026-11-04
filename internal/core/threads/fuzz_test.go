package threadscore_test

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// seedThreadFixtures adds the repo's thread fixtures to the corpus.
func seedThreadFixtures(f *testing.F) {
	f.Helper()
	paths, err := filepath.Glob("../../../evals/fixtures/prompt-injection/threads-*.json")
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
}

// FuzzProviderMappers feeds hostile provider JSON to all six host mappers and
// holds every thread they emit to the published contract: the feed built from
// it validates against schemas/threads.schema.json (the independent oracle —
// enum values, types, required keys), and no comment body exceeds the cap or
// carries invalid UTF-8. A mapper may drop what it cannot read, but it must
// never emit a feed the schema would reject, and must never panic.
func FuzzProviderMappers(f *testing.F) {
	for _, s := range []string{
		`[]`, `{}`, `null`, `[null,1,"x",[],{}]`,
		`[{"id":"T","isResolved":true,"path":"a.go","line":9,"startLine":7,"diffSide":"RIGHT","comments":{"totalCount":1,"nodes":[{"id":"c","databaseId":1,"author":{"__typename":"Bot","login":"x[bot]"},"body":"hi\n<!-- verdict-finding sig=dddddddddddddddddddddddddddddddddddddddd -->","createdAt":"2026-11-01T10:00:00Z"}]}}]`,
		`[{"id":"d1","notes":[{"id":1,"body":"n","system":false,"resolvable":true,"resolved":false,"author":{"username":"u"},"position":{"new_path":"a","new_line":3,"head_sha":"h"}}]}]`,
		`[{"id":7,"body":"b","path":"p","position":0,"original_position":4,"user":{"login":"l"},"resolver":{"login":"r"},"pull_request_review_id":2}]`,
		`[{"id":1,"content":{"raw":"b"},"inline":{"path":"p","to":3,"outdated":true},"parent":{"id":0},"user":{"nickname":"n"}}]`,
		`[{"id":1,"status":"active","comments":[{"id":1,"content":"c","commentType":"text","author":{"displayName":"d"}}],"threadContext":{"filePath":"/p","rightFileStart":{"line":2}}}]`,
		"[{\"body\":\"\xff\xfe\",\"path\":\"\u0000\"}]",
		`[{"path":"a","line":-5,"startLine":99999999999999999999,"diffSide":"SIDEWAYS","comments":{"nodes":[{"body":"x"}]}}]`,
	} {
		f.Add([]byte(s))
	}
	seedThreadFixtures(f)
	checker, err := schemacheck.Load("../../../skills/verdict/schemas/threads.schema.json", "")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := jq.Decode(data)
		if err != nil {
			return
		}
		for name, ts := range allMappers(asList(doc)) {
			assertFeedContract(t, checker, name, ts, data)
		}
	})
}

// asList wraps a non-array payload as a one-element list.
func asList(doc any) []any {
	if items, ok := doc.([]any); ok {
		return items
	}
	return []any{doc}
}

// allMappers runs every host mapper over the same decoded payload.
func allMappers(items []any) map[string][]threadscore.Thread {
	gh, _ := threadscore.FromGitHubThreads(items)
	return map[string][]threadscore.Thread{
		"github":    gh,
		"reviews":   threadscore.FromReviewBodies(items),
		"gitlab":    threadscore.FromGitLab(items, "head"),
		"forgejo":   threadscore.FromForgejo(items, items, "head"),
		"bitbucket": threadscore.FromBitbucket(items),
		"azure":     threadscore.FromAzure(items),
		"gitcafe":   threadscore.FromGitCafe(items, items),
	}
}

// assertFeedContract renders ts as a threads.json feed and checks it against
// the published schema and the body cap.
func assertFeedContract(t *testing.T, checker *schemacheck.Checker, name string, ts []threadscore.Thread, input []byte) {
	t.Helper()
	feed := &threadscore.File{
		SchemaVersion: threadscore.SchemaVersion, Provider: "github", PR: 1,
		HeadSHA: "h", FetchedAt: "2026-11-01T10:00:00Z", Threads: ts,
	}
	rendered := jq.PrettyOrdered(feed.Wire())
	decoded, err := schemacheck.Decode([]byte(rendered))
	if err != nil {
		t.Fatalf("%s: feed is not valid JSON: %v\n%s", name, err, rendered)
	}
	if bad := checker.Check(decoded); len(bad) > 0 {
		t.Fatalf("%s: feed violates threads.schema.json: %v\ninput %q", name, bad, input)
	}
	for i := range ts {
		assertBodies(t, name, ts[i].Comments)
	}
}

// assertBodies checks the cap and UTF-8 validity of every comment body.
func assertBodies(t *testing.T, name string, comments []threadscore.Comment) {
	t.Helper()
	for _, c := range comments {
		if len(c.Body) > threadscore.MaxCommentBytes || !utf8.ValidString(c.Body) {
			t.Fatalf("%s: body of %d bytes (valid UTF-8: %v) breaks the %d-byte cap contract",
				name, len(c.Body), utf8.ValidString(c.Body), threadscore.MaxCommentBytes)
		}
	}
}
