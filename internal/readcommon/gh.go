package readcommon

import (
	"context"
	"errors"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// GraphQL queries, byte-identical to the pinned -f query= arguments
// (indentation included: the eval stub logs argv verbatim, so the
// harness compares these bytes between implementations). One constant per
// payload shape, never per verb: ready and report --live share the
// resolution-only read, calibrate uses the comment variant.
const (
	flagF = "-F"

	// QueryThreads is the resolution-only read for ready and report --live.
	QueryThreads = `
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }`
	// QueryThreadsFull is the authoritative threads.json read: the whole
	// thread (resolution, outdated, anchor, comment authors, bodies, ids).
	// It carries pageInfo so a cursor walk can page every thread instead of
	// trusting one page; comments carry totalCount so a capped comment list
	// marks the feed truncated instead of silently dropping replies.
	QueryThreadsFull = `
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              pageInfo { hasNextPage endCursor }
              nodes {
                id
                isResolved
                isOutdated
                path
                line
                startLine
                diffSide
                comments(first:100) {
                  totalCount
                  nodes { id databaseId author { __typename login } body createdAt url }
                }
              }
            }
          }
        }
      }`
	// QueryThreadsWithComments is the calibrate join's read: resolution
	// state plus the comment bodies and paths the join matches on.
	QueryThreadsWithComments = `
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved comments(first:30) { nodes { body path } } }
          }
        }
      }
    }`
)

// errGhFailed seals a gh call that could not run or exited nonzero. The
// captured streams ride GhResult; callers only distinguish success.
var errGhFailed = errors.New("gh call failed")

// GhResult carries a gh call's captured streams.
type GhResult struct {
	Stdout string
	Stderr string
}

// Threads reads the paginated live review threads for owner/name/pr through
// cmdr, flattening every page. withComments selects the calibrate shape
// (comment bodies and paths); the resolution-only shape is shared by ready
// and report --live. Callers write res.Stderr to their probe err file
// before mapping err to their verb's warn-or-refuse posture.
func Threads(
	ctx context.Context,
	cmdr core.Commander,
	owner, name, pr string,
	withComments bool,
) ([]any, GhResult, error) {
	query := QueryThreads
	if withComments {
		query = QueryThreadsWithComments
	}
	stdout, stderr, ok := coreexec.Gh(ctx, cmdr, ThreadsArgv(owner, name, pr, query)...)
	res := GhResult{Stdout: stdout, Stderr: stderr}
	if !ok {
		return nil, res, jq.Sealed("gh", errGhFailed)
	}
	nodes, err := SlurpThreads(stdout)
	if err != nil {
		return nil, res, err
	}
	return nodes, res, nil
}

// SplitOwner splits owner/repo into its two halves.
func SplitOwner(repo string) (owner, name string) {
	if before, after, ok := strings.Cut(repo, "/"); ok {
		return before, after
	}
	return repo, ""
}

// ThreadsArgv builds the shared graphql thread-read argv for owner/name/pr.
func ThreadsArgv(owner, name, pr, query string) []string {
	return []string{
		"api", "graphql", "--paginate",
		"-F", "owner=" + owner, "-F", "name=" + name, "-F", "pr=" + pr,
		"-f", "query=" + query,
	}
}

// ReviewThreadsArgv builds one GraphQL page read for the authoritative
// threads.json feed. cursor "" omits the endCursor variable (GraphQL's
// nullable String), so the walk needs no sentinel cursor.
func ReviewThreadsArgv(owner, name, pr, cursor string) []string {
	args := []string{
		"api", "graphql",
		flagF, "owner=" + owner, flagF, "name=" + name, flagF, "pr=" + pr,
	}
	if cursor != "" {
		args = append(args, flagF, "endCursor="+cursor)
	}
	return append(args, "-f", "query="+QueryThreadsFull)
}

// ThreadsPage decodes one authoritative thread page: its reviewThreads
// nodes, the next cursor ("" when the host says the list ended), and whether
// more pages exist. A GraphQL errors payload is an error, never an empty
// page: writing an empty feed over a failed read would read as "no prior
// threads" on a re-review.
func ThreadsPage(stdout string) (nodes []any, next string, more bool, err error) {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return nil, "", false, jq.Sealed("slurp threads", err)
	}
	threads := reviewThreadsNode(doc)
	if threads == nil {
		return nil, "", false, jq.Sealed("slurp threads", errors.New("payload carries no reviewThreads data"))
	}
	pageInfo, _ := jq.AsMap(threads["pageInfo"])
	if b, ok := pageInfo["hasNextPage"].(bool); ok && b {
		more = true
		if s, ok := pageInfo["endCursor"].(string); ok {
			next = s
		}
	}
	return jq.Elements(threads["nodes"]), next, more, nil
}

