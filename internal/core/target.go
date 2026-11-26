package core

import (
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Refusal codes of the target resolver: a pasted URL verdict will not turn
// into a target, and an omitted `<owner/repo> <pr>` it cannot infer.
const (
	CodeBadTargetURL     = "bad-target-url"
	CodeTargetUnresolved = "target-unresolved"
)

// Target is one review target: the host verdict talks to, the repository
// path that host names it by, and the pull or merge request number. Host is
// the URL's authority ([URLAuthority]), the host every call for the target is
// pinned to.
type Target struct {
	Provider string
	Host     string
	Repo     string
	Pr       string
}

// IsTargetURL reports whether a positional is a pasted URL instead of an
// `<owner/repo>` or a number: a repository path never carries "://".
func IsTargetURL(s string) bool { return strings.Contains(s, "://") }

// prNumber is a positive decimal with no leading zero. Nine digits keep the
// number inside every host's integer and out of any bundle-name edge.
var prNumber = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

// ValidPrNumber reports whether s is a pull request number the resolver would
// accept from a URL.
func ValidPrNumber(s string) bool { return prNumber.MatchString(s) }

// publicHosts is the one table of code hosts verdict knows by name. Remote
// inference and URL parsing both read it, so a host added here is understood
// by both.
var publicHosts = map[string]string{
	"github.com": providerGitHub, "gitlab.com": providerGitLab, "gitcafe.com": providerGitCafe,
	"codeberg.org": providerForgejo, "bitbucket.org": providerBitbucket,
	"dev.azure.com": providerAzure, "ssh.dev.azure.com": providerAzure,
}

// configuredHosts are the self-hosted mappings an operator opted into through
// the environment; a URL on any other host is refused, never guessed.
var configuredHosts = []struct{ key, provider string }{
	{"GITLAB_HOST", providerGitLab}, {"FORGEJO_HOST", providerForgejo},
}

const envAzureOrg = "AZURE_DEVOPS_ORG"

// ParseTargetURL turns a pull or merge request URL into its target. The
// host picks the provider (a public host, or a self-hosted one the
// environment already maps); the path must match that host's one route shape,
// with at most one known view segment after the number (`/files`, `/diffs`).
// Query and fragment are ignored. Anything else refuses instead of guessing:
// a different route, an unmapped host, credentials in the URL, a segment that
// is empty, dot-relative, encoded, or flag-shaped, or a non-numeric number.
func ParseTargetURL(raw string, env map[string]string) (Target, *RefusalError) {
	u, ref := parseURLShape(raw)
	if ref != nil {
		return Target{}, ref
	}
	provider, ref := urlProvider(u, env)
	if ref != nil {
		return Target{}, ref
	}
	segs, ref := urlSegments(u.EscapedPath(), provider)
	if ref != nil {
		return Target{}, ref
	}
	repo, pr, ref := routeTarget(provider, segs, env)
	if ref != nil {
		return Target{}, ref
	}
	return Target{Provider: provider, Host: URLAuthority(u), Repo: repo, Pr: pr}, nil
}

// TargetUnresolved is the refusal for an omitted target verdict could not
// infer; the message names the exact arguments to pass.
func TargetUnresolved(format string, a ...any) *RefusalError {
	return &RefusalError{Code: CodeTargetUnresolved, Message: Msgf(format, a...)}
}

func badURL(format string, a ...any) *RefusalError {
	return &RefusalError{Code: CodeBadTargetURL, Message: Msgf(format, a...)}
}

// parseURLShape accepts one http(s) URL with a host and no credentials.
func parseURLShape(raw string) (*url.URL, *RefusalError) {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) >= 0 {
		return nil, badURL("the pull request URL carries whitespace, a control byte, or a backslash")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Hostname() == "" {
		return nil, badURL("the pull request URL is malformed (want https://<host>/<owner>/<repo>/pull/<number>)")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, badURL("the pull request URL must be http or https, got scheme %q", u.Scheme)
	}
	if u.User != nil {
		return nil, badURL("the pull request URL carries credentials; pass it without user information")
	}
	return u, nil
}

// urlProvider maps the URL's host to a provider through the shared host
// table and the environment's configured hosts.
func urlProvider(u *url.URL, env map[string]string) (string, *RefusalError) {
	host := strings.ToLower(u.Hostname())
	chosen, ref := hostProvider(u, env)
	switch {
	case ref != nil:
		return "", ref
	case chosen == "":
		return "", badURL("host %q is not a known code host; for a self-hosted GitLab or Forgejo set "+
			"GITLAB_HOST or FORGEJO_HOST, otherwise pass <owner/repo> <pr> --provider <name>", host)
	case chosen == providerGitCafe:
		return "", badURL("verdict has no pull request URL shape for gitcafe; pass <owner/repo> <pr> --provider gitcafe")
	}
	return chosen, nil
}

// hostProvider is the table lookup: a public host on its default port, or a
// configured host matched on host and port. Two mappings for one host refuse.
func hostProvider(u *url.URL, env map[string]string) (string, *RefusalError) {
	chosen := ""
	if u.Port() == "" {
		chosen = publicHosts[strings.ToLower(u.Hostname())]
	}
	for _, c := range configuredHosts {
		if configuredAuthority(env[c.key]) != strings.ToLower(u.Host) {
			continue
		}
		if chosen != "" && chosen != c.provider {
			return "", badURL("host %q maps to two providers (%s and %s); pass <owner/repo> <pr> --provider <name>",
				u.Hostname(), chosen, c.provider)
		}
		chosen = c.provider
	}
	return chosen, nil
}

// configuredAuthority is a configured host's lowercase host[:port], matched
// whole against the URL's so a configured port is never ignored.
func configuredAuthority(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// Route geometry. A route is the slash-separated path after the host; the
// offsets below are the positions the shapes in the manual fix.
const (
	slash        = "/"
	gitlabDash   = "-"
	ownerRepoLen = 2 // <owner>/<repo>
	markerAt     = 2 // <owner>/<repo>/<marker>
	numberAt     = 3 // <owner>/<repo>/<marker>/<number>
	tailAt       = 4 // the optional view segment after the number
	azureSegs    = 6 // <org>/<project>/_git/<repo>/pullrequest/<number>
	azureOrg     = 0
	azureProject = 1
	azureGitAt   = 2
	azureRepo    = 3
	azurePRAt    = 4
	azureNumber  = 5
	viewCommits  = "commits"
)

// urlSegments splits the escaped path into decoded segments.
func urlSegments(escaped, provider string) ([]string, *RefusalError) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(escaped, slash), slash)
	if trimmed == "" {
		return nil, badURL("the pull request URL names no repository")
	}
	segs := strings.Split(trimmed, slash)
	for i, seg := range segs {
		dec, ref := decodeSegment(seg, provider)
		if ref != nil {
			return nil, ref
		}
		segs[i] = dec
	}
	return segs, nil
}

