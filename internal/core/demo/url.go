package democore

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// codeBadTarget is the shared refusal for a demo positional that is not a
// public GitHub pull-request URL. It is the code `fetch` already uses for a
// target it will not read, so the remedy hint and the envelope shape are the
// shipped ones.
const codeBadTarget = "bad-target"

// githubHost is the only host the anonymous read speaks: an enterprise or a
// sibling host needs a provider credential the demo does not have.
const githubHost = "github.com"

// publicURLShape names the one accepted spelling in every refusal, so the
// message teaches the invocation instead of only rejecting one.
const publicURLShape = "https://github.com/<owner>/<repo>/pull/<n>"

// pullSegments is the required path shape after the host: owner, repo, "pull",
// number.
const pullSegments = 4

// pullMarker is the path segment naming a pull request (never /issues/<n>).
const pullMarker = "pull"

// The four path segments one pull-request URL carries, in order.
const (
	pullOwner = iota
	pullRepo
	pullKind
	pullNumber
)

// pullSuffixes are the pull-request sub-pages an operator copies out of the
// browser: they name the same pull request, so they are accepted as a suffix.
var pullSuffixes = []string{"files", "commits", "checks"}

// Target is one parsed public pull request.
type Target struct {
	URL   string
	Owner string
	Repo  string
	Pr    string
}

// Slug is the owner/repo pair every downstream verb takes positionally.
func (t Target) Slug() string { return t.Owner + "/" + t.Repo }

// ParseTarget parses `https://github.com/<owner>/<repo>/pull/<n>`, the demo's
// optional positional. Everything else is refused with the wanted shape named:
// a demo that silently repaired its input (a missing scheme, a bare
// owner/repo, an issue URL) would teach an invocation the other verbs reject.
func ParseTarget(raw string) (Target, *core.RefusalError) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Target{}, badTarget("empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return Target{}, badTarget("not a URL")
	}
	if u.Scheme != "https" {
		return Target{}, badTarget("want an https:// URL")
	}
	if ref := hostRefusal(u); ref != nil {
		return Target{}, ref
	}
	if hasEncodedPath(u) {
		return Target{}, badTarget("no percent escapes in the path")
	}
	target, ok := parsePullPath(u.Path)
	if !ok {
		return Target{}, badTarget("want the path " + "/<owner>/<repo>/pull/<n>")
	}
	target.URL = trimmed
	return target, nil
}

// hostRefusal refuses every host the anonymous read cannot speak: a credential
// in the URL, a non-default port, or any host but github.com itself.
func hostRefusal(u *url.URL) *core.RefusalError {
	if u.User != nil || u.Port() != "" {
		return badTarget("no userinfo or port in the host")
	}
	if !strings.EqualFold(u.Host, githubHost) {
		return badTarget("want " + githubHost + " (this demo reads public GitHub pull requests only)")
	}
	return nil
}

// parsePullPath reads the owner/repo/pull/n path, with the optional browser
// sub-page and no other segment.
func parsePullPath(path string) (Target, bool) {
	segments := splitPath(path)
	if len(segments) == pullSegments+1 && isPullSuffix(segments[pullSegments]) {
		segments = segments[:pullSegments]
	}
	if len(segments) != pullSegments {
		return Target{}, false
	}
	owner, name := segments[pullOwner], segments[pullRepo]
	marker, number := segments[pullKind], segments[pullNumber]
	if !validRepoName(owner) || !validRepoName(name) || marker != pullMarker || !validPrNumber(number) {
		return Target{}, false
	}
	return Target{Owner: owner, Repo: name, Pr: number}, true
}

// hasEncodedPath reports whether the URL spelled any part of its path with a
// percent escape. Every accepted segment is plain ASCII, so an escape is never
// part of a pull request a reader copied out of the browser — and Go decodes
// escapes into Path before the segment check, so refusing the raw spelling is
// the belt to that braces: an escaped "/", "..", or space never reaches a
// provider argv, and the decision does not rest on how the parser decodes.
// RawPath holds the original spelling only when it differs from the canonical
// one, so the escaped form is checked too.
func hasEncodedPath(u *url.URL) bool {
	return strings.Contains(u.RawPath, "%") || strings.Contains(u.EscapedPath(), "%")
}

// validRepoName reports whether s is one owner or repository segment as the
// providers spell them: ASCII letters, digits, dot, dash, or underscore, never
// "." or "..", and never starting with a dash (which reads as a flag to gh and
// curl). A space, a slash, or any other character refuses the URL instead of
// being interpolated into an API path.
func validRepoName(s string) bool {
	if !segmentShaped(s) {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// splitPath splits a URL path into non-empty segments, so a trailing slash
// reads the same as no slash.
func splitPath(path string) []string {
	var out []string
	for segment := range strings.SplitSeq(strings.Trim(path, "/"), "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// isPullSuffix reports whether segment names one of the accepted sub-pages.
func isPullSuffix(segment string) bool {
	return slices.Contains(pullSuffixes, segment)
}

// validPrNumber reports whether s is a pull-request number: a decimal with no
// leading zero and a non-zero value.
func validPrNumber(s string) bool {
	if s == "" || s[0] == '0' {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(s, decimalBase, numberBits)
	return err == nil
}

// segmentShaped reports whether s may name a segment at all: non-empty, not a
// dot entry, and not starting with a dash.
func segmentShaped(s string) bool {
	return s != "" && s != "." && s != ".." && s[0] != '-'
}

// badTarget renders the one bad-target refusal with the shape named.
func badTarget(why string) *core.RefusalError {
	return &core.RefusalError{Code: codeBadTarget, Message: core.Msgf("demo takes a public GitHub pull request URL: "+
		"%s (want "+publicURLShape+")", why)}
}
