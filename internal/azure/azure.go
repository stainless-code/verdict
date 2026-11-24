// Package azure is the Azure DevOps REST 7.1 wire contract both verbs
// share. Azure DevOps ships no review CLI the way GitHub ships `gh`, and
// its PAT cannot be handed to `curl` without a second token read, so every
// call is one `az rest` invocation: `az login` holds the identity, `az`
// exchanges it for a token scoped to the Azure DevOps Entra application, and
// verdict never sees that token. `AZURE_DEVOPS_EXT_PAT` belongs to the
// `az devops` extension (and to the git clone on the fetch path); `az rest`
// does not read it.
//
// The organization gate, the pull-scoped URLs, and the thread payload fields
// live here once, so fetch and post cannot disagree about how a host is
// addressed or how a review reads back.
package azure

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// EnvOrg names the one variable every Azure call reads: the organization
// URL, `https://dev.azure.com/{org}`.
const EnvOrg = "AZURE_DEVOPS_ORG"

// APIQuery is the api-version every call pins, matching fetchAzure.
const APIQuery = "api-version=7.1"

// Refusal codes already in the closed set: a missing credential is
// `auth-missing`, and a malformed organization, repo, or pull request is
// `bad-target`, exactly as the other REST hosts report them.
const (
	CodeAuthMissing = "auth-missing"
	CodeBadTarget   = "bad-target"
)

const slash = "/"

// Target is one resolved pull position: the organization URL, the project
// and repository names, and the numeric pull-request id.
type Target struct {
	Org     string
	Project string
	Repo    string
	PR      int
}

// Base is the pull-scoped API base every call extends.
func (t Target) Base() string {
	return t.Org + slash + url.PathEscape(t.Project) + "/_apis/git/repositories/" +
		url.PathEscape(t.Repo) + "/pullRequests/" + strconv.Itoa(t.PR)
}

// PullURL is the pull-request read the head and the PR page come from.
func (t Target) PullURL() string { return t.Base() + "?" + APIQuery }

// ThreadsURL is the thread list and the thread create endpoint.
func (t Target) ThreadsURL() string { return t.Base() + "/threads?" + APIQuery }

// ThreadCommentURL is the one comment an update patches.
func (t Target) ThreadCommentURL(threadID, commentID string) string {
	return t.Base() + "/threads/" + url.PathEscape(threadID) + "/comments/" +
		url.PathEscape(commentID) + "?" + APIQuery
}

// WebURL is the pull-request page an operator opens: the same address
// fetchAzure records as `html_url`.
func (t Target) WebURL() string {
	return t.Org + slash + url.PathEscape(t.Project) + "/_git/" + url.PathEscape(t.Repo) +
		"/pullrequest/" + strconv.Itoa(t.PR)
}

// ThreadWebURL is the page of one discussion on that pull request.
func (t Target) ThreadWebURL(threadID string) string {
	return t.WebURL() + "?discussionId=" + url.PathEscape(threadID)
}

// connectionDataQuery is the one preview api-version the identity read pins.
// `connectionData` is a locations-service route with no released version:
// `api-version=7.1` answers 400 `VssInvalidPreviewVersionException`, so this
// read carries a preview version while the rest of the contract stays on 7.1.
const connectionDataQuery = "api-version=7.1-preview.1"

// ConnectionDataURL is the organization-scoped read of the authenticated
// user, the one identity source both the authorship gate and the summary
// comment lookup share.
func ConnectionDataURL(org string) string {
	return org + "/_apis/connectionData?" + connectionDataQuery
}

// Identity is the authenticated user one read attributes to: the account
// name a comment author carries and the organization identity id.
type Identity struct {
	ID   string
	Name string
}