// decodeSegment checks one path segment. A host that names repositories by
// plain slugs never percent-encodes one, so an encoded byte there is refused;
// Azure project names may carry encoded spaces.
func decodeSegment(seg, provider string) (string, *RefusalError) {
	dec, err := url.PathUnescape(seg)
	switch {
	case err != nil || seg == "" || dec == "." || dec == "..":
		return "", badURL("the pull request URL has an empty, dot-relative, or malformed path segment")
	case strings.ContainsAny(dec, `/\`) || strings.IndexFunc(dec, unicode.IsControl) >= 0:
		return "", badURL("the pull request URL has a path segment with a separator or control byte")
	case dec != seg && provider != providerAzure:
		return "", badURL("the pull request URL has a percent-encoded path segment %q", seg)
	case strings.HasPrefix(dec, "-") && dec != gitlabDash:
		return "", badURL("the pull request URL has a path segment starting with '-'")
	}
	return dec, nil
}

// routeTarget matches the segments against the provider's one route shape.
func routeTarget(provider string, segs []string, env map[string]string) (repo, pr string, ref *RefusalError) {
	switch provider {
	case providerGitHub:
		return fixedRoute(segs, "pull", []string{"files", viewCommits, "checks"})
	case providerForgejo:
		return fixedRoute(segs, "pulls", []string{"files", viewCommits})
	case providerBitbucket:
		return fixedRoute(segs, "pull-requests", []string{"diff", viewCommits, "overview"})
	case providerGitLab:
		return gitlabRoute(segs)
	default:
		return azureRoute(segs, env)
	}
}

// fixedRoute is `<owner>/<repo>/<marker>/<number>[/<view>]`.
func fixedRoute(segs []string, marker string, views []string) (repo, pr string, ref *RefusalError) {
	if len(segs) <= numberAt || segs[markerAt] != marker {
		return "", "", badURL("the URL is not a pull request route (want .../<owner>/<repo>/%s/<number>)", marker)
	}
	if ref := tailRoute(segs[tailAt:], views); ref != nil {
		return "", "", ref
	}
	pr, ref = numberOf(segs[numberAt])
	return strings.Join(segs[:ownerRepoLen], slash), pr, ref
}

// gitlabRoute is
// `<group>[/<subgroup>...]/<project>/-/merge_requests/<iid>[/<view>]`, or the
// same without the dash.
func gitlabRoute(segs []string) (repo, pr string, ref *RefusalError) {
	at := slices.Index(segs, "merge_requests")
	if at < 0 || at+1 >= len(segs) {
		return "", "", badURL("the URL is not a merge request route (want .../<group>/<project>/-/merge_requests/<number>)")
	}
	project := strings.TrimSuffix(strings.Join(segs[:at], slash), slash+gitlabDash)
	if project == gitlabDash || slices.Contains(strings.Split(project, slash), gitlabDash) ||
		len(strings.Split(project, slash)) < ownerRepoLen {
		return "", "", badURL("the merge request URL needs a <group>/<project> path before /-/merge_requests/")
	}
	if ref := tailRoute(segs[at+2:], []string{"diffs", viewCommits, "pipelines", "changes"}); ref != nil {
		return "", "", ref
	}
	pr, ref = numberOf(segs[at+1])
	return project, pr, ref
}

// azureRoute is `<org>/<project>/_git/<repo>/pullrequest/<number>`; the org
// must be the one AZURE_DEVOPS_ORG already names, because the verbs read the
// organization from the environment alone.
func azureRoute(segs []string, env map[string]string) (repo, pr string, ref *RefusalError) {
	if len(segs) != azureSegs || segs[azureGitAt] != "_git" || segs[azurePRAt] != "pullrequest" {
		return "", "", badURL("the URL is not an Azure DevOps pull request route " +
			"(want .../<org>/<project>/_git/<repo>/pullrequest/<number>)")
	}
	org := strings.ToLower(path.Base(strings.TrimRight(configuredPath(env[envAzureOrg]), slash)))
	if org == "" || org == "." || org != strings.ToLower(segs[azureOrg]) {
		return "", "", badURL("the URL names Azure organization %q; set AZURE_DEVOPS_ORG=https://dev.azure.com/%s "+
			"to match, or pass <project>/<repo> <pr> --provider azure", segs[azureOrg], segs[azureOrg])
	}
	pr, ref = numberOf(segs[azureNumber])
	return segs[azureProject] + slash + segs[azureRepo], pr, ref
}

func configuredPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Path
}

// tailRoute allows nothing after the number, or exactly one known view.
func tailRoute(tail, views []string) *RefusalError {
	if len(tail) == 0 || (len(tail) == 1 && slices.Contains(views, tail[0])) {
		return nil
	}
	return badURL("the URL continues past the pull request number with %q; "+
		"keep only the number, or a view such as /%s", strings.Join(tail, slash), views[0])
}

func numberOf(seg string) (string, *RefusalError) {
	if !prNumber.MatchString(seg) {
		return "", badURL("the pull request number %q is not a positive integer", seg)
	}
	return seg, nil
}

// RemoteTarget reads the `origin` remote URL into its provider and repository
// path: the offline half of target inference. It reports provider-unknown for
// a host verdict cannot map and bad-target-url for a path that is no
// repository. The message never carries the remote, which may hold a token.
func RemoteTarget(raw string, env map[string]string) (provider, repo string, ref *RefusalError) {
	provider = providerFromRemote(raw, env)
	if provider == "" {
		return "", "", &RefusalError{Code: codeProviderUnknown, Message: msgProviderUnknown}
	}
	if !strings.Contains(raw, "://") {
		raw = scpRemote(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", badURL("the origin remote is not a repository URL")
	}
	segs := strings.Split(strings.Trim(u.Path, slash), slash)
	segs[len(segs)-1] = strings.TrimSuffix(segs[len(segs)-1], ".git")
	repo, ok := remoteRepo(provider, segs)
	if !ok {
		return "", "", badURL("the origin remote does not name a %s repository path", provider)
	}
	return provider, repo, nil
}

// remoteRepo shapes a remote's path segments into the repository spelling the
// provider's verbs take.
func remoteRepo(provider string, segs []string) (string, bool) {
	if slices.ContainsFunc(segs, badRemoteSegment) {
		return "", false
	}
	switch provider {
	case providerGitLab:
		return strings.Join(segs, slash), len(segs) >= ownerRepoLen
	case providerAzure:
		return azureRemoteRepo(segs)
	default:
		return strings.Join(segs, slash), len(segs) == ownerRepoLen
	}
}

func badRemoteSegment(s string) bool {
	return s == "" || s == "." || s == ".." || strings.ContainsAny(s, `%\`) || strings.HasPrefix(s, "-")
}

// azureRemoteRepo reads the project/repository pair out of an https remote
// (<org>/<project>/_git/<repo>) or an ssh one (v3/<org>/<project>/<repo>).
func azureRemoteRepo(segs []string) (string, bool) {
	const remoteSegs = 4
	switch {
	case len(segs) == remoteSegs && segs[azureGitAt] == "_git":
		return segs[azureProject] + slash + segs[azureRepo], true
	case len(segs) == remoteSegs && segs[0] == "v3":
		// The project sits where the https shape keeps "_git".
		return segs[azureGitAt] + slash + segs[azureRepo], true
	}
	return "", false
}

// RemoteHost is the web host of an origin remote, the authority a bundle's
// review URL names and every host call for the target is pinned to. An http
// or https remote keeps a non-default port ([URLAuthority]); an ssh remote's
// port is the ssh daemon's, not the web host's, so it reads as the bare
// hostname, and Azure's ssh host folds into dev.azure.com. It is "" for a
// remote verdict cannot read.
func RemoteHost(raw string) string {
	host := remoteHost(raw)
	if host == "ssh.dev.azure.com" {
		return "dev.azure.com"
	}
	if u, err := url.Parse(raw); host != "" && err == nil && (u.Scheme == schemeHTTPS || u.Scheme == schemeHTTP) {
		return URLAuthority(u)
	}
	return host
}
