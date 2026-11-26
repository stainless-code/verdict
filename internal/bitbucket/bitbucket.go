// Package bitbucket is the Bitbucket Cloud wire contract both verbs
// share. Bitbucket Cloud ships no CLI, so every read and write is one
// authenticated `curl` against `https://api.bitbucket.org/2.0`. The
// environment gate, the endpoint URLs, and the payload fields live here
// once, so fetch and post cannot disagree about how a host is addressed or
// how a review reads back.
package bitbucket

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/curlauth"
)

// Environment names the variables every Bitbucket call reads. A Bearer
// token (`BITBUCKET_TOKEN`) takes precedence over user and password
// (`BITBUCKET_USERNAME` + `BITBUCKET_PASSWORD`), matching the fetch gate
// verbatim so one operator story spans both verbs.
const (
	EnvToken    = "BITBUCKET_TOKEN"
	EnvUsername = "BITBUCKET_USERNAME"
	EnvPassword = "BITBUCKET_PASSWORD"
)

// CodeAuthMissing is the refusal code for a missing credential. It is the
// code fetch already returns for the same variables, so one operator
// story spans both verbs.
const CodeAuthMissing = "auth-missing"

// CodeBadTarget is the refusal code for a pull position that is not a
// `workspace/slug` repo plus a numeric pull-request number. It is already
// in the closed refusal set (`no-pr-target` names the absent target; this
// one names the malformed one).
const CodeBadTarget = "bad-target"

// slash joins the workspace and slug in a `workspace/slug` repo and marks
// the segment boundary everywhere one is spliced.
const slash = "/"

const apiRoot = "https://api.bitbucket.org/2.0"

// Endpoint suffixes under the pull-scoped API base.
const (
	CommentsPath = "/comments"
	ApprovePath  = "/approve"
)

// PageSize is the page every list read requests: Bitbucket's maximum,
// which fetch's comment walk and post's read-back both use, so the two
// verbs walk one window.
const PageSize = 100

// APIBase gates the environment and returns the curl credential plus the
// authenticated API root. Both refusal sentences name the variable to
// set, and the refusal fires before any call is spawned.
func APIBase(env map[string]string) (auth curlauth.Credential, api string, err *core.RefusalError) {
	token, _ := env[EnvToken]
	user, _ := env[EnvUsername]
	pass, _ := env[EnvPassword]
	switch {
	case token != "":
		auth, cerr := curlauth.Header("Authorization", "Bearer "+token)
		if cerr != nil {
			return curlauth.Credential{}, "", &core.RefusalError{
				Code:    CodeAuthMissing,
				Message: "BITBUCKET_TOKEN carries a line break — refusing to build a curl config",
			}
		}
		return auth, apiRoot, nil
	case user != "" && pass != "":
		auth, cerr := curlauth.Basic(user, pass)
		if cerr != nil {
			return curlauth.Credential{}, "", &core.RefusalError{
				Code:    CodeAuthMissing,
				Message: "BITBUCKET_USERNAME/BITBUCKET_PASSWORD carry a line break — refusing to build a curl config",
			}
		}
		return auth, apiRoot, nil
	default:
		return curlauth.Credential{}, "", &core.RefusalError{
			Code:    CodeAuthMissing,
			Message: "bitbucket needs BITBUCKET_TOKEN (Bearer) or BITBUCKET_USERNAME+BITBUCKET_PASSWORD",
		}
	}
}

// Resolve gates the environment and returns the pull-scoped API base the
// read and write paths share: the Bitbucket Cloud REST v2.0 pull-request
// endpoint `/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{id}`.
// The repo must be one `workspace/slug` pair and the pull number must be
// numeric, so a value like `--pr 7/../../user` cannot traverse to another
// API endpoint; each segment is path-escaped before it is spliced in.
func Resolve(env map[string]string, repo, pr string) (auth curlauth.Credential, base string, err *core.RefusalError) {
	auth, api, ref := APIBase(env)
	if ref != nil {
		return curlauth.Credential{}, "", ref
	}
	ws, slug, ok := splitRepo(repo)
	if !ok {
		return curlauth.Credential{}, "", &core.RefusalError{
			Code:    CodeBadTarget,
			Message: core.Msgf("bitbucket needs a workspace/repo slug, got %q", repo),
		}
	}
	n, aerr := strconv.Atoi(pr)
	if aerr != nil || n <= 0 {
		return curlauth.Credential{}, "", &core.RefusalError{
			Code:    CodeBadTarget,
			Message: core.Msgf("bitbucket needs a numeric pull-request number, got %q", pr),
		}
	}
	return auth, api + "/repositories/" + url.PathEscape(ws) + slash + url.PathEscape(slug) +
		"/pullrequests/" + strconv.Itoa(n), nil
}

