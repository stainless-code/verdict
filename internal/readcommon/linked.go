package readcommon

import (
	"regexp"
	"strconv"
)

// closingIssueRe matches a closing keyword and the issue it names: "Fixes
// #12", "Fixes: #12", "Closes acme/app#7", "resolved #3", and the full
// "Closes https://github.com/acme/app/issues/12" URL form. It is the
// vocabulary GitHub populates closingIssuesReferences from, so the cheap local
// gate and the GraphQL read agree about what a PR links. The scheme and host
// are matched case-insensitively, and a trailing query or fragment is simply
// not part of the match.
var closingIssueRe = regexp.MustCompile(
	`(?i)\b(?:fix(?:es|ed)?|close(?:s|d)?|resolve(?:s|d)?):?\s+(?:` +
		`https?://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)` +
		`|([\w.-]+/[\w.-]+)?#(\d+))`)

// The submatch slots of closingIssueRe: the URL form names its repository and
// number first, and the `#N` form after it.
const (
	urlSlugMatch   = 1
	urlNumberMatch = 2
	slugMatch      = 3
	numberMatch    = 4
)

// LinkedIssue is one issue a PR title or body names with a closing keyword.
// Slug is the owner/repo the reference named; empty means the reviewed
// repository.
type LinkedIssue struct {
	Slug   string
	Number int
}

// ClosingIssueRefs lists the issues a PR title and body link with a closing
// keyword, in first-seen order with repeats dropped. An empty result is the
// gate that keeps a PR linking nothing from costing a host call.
func ClosingIssueRefs(title, body string) []LinkedIssue {
	var out []LinkedIssue
	seen := make(map[LinkedIssue]bool)
	for _, part := range []string{title, body} {
		for _, ref := range closingRefsIn(part) {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// closingRefsIn reads one text's closing references in first-seen order.
func closingRefsIn(text string) []LinkedIssue {
	var out []LinkedIssue
	for _, match := range closingIssueRe.FindAllStringSubmatch(text, -1) {
		slug, raw := match[slugMatch], match[numberMatch]
		if match[urlSlugMatch] != "" {
			slug, raw = match[urlSlugMatch], match[urlNumberMatch]
		}
		number, err := strconv.Atoi(raw)
		if err != nil || number <= 0 {
			continue
		}
		out = append(out, LinkedIssue{Slug: slug, Number: number})
	}
	return out
}
