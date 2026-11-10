package forgejo_test

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgejo"
)

// env is the resolved environment every case starts from.
func env() map[string]string {
	return map[string]string{
		forgejo.EnvHost:  "https://forgejo.invalid",
		forgejo.EnvToken: "eval-token",
	}
}

// TestResolve pins the auth argv and the pull-scoped base: the two literals a
// read and a write must share.
func TestResolve(t *testing.T) {
	t.Parallel()
	auth, base, ref := forgejo.Resolve(env(), "acme/app", "7")
	if ref != nil {
		t.Fatalf("Resolve refusal: %s: %s", ref.Code, ref.Message)
	}
	if want := []string{"-H", "Authorization: token eval-token"}; !slices.Equal(auth, want) {
		t.Errorf("auth = %q, want %q", auth, want)
	}
	if want := "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7"; base != want {
		t.Errorf("base = %q, want %q", base, want)
	}
}

// TestResolveGatesTheEnvironment proves each missing variable refuses with the
// published code and names the variable, before any URL is built.
func TestResolveGatesTheEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dropped string
		want    string
	}{
		{name: "no host", dropped: forgejo.EnvHost, want: "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"},
		{name: "no token", dropped: forgejo.EnvToken, want: "forgejo needs FORGEJO_TOKEN set (app token with repo read)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := env()
			delete(e, tc.dropped)
			auth, base, ref := forgejo.Resolve(e, "acme/app", "7")
			assertResolveRefused(t, tc.dropped, tc.want, auth, base, ref)
		})
	}
}

// assertResolveRefused checks one refused gate: the published code, the exact
// sentence, and the empty values a caller must never build a URL from.
func assertResolveRefused(t *testing.T, dropped, want string, auth []string, base string, ref *core.RefusalError) {
	t.Helper()
	if ref == nil {
		t.Fatalf("Resolve accepted an environment without %s", dropped)
	}
	if ref.Code != "auth-missing" {
		t.Errorf("code = %q, want auth-missing", ref.Code)
	}
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
	if auth != nil || base != "" {
		t.Errorf("refused Resolve returned auth=%q base=%q", auth, base)
	}
}

// TestEndpoints pins every URL both verbs build, so the read and the write can
// never address different paths.
func TestEndpoints(t *testing.T) {
	t.Parallel()
	const base = "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7"
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "review list", got: forgejo.ReviewListURL(base), want: base + "/reviews?limit=50&page=1"},
		{name: "comments", got: forgejo.CommentsURL(base, "301"), want: base + "/reviews/301/comments"},
		{name: "user", got: forgejo.UserURL(base), want: "https://forgejo.invalid/api/v1/user"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestDecodeReviewList reads the review fields post depends on: the state that
// marks a draft, the commit the review covers, and the author login.
func TestDecodeReviewList(t *testing.T) {
	t.Parallel()
	raw := `[{"id":301,"state":"PENDING","body":"","commit_id":"aaaa",` +
		`"submitted_at":"2026-11-01T10:00:00Z","html_url":"https://forgejo.invalid/r/301",` +
		`"user":{"login":"rev"}}]`
	reviews, ok := forgejo.DecodeReviews(raw)
	if !ok {
		t.Fatal("DecodeReviews refused a valid page")
	}
	want := forgejo.Review{
		ID: 301, State: "PENDING", CommitID: "aaaa", SubmittedAt: "2026-11-01T10:00:00Z",
		HTMLURL: "https://forgejo.invalid/r/301", User: forgejo.User{Login: "rev"},
	}
	if len(reviews) != 1 || reviews[0] != want {
		t.Fatalf("reviews = %+v, want [%+v]", reviews, want)
	}
	if _, ok := forgejo.DecodeReviews("not json{"); ok {
		t.Error("DecodeReviews accepted a malformed page")
	}
}

// TestDecodeReviewAndUser pins the two single-object reads.
func TestDecodeReviewAndUser(t *testing.T) {
	t.Parallel()
	review, ok := forgejo.DecodeReview(`{"id":901,"state":"APPROVED","html_url":"u","submitted_at":"t"}`)
	if !ok || review.ID != 901 || review.State != "APPROVED" || review.HTMLURL != "u" || review.SubmittedAt != "t" {
		t.Fatalf("DecodeReview = %+v, %v", review, ok)
	}
	user, ok := forgejo.DecodeUser(`{"login":"rev"}`)
	if !ok || user.Login != "rev" {
		t.Fatalf("DecodeUser = %+v, %v", user, ok)
	}
}

// TestDecodePull pins the head read: the drift check compares this SHA against
// the bundle head.
func TestDecodePull(t *testing.T) {
	t.Parallel()
	pull := forgejo.DecodePull(`{"head":{"sha":"aaaa"},"base":{"sha":"bbbb"}}`)
	if pull.Head.SHA != "aaaa" {
		t.Errorf("head sha = %q, want aaaa", pull.Head.SHA)
	}
	if got := forgejo.DecodePull("not json{").Head.SHA; got != "" {
		t.Errorf("malformed pull head = %q, want the empty pull", got)
	}
}

// TestResolved pins the one resolution rule threads and the post recheck
// share: a comment with no resolver is an open thread, and a null resolver is
// the same as a missing one.
func TestResolved(t *testing.T) {
	t.Parallel()
	tests := []struct {
		comment map[string]any
		name    string
		want    bool
	}{
		{name: "no resolver", comment: map[string]any{"id": float64(1)}, want: false},
		{name: "null resolver", comment: map[string]any{"resolver": nil}, want: false},
		{name: "resolved", comment: map[string]any{"resolver": map[string]any{"login": "rev"}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := forgejo.Resolved(tc.comment); got != tc.want {
				t.Errorf("Resolved(%v) = %v, want %v", tc.comment, got, tc.want)
			}
		})
	}
}

// TestDecodeComments pins the comment page decode Resolved runs over.
func TestDecodeComments(t *testing.T) {
	t.Parallel()
	comments, ok := forgejo.DecodeComments(`[{"id":401,"position":2},{"id":402,"resolver":{"login":"rev"}}]`)
	if !ok || len(comments) != 2 {
		t.Fatalf("DecodeComments = %v, %v", comments, ok)
	}
	if forgejo.Resolved(comments[0]) || !forgejo.Resolved(comments[1]) {
		t.Errorf("resolution read = %v, %v; want false, true", forgejo.Resolved(comments[0]), forgejo.Resolved(comments[1]))
	}
	if _, ok := forgejo.DecodeComments(`{"id":401}`); ok {
		t.Error("DecodeComments accepted an object where the API sends an array")
	}
}
