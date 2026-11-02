package readcommon

import (
	"bytes"
	"context"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// GraphQL queries, byte-identical to the shell's -f query= arguments
// (indentation included: the eval stub logs argv verbatim, so the parity
// harness compares these bytes between implementations).
const (
	// QueryLiveCount is live_open_thread_count's resolution-only read.
	QueryLiveCount = `
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }`
	// QueryCalibrate is the calibrate join's thread read with comments.
	QueryCalibrate = `
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved comments(first:30) { nodes { body path } } }
          }
        }
      }
    }`
	// QueryReady is ready's resolution-only read.
	QueryReady = `
    query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
      repository(owner:$owner, name:$name) {
        pullRequest(number:$pr) {
          reviewThreads(first:100, after:$endCursor) {
            nodes { isResolved }
          }
        }
      }
    }`
)

// GhResult carries a gh call's captured streams.
type GhResult struct {
	Stdout string
	Stderr string
}

// Gh runs gh with exactly the given argv, capturing both streams. A missing
// binary or a failing call returns the sealed error with stderr attached;
// callers map failures to the verb's warn-or-refuse posture. Callers needing
// the stub-log comparison pass the same argv the shell passes (O5).
func Gh(ctx context.Context, args ...string) (GhResult, error) {
	return ghRun(ctx, "", args...)
}

// GhStdin runs gh with exactly the given argv and stdin body, capturing
// both streams. The post verb's reviews POST pipes the payload the way the
// shell's `echo "$payload" | gh ... --input -` does (payload plus one
// newline); the stub-log comparison covers those bytes on both sides.
func GhStdin(ctx context.Context, stdin string, args ...string) (GhResult, error) {
	return ghRun(ctx, stdin, args...)
}

// ghRun is the shared gh spawn: stdin carries the POST payload (payload
// plus one newline, exactly what the shell's echo pipes); reads pass "".
func ghRun(ctx context.Context, stdin string, args ...string) (GhResult, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(stdin)
	res := GhResult{}
	if err := cmd.Run(); err != nil {
		res.Stdout = stdout.String()
		res.Stderr = stderr.String()
		return res, jq.Sealed("gh", err)
	}
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res, nil
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

// SlurpThreads decodes the paginated thread payload: concatenated JSON docs
// (one per page) slurped like `jq -s`, navigating every page to its
// reviewThreads nodes. An unreadable payload is an error, mirroring the
// shell's `jq -s` failure.
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

// LiveOpenThreadCount ports live_open_thread_count: the count of unresolved
// live threads, or an error when the lookup fails. Callers write res.Stderr
// to the probe err file before mapping the error to their posture.
func LiveOpenThreadCount(ctx context.Context, repo, pr string) (int, GhResult, error) {
	owner, name := SplitOwner(repo)
	res, err := Gh(ctx, ThreadsArgv(owner, name, pr, QueryLiveCount)...)
	if err != nil {
		return 0, res, err
	}
	nodes, err := SlurpThreads(res.Stdout)
	if err != nil {
		return 0, res, err
	}
	return countOpenNodes(nodes), res, nil
}

// countOpenNodes counts unresolved live threads, skipping exotic nodes.
func countOpenNodes(nodes []any) int {
	n := 0
	for _, node := range nodes {
		m, ok := node.(map[string]any)
		if !ok {
			continue
		}
		if resolved, ok := m["isResolved"].(bool); !ok || !resolved {
			n++
		}
	}
	return n
}