// DecodeIdentity reads `connectionData`'s authenticatedUser. Azure publishes
// the account name as a property (`Account`), not as `uniqueName`, so both
// spellings are read here instead of in the caller.
func DecodeIdentity(raw string) (Identity, bool) {
	var doc struct {
		AuthenticatedUser struct {
			ID                  string `json:"id"`
			ProviderDisplayName string `json:"providerDisplayName"`
			Properties          struct {
				Account struct {
					Value string `json:"$value"`
				} `json:"Account"`
			} `json:"properties"`
		} `json:"authenticatedUser"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return Identity{}, false
	}
	user := doc.AuthenticatedUser
	id := Identity{ID: user.ID, Name: user.Properties.Account.Value}
	if id.Name == "" {
		id.Name = user.ProviderDisplayName
	}
	if id.Name == "" {
		id.Name = user.ID
	}
	return id, id.Name != ""
}

// The two hosts an organization URL may name. `az rest` buys one token for
// the Azure DevOps Entra application and sends it to the host in `--url`, so
// an organization that resolved anywhere else would hand that token to it:
// the allowlist below is what keeps `AZURE_DEVOPS_ORG` from redirecting the
// credential.
const (
	azureDevOpsHost   = "dev.azure.com"
	azureLegacySuffix = ".visualstudio.com"
)

// allowedOrgHost reports one organization hostname the allowlist accepts: the
// service host itself, or exactly one non-empty label in front of the legacy
// `visualstudio.com` domain. A deeper name (`evil.dev.azure.com`), a lookalike
// (`notvisualstudio.com`), and a bare suffix (`.visualstudio.com`) all fail.
func allowedOrgHost(host string) bool {
	if host == azureDevOpsHost {
		return true
	}
	label, ok := strings.CutSuffix(host, azureLegacySuffix)
	return ok && label != "" && !strings.Contains(label, ".")
}

// Org gates the environment and returns the organization URL. It refuses a
// missing variable, a non-https URL, a URL carrying userinfo, a query, or a
// fragment, and a host outside the allowlist: the credential belongs in `az
// login`, and a URL that embeds one — or names another host — would put it in
// every argv and every error the host prints. No refusal echoes the value.
func Org(env map[string]string) (string, *core.RefusalError) {
	raw := strings.TrimSpace(env[EnvOrg])
	if raw == "" {
		return "", &core.RefusalError{
			Code:    CodeAuthMissing,
			Message: "azure needs AZURE_DEVOPS_ORG=https://dev.azure.com/{org} (and an az login that reaches it)",
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", &core.RefusalError{
			Code:    CodeBadTarget,
			Message: "AZURE_DEVOPS_ORG must be an https organization URL like https://dev.azure.com/{org}",
		}
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", &core.RefusalError{
			Code: CodeBadTarget,
			Message: "AZURE_DEVOPS_ORG carries credentials, a query, or a fragment — " +
				"sign in with az login instead of putting a token in the URL",
		}
	}
	if hasControl(raw) {
		return "", &core.RefusalError{
			Code:    CodeBadTarget,
			Message: "AZURE_DEVOPS_ORG carries a control character",
		}
	}
	if ref := orgHostRefusal(u); ref != nil {
		return "", ref
	}
	return strings.TrimRight(raw, slash), nil
}

// msgOrgHost is the allowlist refusal: it names the two hosts the `az login`
// token reaches and never the value it rejected.
const msgOrgHost = "AZURE_DEVOPS_ORG must name dev.azure.com or {org}.visualstudio.com — " +
	"the az login token reaches those hosts and verdict will not send it to another"

// orgHostRefusal refuses a parsed organization whose host the token may not be
// sent to: an explicit port has no endpoint to buy a token for, and a host
// outside the allowlist is where the token would leak.
func orgHostRefusal(u *url.URL) *core.RefusalError {
	if u.Port() == "" && allowedOrgHost(strings.ToLower(u.Hostname())) {
		return nil
	}
	return &core.RefusalError{Code: CodeBadTarget, Message: msgOrgHost}
}

// Auth gates the environment alone, for a caller that has not resolved a
// position yet.
func Auth(env map[string]string) *core.RefusalError {
	_, ref := Org(env)
	return ref
}

// Resolve gates the environment and returns the pull position. The repo must
// be one `project/repository` pair of real path segments and the pull id
// must be a positive integer, so a value like `--pr 7/../../user` cannot
// traverse to another API endpoint; every segment is path-escaped before it
// is spliced in.
func Resolve(env map[string]string, repo, pr string) (Target, *core.RefusalError) {
	org, ref := Org(env)
	if ref != nil {
		return Target{}, ref
	}
	project, name, ok := splitRepo(repo)
	if !ok {
		return Target{}, &core.RefusalError{
			Code:    CodeBadTarget,
			Message: fmt.Sprintf("azure needs a project/repository pair, got %q", repo),
		}
	}
	n, err := strconv.Atoi(pr)
	if err != nil || n <= 0 {
		return Target{}, &core.RefusalError{
			Code:    CodeBadTarget,
			Message: fmt.Sprintf("azure needs a numeric pull-request id, got %q", pr),
		}
	}
	return Target{Org: org, Project: project, Repo: name, PR: n}, nil
}

// splitRepo splits one `project/repository` repo into its two non-empty
// segments, refusing `.`, `..`, a separator, and any control character.
func splitRepo(repo string) (string, string, bool) {
	project, name, ok := strings.Cut(repo, slash)
	if !ok || !validSegment(project) || !validSegment(name) {
		return "", "", false
	}
	return project, name, true
}

// validSegment reports one repo segment the URL builder can splice in
// safely: non-empty, not a traversal, no separator, no control byte.
func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\") && !hasControl(s)
}

