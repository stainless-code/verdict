package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// issueSourceURL is the canonical source the bundle cites for one GitHub
// issue: the host's own URL when it gave one, else the deterministic spelling
// that carries the repository and the number.
func issueSourceURL(slug string, number int, hostURL string) string {
	if hostURL != "" {
		return hostURL
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", slug, number)
}

// closingIssues reads the issues GitHub itself treats as closed by the PR.
// text carries the host's streams for the reason mapping; it is never written
// into a warning.
func closingIssues(ctx context.Context, ctx2 *fetchRunCtx) (nodes []any, more bool, text string, err error) {
	owner, name := readcommon.SplitOwner(ctx2.repo)
	res, err := ctx2.runner.ghFetch(ctx, readcommon.ClosingIssuesArgv(owner, name, ctx2.pr)...)
	text = issueReasonText(res)
	if err != nil {
		return nil, false, text, err
	}
	nodes, more, derr := readcommon.ClosingIssuesPage(res.Stdout)
	if derr != nil {
		return nil, false, text, jq.Sealed("closing issues", derr)
	}
	return nodes, more, text, nil
}

// readGitHubIssue reads one issue through the REST API: its title, body, and
// the canonical URL the envelope cites as the source.
func readGitHubIssue(ctx context.Context, s *session, ref issueRef) issueRead {
	res, err := s.ghFetch(ctx,
		ghCmdAPI, apiReposPrefix+ref.slug+"/issues/"+strconv.Itoa(ref.number))
	text := issueReasonText(res)
	if err == nil {
		if doc, ok := decodeGitHubIssue(res.Stdout, ref); ok {
			return issueRead{doc: doc}
		}
		err = errUnreadableIssue
	}
	return issueRead{warn: fmt.Sprintf(msgIssueUnavailable, issueReason(text, err), ref.label())}
}

// errUnreadableIssue marks a payload that carries no issue: the reason
// mapping reads it as a host error, never as a missing issue.
var errUnreadableIssue = errors.New("unreadable issue payload")

// decodeGitHubIssue reads one REST issue payload. A null body reads "" so the
// composed document is the title line alone.
func decodeGitHubIssue(stdout string, ref issueRef) (issueDoc, bool) {
	payload, err := jq.DecodeObject([]byte(stdout))
	if err != nil || payload[keyTitle] == nil {
		return issueDoc{}, false
	}
	number := ref.number
	if n, ok := jq.IntOf(payload[keyNumber]); ok && n > 0 {
		number = n
	}
	return issueDoc{
		source: issueSourceURL(ref.slug, number, jq.StrOf(payload["html_url"])),
		label:  ref.label(),
		title:  jq.StrOf(payload[keyTitle]),
		body:   jq.StrOf(payload[keyBody]),
	}, true
}

// linkedCandidate is one issue the PR links: a GraphQL node when GitHub
// reported it, else a keyword-only reference that costs one REST read.
type linkedCandidate struct {
	node map[string]any
	ref  issueRef
}

// source is the canonical URL the candidate's document will cite: a GraphQL
// node names its own, and a keyword-only reference's is the deterministic
// spelling the REST read would land on. It is known before any host call, so
// an issue the run already carries never costs a second read.
func (c *linkedCandidate) source() string {
	if c.node == nil {
		return issueSourceURL(c.ref.slug, c.ref.number, "")
	}
	return issueSourceURL(c.ref.slug, c.ref.number, jq.StrOf(c.node["url"]))
}

// read resolves one candidate: a GraphQL node already carries its document,
// and a keyword-only reference reuses the explicit read.
func (c *linkedCandidate) read(ctx context.Context, ctx2 *fetchRunCtx) issueRead {
	if c.node == nil {
		return readGitHubIssue(ctx, ctx2.runner, c.ref)
	}
	return issueRead{doc: issueDoc{
		source: c.source(),
		label:  c.ref.label(),
		title:  jq.StrOf(c.node[keyTitle]),
		body:   jq.StrOf(c.node[keyBody]),
	}}
}

// linkedCandidates unions the GraphQL nodes with the keyword references,
// deduped by the case-folded repository and the number and sorted by number so
// both the host-call order and the bundle order are deterministic.
func linkedCandidates(gate []readcommon.LinkedIssue, nodes []any, repo string) []linkedCandidate {
	byKey := make(map[string]linkedCandidate, len(nodes)+len(gate))
	addGraphQLCandidates(byKey, nodes, repo)
	addKeywordCandidates(byKey, gate, repo)
	out := make([]linkedCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		out = append(out, candidate)
	}
	slices.SortFunc(out, func(a, b linkedCandidate) int {
		if a.ref.number != b.ref.number {
			return a.ref.number - b.ref.number
		}
		return strings.Compare(a.ref.slug, b.ref.slug)
	})
	return out
}

// addGraphQLCandidates records the issues GitHub itself reports.
func addGraphQLCandidates(byKey map[string]linkedCandidate, nodes []any, repo string) {
	for _, node := range nodes {
		candidate, ok := nodeCandidate(node, repo)
		if !ok {
			continue
		}
		byKey[candidate.ref.dedupeKey()] = candidate
	}
}

// addKeywordCandidates records the keyword-only references GraphQL did not
// carry, so the union loses nothing and each extra costs one REST read. A
// reference whose slug is not a repository shape is dropped: a PR body is
// attacker-influenced, and `../..` must never reach a host argv.
func addKeywordCandidates(byKey map[string]linkedCandidate, gate []readcommon.LinkedIssue, repo string) {
	for _, ref := range gate {
		slug := ref.Slug
		if slug == "" {
			slug = repo
		}
		if !validSlug(slug) {
			continue
		}
		candidate := linkedCandidate{ref: issueRef{kind: issueKindGitHub, slug: slug, number: ref.Number}}
		key := candidate.ref.dedupeKey()
		if _, seen := byKey[key]; seen {
			continue
		}
		byKey[key] = candidate
	}
}

// nodeCandidate reads one closingIssuesReferences node. A null node, or one
// without a number, is dropped: the read has nothing to cite. A repository
// name that is not a slug leaves the reviewed repository in place, so a host
// answer can never smuggle a path into a reference.
func nodeCandidate(node any, repo string) (linkedCandidate, bool) {
	fields, ok := jq.AsMap(node)
	if !ok {
		return linkedCandidate{}, false
	}
	number, ok := jq.IntOf(fields[keyNumber])
	if !ok || number <= 0 {
		return linkedCandidate{}, false
	}
	slug := repo
	if repository, ok := jq.AsMap(fields["repository"]); ok {
		if name := jq.StrOf(repository["nameWithOwner"]); validSlug(name) {
			slug = name
		}
	}
	return linkedCandidate{node: fields, ref: issueRef{kind: issueKindGitHub, slug: slug, number: number}}, true
}

// issueReasonText renders the bytes a reason is read from: the host's own
// streams, which the warning never carries.
func issueReasonText(res core.Result) string {
	return res.Stderr + "\n" + res.Stdout
}

// issueReason maps a failed read onto its outcome class. Only the class
// travels: a host error body can echo the request, so it is read here and
// never written into a warning.
func issueReason(text string, err error) string {
	if _, ok := errors.AsType[*core.MissingToolError](err); ok {
		return reasonMissingTool
	}
	lowered := strings.ToLower(text)
	switch {
	case strings.Contains(lowered, "404"), strings.Contains(lowered, "not_found"),
		strings.Contains(lowered, "not found"):
		return reasonNotFound
	// Rate-limit indicators come first: a throttled call answers 403 (or 429)
	// with a body that also says "Forbidden", and reading it as unauthorized
	// would send the operator after a credential that is already fine.
	case strings.Contains(lowered, "429"), strings.Contains(lowered, "rate_limit"),
		strings.Contains(lowered, "rate limit"):
		return reasonRateLimited
	case strings.Contains(lowered, "401"), strings.Contains(lowered, "403"),
		strings.Contains(lowered, "unauthorized"), strings.Contains(lowered, "forbidden"):
		return reasonUnauthorized
	default:
		return reasonHostError
	}
}
