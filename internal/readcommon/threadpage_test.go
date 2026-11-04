package readcommon_test

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// threadPageJSON renders one authoritative reviewThreads page.
func threadPageJSON(nodes, pageInfo string) string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{` +
		`"nodes":` + nodes + `,"pageInfo":` + pageInfo + `}}}}}`
}

// threadIDs reads the ids off a decoded page in order.
func threadIDs(nodes []any) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// assertThreadsPage checks one decoded page: the node ids in order, the next
// cursor, and the more flag.
func assertThreadsPage(t *testing.T, stdout string, wantIDs []string, wantNext string, wantMore bool) {
	t.Helper()
	nodes, next, more, err := readcommon.ThreadsPage(stdout)
	if err != nil {
		t.Fatalf("ThreadsPage(%s) error: %v", stdout, err)
	}
	if got := threadIDs(nodes); !slices.Equal(got, wantIDs) {
		t.Errorf("node ids = %v; want %v", got, wantIDs)
	}
	if next != wantNext {
		t.Errorf("next = %q; want %q", next, wantNext)
	}
	if more != wantMore {
		t.Errorf("more = %v; want %v", more, wantMore)
	}
}

// assertThreadsPageRefuses checks that an unreadable page fails without
// leaking any decoded value, so a failed read never reads as an empty feed.
func assertThreadsPageRefuses(t *testing.T, stdout string) {
	t.Helper()
	nodes, next, more, err := readcommon.ThreadsPage(stdout)
	if err == nil {
		t.Fatalf("ThreadsPage(%s) = %v/%q/%v; want an error", stdout, nodes, next, more)
	}
	if nodes != nil || next != "" || more {
		t.Errorf("failed read leaked %v/%q/%v; want zero values", nodes, next, more)
	}
}

// TestThreadsPage pins the page decoder the threads feed walks with: the
// nodes of this page in order, the next cursor only when the host says a
// next page exists, and never a cursor the payload did not carry.
func TestThreadsPage(t *testing.T) {
	t.Parallel()
	t.Run("more pages", func(t *testing.T) {
		t.Parallel()
		page := threadPageJSON(`[{"id":"t1"},{"id":"t2"}]`, `{"hasNextPage":true,"endCursor":"cur1"}`)
		assertThreadsPage(t, page, []string{"t1", "t2"}, "cur1", true)
	})
	t.Run("last page drops the cursor", func(t *testing.T) {
		t.Parallel()
		page := threadPageJSON(`[{"id":"t3"}]`, `{"hasNextPage":false,"endCursor":"cur2"}`)
		assertThreadsPage(t, page, []string{"t3"}, "", false)
	})
	t.Run("more pages without a cursor", func(t *testing.T) {
		t.Parallel()
		page := threadPageJSON(`[{"id":"t4"}]`, `{"hasNextPage":true}`)
		assertThreadsPage(t, page, []string{"t4"}, "", true)
	})
	t.Run("empty page", func(t *testing.T) {
		t.Parallel()
		page := threadPageJSON(`[]`, `{"hasNextPage":false,"endCursor":null}`)
		assertThreadsPage(t, page, nil, "", false)
	})
}

// TestThreadsPageRefusals pins that a transport failure and an errors payload
// are refusals, not empty feeds: reading either as "no prior threads" would
// re-post every finding on the next pass.
func TestThreadsPageRefusals(t *testing.T) {
	t.Parallel()
	t.Run("unreadable payload", func(t *testing.T) {
		t.Parallel()
		assertThreadsPageRefuses(t, `{"data":`)
	})
	t.Run("errors payload is not an empty page", func(t *testing.T) {
		t.Parallel()
		assertThreadsPageRefuses(t, `{"errors":[{"message":"nope"}]}`)
	})
}

// reviewThreadsArgv renders the argv a page read is expected to carry: the
// fixed head, the owner/name/pr variables, the cursor when one was handed in,
// then the shared query pair last.
func reviewThreadsArgv(vars []string) []string {
	argv := []string{"api", "graphql"}
	argv = append(argv, vars...)
	argv = append(argv, "-f", "query="+readcommon.QueryThreadsFull)
	return argv
}

// TestReviewThreadsArgv pins the page-read argv: the owner/name/pr variables
// on every call, the endCursor variable only when a cursor was handed in
// (GraphQL's nullable String), and the full-thread query last. The query body
// itself is the goldens' contract; this test owns the flag plumbing.
func TestReviewThreadsArgv(t *testing.T) {
	t.Parallel()
	t.Run("first page has no cursor variable", func(t *testing.T) {
		t.Parallel()
		got := readcommon.ReviewThreadsArgv("acme", "app", "5101", "")
		want := reviewThreadsArgv([]string{"-F", "owner=acme", "-F", "name=app", "-F", "pr=5101"})
		if !slices.Equal(got, want) {
			t.Fatalf("argv = %v; want %v", got, want)
		}
	})
	t.Run("later page carries the cursor", func(t *testing.T) {
		t.Parallel()
		got := readcommon.ReviewThreadsArgv("acme", "app", "5101", "cur1")
		want := reviewThreadsArgv([]string{
			"-F", "owner=acme", "-F", "name=app",
			"-F", "pr=5101", "-F", "endCursor=cur1",
		})
		if !slices.Equal(got, want) {
			t.Fatalf("argv = %v; want %v", got, want)
		}
	})
}