// Control bytes: nothing below the first printable byte, and not DEL, ever
// belongs in a URL verdict builds from a request field.
const (
	printableFrom = 0x20
	deleteByte    = 0x7f
)

// hasControl reports a C0 control byte or DEL.
func hasControl(s string) bool {
	for i := range len(s) {
		if s[i] < printableFrom || s[i] == deleteByte {
			return true
		}
	}
	return false
}

// Pull is the slice of the PR read the post path needs: the commit the pull
// currently points at, in the same `lastMergeSourceCommit.commitId` shape
// fetch records.
type Pull struct {
	LastMergeSourceCommit struct {
		CommitID string `json:"commitId"`
	} `json:"lastMergeSourceCommit"`
}

// Head is the live source commit the drift check compares.
func (p Pull) Head() string { return p.LastMergeSourceCommit.CommitID }

// DecodePull decodes one PR read; a malformed payload reads as the empty
// pull, which the drift check treats as an unreadable live head.
func DecodePull(raw string) Pull {
	var pull Pull
	_ = json.Unmarshal([]byte(raw), &pull) //nolint:errcheck // the empty Pull is the unreadable answer
	return pull
}

// DecodeThreads decodes one thread list. Azure answers `{count, value}` and
// not a bare array, so the values are unwrapped here instead of in the two
// callers that walk threads.
func DecodeThreads(raw string) ([]map[string]any, bool) {
	var page struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		return nil, false
	}
	if page.Value == nil {
		return nil, false
	}
	return page.Value, true
}

// Created is the slice of a created thread a write reads back: the thread
// id, its first comment, and the timestamps the receipt records.
type Created struct {
	PublishedDate   string    `json:"publishedDate"`
	LastUpdatedDate string    `json:"lastUpdatedDate"`
	Comments        []Comment `json:"comments"`
	ID              int       `json:"id"`
}

// FirstCommentID is the created thread's opening comment id.
func (c Created) FirstCommentID() int {
	if len(c.Comments) == 0 {
		return 0
	}
	return c.Comments[0].ID
}

// FirstCommentBody is the created thread's opening comment content.
func (c Created) FirstCommentBody() string {
	if len(c.Comments) == 0 {
		return ""
	}
	return c.Comments[0].Content
}

// Comment is one thread comment, typed for the create read-back: the list
// reads stay maps, the shape the recheck and publication walks consume.
type Comment struct {
	Content string `json:"content"`
	ID      int    `json:"id"`
}

// DecodeCreated decodes one created thread.
func DecodeCreated(raw string) (Created, bool) {
	var created Created
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		return Created{}, false
	}
	if created.ID <= 0 {
		return Created{}, false
	}
	return created, true
}

// Thread statuses as Azure spells them in a response.
const (
	StatusActive  = "active"
	StatusPending = "pending"
)

// statusByNumber maps the numeric spelling a request uses onto the string
// spelling a response carries, in the enumeration's own order.
var statusByNumber = map[int]string{
	0: "unknown", 1: StatusActive, 2: "fixed", 3: "wontFix", 4: "closed",
	5: "byDesign", 6: StatusPending,
}

// Status reads one thread's status in either spelling and reports the string
// form; an unknown value reads as "unknown".
func Status(m map[string]any) string {
	switch v := m["status"].(type) {
	case string:
		return v
	case float64:
		if s, ok := statusByNumber[int(v)]; ok {
			return s
		}
	}
	return "unknown"
}

// Open reports a thread still awaiting an answer: active or pending. Every
// resolved status (fixed, wontFix, byDesign, closed) and an unknown reading
// is not open.
func Open(m map[string]any) bool {
	switch Status(m) {
	case StatusActive, StatusPending:
		return true
	default:
		return false
	}
}

// Inline reports a thread anchored to a file: a `threadContext` member set.
// A general thread — the summary comment, a vote, a merge notice — carries
// none, so it is never a review thread.
func Inline(m map[string]any) bool {
	v, ok := m["threadContext"]
	return ok && v != nil
}

// Deleted reports a thread whose comments are all gone: the thread's own
// `isDeleted` flag or an empty comment list.
func Deleted(m map[string]any) bool {
	if deleted, ok := m["isDeleted"].(bool); ok && deleted {
		return true
	}
	return len(liveComments(m)) == 0
}

