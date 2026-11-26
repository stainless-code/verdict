package fetchcore

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	slugSep                 = "/"
	githubIssueParts        = 4
	githubIssueSlugParts    = 2
	githubIssueNumberIndex  = 3
	githubIssueSegmentIndex = 2
	githubIssuesSegment     = "issues"
)

// issueRef is one parsed --issue operand: the host it resolves through, the
// repository for a GitHub read, and the number.
type issueRef struct {
	kind   string
	slug   string
	number int
}

// label renders the reference the warning and the human stream name it by.
func (r issueRef) label() string {
	switch r.kind {
	case issueKindGitHub:
		return fmt.Sprintf("%s#%d", r.slug, r.number)
	default:
		return fmt.Sprintf("#%d", r.number)
	}
}

// dedupeKey is the identity a reference is deduplicated by. GitHub repository
// names are case-insensitive, so `Acme/App#12` and `acme/app#12` are one issue:
// the key folds the slug while label keeps the spelling the operator or the
// host used.
func (r issueRef) dedupeKey() string {
	return strings.ToLower(r.slug) + "#" + strconv.Itoa(r.number)
}

// issueInvalid builds the typed refusal a malformed --issue operand earns:
// --context and --spec refuse a malformed operand too, and a skip would hide
// the typo.
func issueInvalid(format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: "issue-invalid", Message: core.Msgf(format, args...)}
}

// parseIssueRefs parses every --issue operand, sorted and deduped so warning
// and host-call order never depend on argv order. A bare number resolves
// against the reviewed repository, and on any other host it refuses: there is
// no repository to join the number to.
func parseIssueRefs(refs []string, provider, repo string) ([]issueRef, *core.RefusalError) {
	sorted := slices.Clone(refs)
	slices.Sort(sorted)
	var out []issueRef
	seen := make(map[string]bool, len(sorted))
	for _, raw := range slices.Compact(sorted) {
		ref, refErr := resolveIssueRef(raw, provider, repo)
		if refErr != nil {
			return nil, refErr
		}
		key := ref.dedupeKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out, nil
}

// resolveIssueRef parses one operand and gives a bare number the reviewed
// repository, refusing it where there is no repository to join it to.
func resolveIssueRef(raw, provider, repo string) (issueRef, *core.RefusalError) {
	ref, refErr := parseIssueRef(raw)
	if refErr != nil {
		return issueRef{}, refErr
	}
	switch {
	case ref.kind != issueKindBare:
		return ref, nil
	case provider != providerGitHub:
		return issueRef{}, issueInvalid(msgIssueBareProvider, raw)
	default:
		return issueRef{kind: issueKindGitHub, slug: repo, number: ref.number}, nil
	}
}

// parseIssueRef parses one operand into its shape. Three shapes resolve; every
// other string is issue-invalid. A third-party tracker identifier is not one of
// them: its text reaches a review through --spec.
func parseIssueRef(raw string) (issueRef, *core.RefusalError) {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return issueRef{}, issueInvalid(msgIssueWhitespace, raw)
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "https://github.com/"):
		return parseGitHubIssueURL(raw)
	case strings.Contains(raw, "://"):
		return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
	}
	if slug, tail, ok := strings.Cut(raw, "#"); ok {
		return parseNumberedIssueRef(raw, slug, tail)
	}
	if number, ok := positiveNumber(raw); ok {
		return issueRef{kind: issueKindBare, number: number}, nil
	}
	return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
}

// parseNumberedIssueRef parses the `#N` and `owner/repo#N` shapes.
func parseNumberedIssueRef(raw, slug, tail string) (issueRef, *core.RefusalError) {
	number, ok := positiveNumber(tail)
	if !ok {
		return issueRef{}, issueInvalid(msgIssueNeedsNumber, raw)
	}
	if slug == "" {
		return issueRef{kind: issueKindBare, number: number}, nil
	}
	if !validSlug(slug) {
		return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
	}
	return issueRef{kind: issueKindGitHub, slug: slug, number: number}, nil
}

// parseGitHubIssueURL reads an https://github.com/<owner>/<repo>/issues/<n>
// URL.
func parseGitHubIssueURL(raw string) (issueRef, *core.RefusalError) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
	}
	parts := strings.Split(strings.Trim(parsed.Path, slugSep), slugSep)
	if len(parts) != githubIssueParts || parts[githubIssueSegmentIndex] != githubIssuesSegment {
		return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
	}
	slug := strings.Join(parts[:githubIssueSlugParts], slugSep)
	if !validSlug(slug) {
		return issueRef{}, issueInvalid(msgIssueUnrecognized, raw)
	}
	number, ok := positiveNumber(parts[githubIssueNumberIndex])
	if !ok {
		return issueRef{}, issueInvalid(msgIssueNeedsNumber, raw)
	}
	return issueRef{kind: issueKindGitHub, slug: slug, number: number}, nil
}

// positiveNumber reads a positive decimal issue number: digits only, no sign,
// and greater than zero.
func positiveNumber(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	number, err := strconv.Atoi(s)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// validSlug reports the owner/repo shape a GitHub reference must carry.
func validSlug(slug string) bool {
	owner, name, ok := strings.Cut(slug, slugSep)
	return ok && validSlugPart(owner) && validSlugPart(name)
}

// validSlugPart reports one repository path segment.
func validSlugPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	for _, r := range part {
		if !slugRune(r) {
			return false
		}
	}
	return true
}

// slugRune reports the runes GitHub allows in an owner or repository name.
func slugRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.':
		return true
	default:
		return false
	}
}
