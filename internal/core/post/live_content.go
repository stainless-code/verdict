package postcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgejo"
	"github.com/stainless-code/verdict/internal/jq"
)

type remotePublication struct {
	reviews  []liveReview
	comments []map[string]any
}

func (st *state) refreshRemote(ctx context.Context) *core.RefusalError {
	cmdr, ref := st.commander()
	if ref != nil {
		return ref
	}
	_, reader, _ := hostFor(st.provider())
	remote, err := reader.publication(ctx, cmdr, st.env(), st.req.Repo, st.req.Pr)
	if err != nil {
		return &core.RefusalError{
			Code:    codePostFailed,
			Message: fmt.Sprintf("post failed: live publication read failed — nothing written: %v", err),
		}
	}
	st.live = remote.reviews
	st.liveLoaded = true
	st.remoteInline = remote.comments
	return nil
}

func readPublicationList(ctx context.Context, cmdr Commander, argv []string) ([]map[string]any, error) {
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: argv})
	if err != nil {
		return nil, err
	}
	return publicationList(res.Stdout)
}

func publicationList(raw string) ([]map[string]any, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(raw))
	if err != nil {
		return nil, jq.Sealed("live publication JSON", err)
	}
	if len(docs) == 0 {
		return nil, shapeError("empty live publication response")
	}
	var out []map[string]any
	for _, doc := range docs {
		page, err := publicationPage(doc)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
	}
	return out, nil
}

func publicationReviews(items []map[string]any) []liveReview {
	var raw []any
	for _, item := range items {
		raw = append(raw, item)
	}
	return parseReviewPage(raw)
}

func (githubHost) publication(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr string) (remotePublication, error) {
	base := "repos/" + repo + "/pulls/" + pr
	reviews, err := readPublicationList(ctx, cmdr, []string{toolGH, cmdAPI, base + "/reviews", flagPaginate})
	if err != nil {
		return remotePublication{}, err
	}
	comments, err := readPublicationList(ctx, cmdr, []string{toolGH, cmdAPI, base + "/comments", flagPaginate})
	return remotePublication{reviews: publicationReviews(reviews), comments: comments}, err
}

func (gitlabHost) publication(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr string) (remotePublication, error) {
	items, err := readPublicationList(ctx, cmdr, append(gitlabReadArgv(repo, pr, gitlabDiscussionsPath+gitlabPageQuery), flagPaginate))
	if err != nil {
		return remotePublication{}, err
	}
	var remote remotePublication
	for _, item := range items {
		for _, note := range remotePage([]any{item}) {
			if _, ok := note["position"].(map[string]any); ok {
				remote.comments = append(remote.comments, note)
			} else {
				remote.reviews = append(remote.reviews, publicationReviews([]map[string]any{note})...)
			}
		}
	}
	return remote, nil
}

func (forgejoHost) publication(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (remotePublication, error) {
	auth, api, ref := forgejo.APIBase(env)
	if ref != nil {
		return remotePublication{}, forgejoRefusal(ref)
	}
	base := api + "/repos/" + repo + "/pulls/" + pr + "/reviews"
	read := func(url string) ([]map[string]any, error) {
		raw, _, err := forgejoRead(ctx, cmdr, auth, url)
		if err != nil {
			return nil, err
		}
		return publicationList(raw)
	}
	reviews, err := readPages(base, read)
	if err != nil {
		return remotePublication{}, err
	}
	return forgejoPublication(base, reviews, read)
}

func forgejoPublication(base string, reviews []map[string]any, read func(string) ([]map[string]any, error)) (remotePublication, error) {
	remote := remotePublication{reviews: publicationReviews(reviews)}
	for _, review := range reviews {
		comments, err := readPages(base+"/"+jq.Interp(review["id"])+"/comments", read)
		if err != nil {
			return remotePublication{}, err
		}
		stampReviewHead(comments, review)
		remote.comments = append(remote.comments, comments...)
	}
	return remote, nil
}

func readPages(base string, read func(string) ([]map[string]any, error)) ([]map[string]any, error) {
	const pageSize = 50
	const pageLimit = 1000
	var out []map[string]any
	for page := 1; page <= pageLimit; page++ {
		items, err := read(base + "?limit=50&page=" + strconv.Itoa(page))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(items) < pageSize {
			return out, nil
		}
	}
	return nil, shapeError("live publication pagination exceeded limit")
}

func publicationPage(doc any) ([]map[string]any, error) {
	page, ok := doc.([]any)
	if !ok {
		return nil, shapeError("live publication response is not an array")
	}
	var out []map[string]any
	for _, item := range page {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, shapeError("live publication item is not an object")
		}
		out = append(out, m)
	}
	return out, nil
}

func stampReviewHead(comments []map[string]any, review map[string]any) {
	for _, comment := range comments {
		if jq.StrOf(comment["commit_id"]) == "" {
			comment["commit_id"] = review["commit_id"]
		}
	}
}
