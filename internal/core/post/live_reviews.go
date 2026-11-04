package postcore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	reviewMarkerFormat  = "<!-- verdict-review payload=%s head=%s -->"
	markerHashLen       = 40
	reviewMarkerReserve = len(reviewMarkerFormat) + 2*markerHashLen + 2
)

var reviewMarkerRe = regexp.MustCompile(`^<!-- verdict-review payload=([0-9a-f]{40}) head=([0-9a-f]{0,40}) -->$`)

func reviewMarker(body string) (payloadHash, head string, ok bool) {
	trimmed := strings.TrimRight(body, " \t\r\n")
	line := trimmed
	if _, after, found := strings.CutLast(trimmed, "\n"); found {
		line = after
	}
	m := reviewMarkerRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

type liveReview struct {
	body, commitID, submittedAt string
}

func (st *state) liveReviews() []liveReview {
	if st.liveLoaded {
		return st.live
	}
	st.liveLoaded = true
	raw, err := jq.CappedFile(st.dir+"/reviews.json", jq.MaxPRBytes)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			st.warn("live reviews record unreadable — posting on local stores; re-fetch to restore the repeat guard")
		}
		return nil
	}
	docs, err := jq.DecodeSlurp(bytes.NewReader(raw))
	if err != nil {
		st.warn("live reviews record unparseable — posting on local stores; re-fetch to restore the repeat guard")
		return nil
	}
	for _, doc := range docs {
		items, _ := doc.([]any) //nolint:errcheck // non-array pages are skipped
		st.live = append(st.live, parseReviewPage(items)...)
	}
	return st.live
}

func parseReviewPage(items []any) []liveReview {
	var out []liveReview
	for _, el := range items {
		if m, ok := el.(map[string]any); ok {
			out = append(out, liveReview{
				body: jq.StrOf(m["body"]), commitID: jq.StrOf(m["commit_id"]),
				submittedAt: jq.StrOf(m["submitted_at"]),
			})
		}
	}
	return out
}

//nolint:revive // marker fallback keeps the head read in one place
func (st *state) liveAlreadyPosted() *core.RefusalError {
	for _, rev := range st.liveReviews() {
		_, markerHead, ok := reviewMarker(rev.body)
		if !ok {
			continue
		}
		head := rev.commitID
		if head == "" {
			head = markerHead
		}
		if head != st.commitID {
			continue
		}
		postedAt := rev.submittedAt
		if postedAt == "" {
			postedAt = unknownTime
		}
		return &core.RefusalError{Code: "already-posted", Message: fmt.Sprintf(msgAlreadyPosted,
			readcommon.ShortSHA(st.commitID), postedAt, st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.commitID))}
	}
	return nil
}

func (st *state) liveDuplicatePayload() *core.RefusalError {
	for _, rev := range st.liveReviews() {
		if hash, _, ok := reviewMarker(rev.body); ok && hash == st.phash {
			return duplicatePayloadRefusal()
		}
	}
	return nil
}

func duplicatePayloadRefusal() *core.RefusalError {
	return &core.RefusalError{Code: "duplicate-payload", Message: "identical payload already posted — " +
		"refusing duplicate (pass --force to repost)"}
}