// reviewThreadsNode navigates one decoded page to its reviewThreads object.
func reviewThreadsNode(doc any) map[string]any {
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return nil
	}
	repository, ok := data["repository"].(map[string]any)
	if !ok {
		return nil
	}
	pull, ok := repository["pullRequest"].(map[string]any)
	if !ok {
		return nil
	}
	threads, ok := pull["reviewThreads"].(map[string]any)
	if !ok {
		return nil
	}
	return threads
}

// SlurpThreads decodes the paginated thread payload: concatenated JSON docs
// (one per page) slurped like `jq -s`, navigating every page to its
// reviewThreads nodes. An unreadable payload is an error.
func SlurpThreads(stdout string) ([]any, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(stdout))
	if err != nil {
		return nil, jq.Sealed("slurp threads", err)
	}
	var nodes []any
	for _, doc := range docs {
		nodes = append(nodes, pageNodes(doc)...)
	}
	return nodes, nil
}

// pageNodes navigates one page to its reviewThreads nodes.
func pageNodes(doc any) []any {
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return nil
	}
	repository, ok := data["repository"].(map[string]any)
	if !ok {
		return nil
	}
	pull, ok := repository["pullRequest"].(map[string]any)
	if !ok {
		return nil
	}
	threads, ok := pull["reviewThreads"].(map[string]any)
	if !ok {
		return nil
	}
	list, ok := threads["nodes"].([]any)
	if !ok {
		return nil
	}
	return list
}

// OpenThreadCount counts unresolved live review threads. The posture is
// fail-closed: only an object whose isResolved is exactly true reads
// resolved, so a null or other non-object node, a missing flag, or a
// non-bool flag counts as unresolved. A malformed payload therefore cannot
// hide open threads from the ready gate or the report --live probe (this
// replaces the old skip-and-zero postures, which could disagree about the
// same PR).
func OpenThreadCount(nodes []any) int {
	n := 0
	for _, node := range nodes {
		if !ThreadResolved(node) {
			n++
		}
	}
	return n
}

// MalformedThreadCount counts thread nodes whose resolution state cannot be
// read: non-object nodes and objects without an exactly-bool isResolved.
// Callers warn with this count so a fail-closed gate verdict explains
// itself instead of looking like a plain open-thread result.
func MalformedThreadCount(nodes []any) int {
	n := 0
	for _, node := range nodes {
		if _, ok := threadResolvedValue(node); !ok {
			n++
		}
	}
	return n
}

// ThreadResolved reports whether one live review thread is resolved:
// exactly true. Everything else (null, a non-object node, a missing or
// non-bool isResolved) reads unresolved, the same fail-closed posture
// [OpenThreadCount] applies.
func ThreadResolved(node any) bool {
	resolved, _ := threadResolvedValue(node)
	return resolved
}

// threadResolvedValue reads one node's isResolved flag, reporting whether
// the node carried a readable bool.
func threadResolvedValue(node any) (bool, bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return false, false
	}
	resolved, ok := m["isResolved"].(bool)
	return resolved, ok
}