// splitRepo splits one `workspace/slug` repo into its two non-empty
// segments.
func splitRepo(repo string) (string, string, bool) {
	ws, slug, ok := strings.Cut(repo, slash)
	if !ok || ws == "" || slug == "" || strings.Contains(slug, slash) {
		return "", "", false
	}
	return ws, slug, true
}

// Auth gates the environment alone, for a caller that has not resolved a
// position yet.
func Auth(env map[string]string) *core.RefusalError {
	_, _, ref := APIBase(env)
	return ref
}

// CommentsURL is the first comments page: the URL fetch pages into
// comments.json, and the one post reads back for the duplicate check and
// the open-thread recheck.
func CommentsURL(base string) string {
	return fmt.Sprintf("%s%s?pagelen=%d", base, CommentsPath, PageSize)
}

// UserURL is the authenticated-user read.
func UserURL() string { return apiRoot + "/user" }

// Pull is the slice of the PR read both verbs need: the commit the pull
// currently points at, in the same `source.commit.hash` shape fetch
// records.
type Pull struct {
	Source struct {
		Commit struct {
			Hash string `json:"hash"`
		} `json:"commit"`
	} `json:"source"`
}

// DecodePull decodes one PR read; a malformed payload reads as the empty
// pull, which the drift check treats as an unreadable live head.
func DecodePull(raw string) Pull {
	var pull Pull
	_ = json.Unmarshal([]byte(raw), &pull) //nolint:errcheck // the empty Pull is the unreadable answer
	return pull
}

// User is the login a comment attributes to its author.
type User struct {
	Nickname    string `json:"nickname"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// Login is the nickname a bundle author carries, falling back through the
// other two spellings the host publishes.
func (u User) Login() string {
	if u.Nickname != "" {
		return u.Nickname
	}
	if u.Username != "" {
		return u.Username
	}
	return u.DisplayName
}

// DecodeUser decodes the authenticated-user read.
func DecodeUser(raw string) (User, bool) {
	var user User
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return User{}, false
	}
	return user, true
}

// commentIDBase is the decimal base a numeric comment id renders in.
const commentIDBase = 10

// Comment is one created comment row: what post reads back from an
// accepted write. List reads decode as maps, the shape the duplicate
// and recheck reads consume.
type Comment struct {
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Created  string `json:"created_on"`
	Updated  string `json:"updated_on"`
	RemoteID int    `json:"id"`
}

// DecodeComments decodes one comments page as maps, the shape the
// duplicate and recheck reads consume.
func DecodeComments(raw string) ([]map[string]any, bool) {
	var page struct {
		Values []map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		return nil, false
	}
	if page.Values == nil {
		return nil, false
	}
	return page.Values, true
}

// DecodeComment decodes one created comment.
func DecodeComment(raw string) (Comment, bool) {
	var comment Comment
	if err := json.Unmarshal([]byte(raw), &comment); err != nil {
		return Comment{}, false
	}
	return comment, true
}

// DecodePullHTML reads the PR page URL the receipt records.
func DecodePullHTML(raw string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}
	if links, ok := doc["links"].(map[string]any); ok {
		if html, ok := links["html"].(map[string]any); ok {
			if href, ok := html["href"].(string); ok {
				return href
			}
		}
	}
	return ""
}

// IsInline reports whether a decoded comment carries an anchor.
func IsInline(m map[string]any) bool {
	v, present := m["inline"]
	return present && v != nil
}

// CommentBody reads the raw body of one decoded comment row.
func CommentBody(m map[string]any) string {
	if content, ok := m["content"].(map[string]any); ok {
		if raw, ok := content["raw"].(string); ok {
			return raw
		}
	}
	return ""
}

// CommentAuthor reads the author's login from one decoded comment row.
func CommentAuthor(m map[string]any) string {
	user, ok := m["user"].(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"nickname", "username", "display_name"} {
		if s, ok := user[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// CommentID reads one decoded comment row's id as a string.
func CommentID(m map[string]any) string {
	switch v := m["id"].(type) {
	case float64:
		return strconv.FormatInt(int64(v), commentIDBase)
	case string:
		return v
	default:
		return ""
	}
}

// SameOrigin reports whether the server-supplied next URL stays on the
// API origin: a next elsewhere is refused rather than followed.
func SameOrigin(base, next string) bool {
	if next == "" {
		return false
	}
	origin, _, _ := strings.Cut(base, "/repositories/")
	if origin == "" {
		origin = apiRoot
	}
	return strings.HasPrefix(next, origin+slash) || next == origin
}
