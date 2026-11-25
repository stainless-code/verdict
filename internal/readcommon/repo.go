package readcommon

import (
	urlpkg "net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// slashSep is the URL path separator the project-path walk splits on.
const slashSep = "/"

// BundleProject names the repository a bundle belongs to: a local
// source.json's repo, else the review URL's project path. GitHub and Forgejo
// put owner/repo before /pull/, /pulls/, or /issues/; GitLab puts every
// subgroup before /-/. It returns "" when the bundle carries neither (legacy
// or hand-made bundles), so callers can treat an unknown project as no claim.
func BundleProject(dir string) string {
	if repo := JSONStr(dir+"/source.json", "repo"); repo != "" {
		return repo
	}
	rawURL := OpenBundle(dir).StrField("url")
	if rawURL == "" {
		return ""
	}
	parsed, err := urlpkg.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, slashSep), slashSep)
	if len(parts) < 2 {
		return ""
	}
	if project, ok := azureProject(parts); ok {
		return project
	}
	if end := projectPathEnd(parts); end > 0 {
		return path.Join(parts[:end]...)
	}
	return path.Join(parts[:2]...)
}

// BundleHost names the review URL's authority ([core.URLAuthority]: a
// non-default port kept, so two hosts that differ by port never match), or ""
// when the bundle carries no review URL: a local bundle names no host, so two
// local bundles never mismatch on it.
func BundleHost(dir string) string {
	rawURL := OpenBundle(dir).StrField("url")
	if rawURL == "" {
		return ""
	}
	parsed, err := urlpkg.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return core.URLAuthority(parsed)
}

// azureProject reads Azure DevOps' <project>/<repo> out of
// .../<project>/_git/<repo>/pullrequest/<n>: the organization sits above the
// project there, so the first-two-segments fallback would name the wrong pair.
func azureProject(parts []string) (string, bool) {
	at := slices.Index(parts, "_git")
	if at < 1 || at+1 >= len(parts) {
		return "", false
	}
	return parts[at-1] + slashSep + parts[at+1], true
}

// projectPathEnd finds the segment that ends the project path: GitLab's /-/
// separator or another host's PR route with its numeric id.
func projectPathEnd(parts []string) int {
	for i := 2; i+1 < len(parts); i++ {
		if isProjectSeparator(parts, i) {
			return i
		}
	}
	return 0
}

// isProjectSeparator reports whether parts[i] starts a host route tail.
func isProjectSeparator(parts []string, i int) bool {
	if parts[i] == "-" {
		return parts[i+1] == "merge_requests"
	}
	if !repoRouteSegment(parts[i]) {
		return false
	}
	_, err := strconv.Atoi(parts[i+1])
	return err == nil
}

// repoRouteSegment reports one host route segment that ends the project path.
func repoRouteSegment(segment string) bool {
	switch segment {
	case "pull", "pulls", "issues", "merge_requests":
		return true
	default:
		return false
	}
}
