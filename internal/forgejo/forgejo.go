// Package forgejo is the Forgejo wire contract both verbs share. Forgejo
// ships no CLI, so every read and write is one authenticated `curl` against
// `$FORGEJO_HOST/api/v1`. The environment gate, the endpoint URLs, and the
// payload fields live here once, so fetch and post cannot disagree about how
// a host is addressed or how a review reads back.
package forgejo

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Environment names the two variables every Forgejo call needs.
const (
	EnvHost  = "FORGEJO_HOST"
	EnvToken = "FORGEJO_TOKEN"
)

const apiPrefix = "/api/v1"

// CodeAuthMissing is the refusal code for an unset host or token. It is the
// code fetch already returns for the same two variables, so one operator
// story spans both verbs.
const CodeAuthMissing = "auth-missing"

// PageSize is the page every list read requests: the Forgejo maximum, which
// fetch's review walk and post's read-back both use, so the two verbs walk one
// window.
const PageSize = 50

// Endpoint suffixes under the pull-scoped API base.
const (
	ReviewsPath = "/reviews"
	FilesPath   = "/files"
	DiffSuffix  = ".diff"
)

// Review states Forgejo records. PENDING is the unsubmitted draft nobody else
// sees, which is why post never reads one as a posted review.
const (
	StatePending = "PENDING"
)

// Review events a Forgejo review write carries. Forgejo spells the approval
// APPROVED, where verdict's own payload event is APPROVE.
const (
	EventComment        = "COMMENT"
	EventRequestChanges = "REQUEST_CHANGES"
	EventApproved       = "APPROVED"
)

// Resolve gates the environment and returns the curl auth argv plus the
// pull-scoped API base. Both messages name the variable to set, and the
// refusal fires before any call is spawned.
func Resolve(env map[string]string, repo, pr string) (auth []string, base string, err *core.RefusalError) {
	host := env[EnvHost]
	token := env[EnvToken]
	if host == "" {
		return nil, "", &core.RefusalError{Code: CodeAuthMissing, Message: "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"}
	}
	if token == "" {
		return nil, "", &core.RefusalError{Code: CodeAuthMissing, Message: "forgejo needs FORGEJO_TOKEN set (app token with repo read)"}
	}
	return []string{"-H", "Authorization: token " + token}, host + apiPrefix + "/repos/" + repo + "/pulls/" + pr, nil
}

// ReviewListURL is the first reviews page: the URL fetch pages into
// reviews.json, and the one post reads back for the draft check and the
// open-thread recheck.
func ReviewListURL(base string) string {
	return fmt.Sprintf("%s%s?limit=%d&page=1", base, ReviewsPath, PageSize)
}

// CommentsURL is the comment list of one review.
func CommentsURL(base, id string) string {
	return base + ReviewsPath + "/" + id + "/comments"
}

// UserURL is the authenticated-user read, derived from the pull-scoped base so
// a caller resolves the host once.
func UserURL(base string) string {
	host, _, ok := strings.Cut(base, "/repos/")
	if !ok {
		return ""
	}
	return host + "/user"
}

// Review is one pull-review row: what post reads back from the list and from
// an accepted write.
type Review struct {
	ID          int    `json:"id"`
	State       string `json:"state"`
	Body        string `json:"body"`
	CommitID    string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
	HTMLURL     string `json:"html_url"`
	User        User   `json:"user"`
}

// User is the login a review attributes to its author.
type User struct {
	Login string `json:"login"`
}

// Pull is the slice of the PR read both verbs need: the commit the pull
// currently points at, in the same `head.sha` shape fetch records.
type Pull struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// DecodePull decodes one PR read; a malformed payload reads as the empty pull,
// which the drift check treats as an unreadable live head.
func DecodePull(raw string) Pull {
	var pull Pull
	_ = json.Unmarshal([]byte(raw), &pull) //nolint:errcheck // the empty Pull is the unreadable answer
	return pull
}

// DecodeReviews decodes one review-list page.
func DecodeReviews(raw string) ([]Review, bool) {
	var reviews []Review
	if err := json.Unmarshal([]byte(raw), &reviews); err != nil {
		return nil, false
	}
	return reviews, true
}

// DecodeReview decodes one created review.
func DecodeReview(raw string) (Review, bool) {
	var review Review
	if err := json.Unmarshal([]byte(raw), &review); err != nil {
		return Review{}, false
	}
	return review, true
}

// DecodeUser decodes the authenticated-user read.
func DecodeUser(raw string) (User, bool) {
	var user User
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return User{}, false
	}
	return user, true
}

// DecodeComments decodes one review-comment page as maps, the shape threads
// maps and Resolved reads.
func DecodeComments(raw string) ([]map[string]any, bool) {
	var comments []map[string]any
	if err := json.Unmarshal([]byte(raw), &comments); err != nil {
		return nil, false
	}
	return comments, true
}

// Resolved reports whether one review comment carries a resolver: resolution
// lives on the comment itself, so a missing or null resolver is an open
// thread. threads.FromForgejo and the post recheck read this one rule.
func Resolved(comment map[string]any) bool {
	return comment["resolver"] != nil
}