// System reports a thread whose opening comment is a system message (a vote,
// a merge attempt, a reviewer update). Azure publishes those beside review
// threads, so a recheck or a summary read must skip them.
func System(m map[string]any) bool {
	root, ok := Root(m)
	if !ok {
		return true
	}
	return commentType(root) == TypeSystem
}

// The comment types Azure publishes: the response carries the name, a
// request carries the number.
const (
	TypeText       = "text"
	TypeCodeChange = "codeChange"
	TypeSystem     = "system"
)

// commentTypeByNumber maps the numeric spelling a request uses onto the name
// a response carries.
var commentTypeByNumber = map[int]string{
	1: TypeText,
	2: TypeCodeChange,
	3: TypeSystem,
}

// commentType reads a comment's type in either spelling.
func commentType(m map[string]any) string {
	switch v := m["commentType"].(type) {
	case string:
		return v
	case float64:
		return commentTypeByNumber[int(v)]
	}
	return ""
}

// Root is the opening comment of one thread: the undeleted comment whose
// parent is 0 or absent, and failing that the first undeleted comment.
func Root(m map[string]any) (map[string]any, bool) {
	comments := liveComments(m)
	for _, c := range comments {
		if parent, ok := c["parentCommentId"].(float64); !ok || parent == 0 {
			return c, true
		}
	}
	if len(comments) == 0 {
		return nil, false
	}
	return comments[0], true
}

// liveComments returns a thread's undeleted comments in payload order.
func liveComments(m map[string]any) []map[string]any {
	raw, ok := m["comments"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, el := range raw {
		c, ok := el.(map[string]any)
		if !ok {
			continue
		}
		if deleted, ok := c["isDeleted"].(bool); ok && deleted {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Body reads the opening comment's content.
func Body(m map[string]any) string {
	root, ok := Root(m)
	if !ok {
		return ""
	}
	return str(root["content"])
}

// Author reads the opening comment's author: the unique name the identity
// gate compares, falling back to the display name.
func Author(m map[string]any) string {
	root, ok := Root(m)
	if !ok {
		return ""
	}
	identity, ok := root["author"].(map[string]any)
	if !ok {
		return ""
	}
	if name := str(identity["uniqueName"]); name != "" {
		return name
	}
	return str(identity["displayName"])
}

// ThreadID reads one thread's id as a string.
func ThreadID(m map[string]any) string {
	return idStr(m["id"])
}

// CommentID reads one thread's opening comment id as a string. Azure comment
// ids restart at 1 on every thread, so a summary comment is addressed by its
// thread id and this id together.
func CommentID(m map[string]any) string {
	root, ok := Root(m)
	if !ok {
		return ""
	}
	return idStr(root["id"])
}

// Anchor is one anchored thread's place: a path, a side, and the line on
// that side. A thread carries both starts and ends, so the start line is the
// range's first line.
type Anchor struct {
	Path  string
	Side  string
	Start int
	End   int
}

// AnchorOf reads the anchor of one inline thread, reporting false for a
// general thread or one whose position is not numeric.
func AnchorOf(m map[string]any) (Anchor, bool) {
	ctx, ok := m["threadContext"].(map[string]any)
	if !ok {
		return Anchor{}, false
	}
	path := str(ctx["filePath"])
	if path == "" {
		return Anchor{}, false
	}
	side, start, end, ok := anchorSide(ctx)
	if !ok {
		return Anchor{}, false
	}
	return Anchor{Path: strings.TrimPrefix(path, slash), Side: side, Start: start, End: end}, true
}

// anchorSide reads the first side the thread context carries: the new side
// first, then the old one.
func anchorSide(ctx map[string]any) (side string, start, end int, ok bool) {
	for _, candidate := range []struct{ side, start, end string }{
		{"RIGHT", "rightFileStart", "rightFileEnd"}, {"LEFT", "leftFileStart", "leftFileEnd"},
	} {
		first, ok := position(ctx[candidate.start])
		if !ok {
			continue
		}
		last, _ := position(ctx[candidate.end])
		if last < first {
			last = first
		}
		return candidate.side, first, last, true
	}
	return "", 0, 0, false
}

// position reads one `{line, offset}` pair.
func position(v any) (int, bool) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return 0, false
	}
	line, ok := m["line"].(float64)
	if !ok || int(line) <= 0 {
		return 0, false
	}
	return int(line), true
}

// str reads one string field, reporting "" when absent.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// idStr reads one numeric id as a string.
func idStr(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case string:
		return n
	default:
		return ""
	}
}
