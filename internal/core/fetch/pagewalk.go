package fetchcore

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// providerMaxPages caps every provider list walk: a broken or hostile host
// that repeats its cursor would otherwise page forever, and ten pages is a
// generous read for every list these hosts serve.
const (
	providerMaxPages    = 10
	forgejoPageSize     = 50
	gitlabNotePageSize  = 100
	azureChangePageSize = 1000
)

func pageWalk(
	ctx context.Context, first string, maxPages int,
	fetch func(context.Context, string) ([]any, string, error),
) (items []any, truncated bool, pages int, err error) {
	seen := make(map[string]bool, maxPages)
	for cursor := first; cursor != ""; {
		if seen[cursor] || len(seen) == maxPages {
			return items, true, pages, nil
		}
		seen[cursor] = true
		pages++
		page, next, ferr := fetch(ctx, cursor)
		if ferr != nil {
			return items, true, pages, ferr
		}
		items = append(items, page...)
		cursor = next
	}
	return items, false, pages, nil
}

type indexPager struct {
	fetch    func(context.Context, int) (string, error)
	rawFirst string
	pageSize int
}

func indexPageWalk(
	ctx context.Context, pageSize int, fetch func(context.Context, int) (string, error),
) (body string, truncated bool, err error) {
	p := &indexPager{pageSize: pageSize, fetch: fetch}
	items, truncated, pages, werr := pageWalk(ctx, "1", providerMaxPages, p.page)
	if pages == 1 && werr == nil && !truncated {
		return p.rawFirst, false, nil
	}
	if items == nil {
		items = []any{}
	}
	return jq.Pretty(items), truncated, werr
}

func (p *indexPager) page(ctx context.Context, cursor string) ([]any, string, error) {
	page, ok := cursorPage(cursor)
	if !ok {
		return nil, "", internalErrorf("bad page cursor %q", cursor)
	}
	raw, ferr := p.fetch(ctx, page)
	if ferr != nil {
		return nil, "", ferr
	}
	if page == 1 {
		p.rawFirst = raw
	}
	list, derr := jsonArray(raw)
	if derr != nil {
		return nil, "", internalErrorf("page unreadable: %v", derr)
	}
	if len(list) < p.pageSize {
		return list, "", nil
	}
	return list, strconv.Itoa(page + 1), nil
}

// cursorPage parses an internal page cursor (always digits this code wrote).
func cursorPage(cursor string) (int, bool) {
	page, err := strconv.Atoi(cursor)
	if err != nil {
		return 0, false
	}
	return page, true
}

func warnPaging(r *session, provider, what string, err error, truncated bool) {
	r.observePaging(truncated || err != nil)
	switch {
	case err != nil:
		r.Warn(provider + " " + what + " paging stopped on a failed page (" +
			headBytes(err.Error(), numExcerptShort) + ") — the list may be incomplete")
	case truncated:
		r.Warn(fmt.Sprintf("%s %s paging hit the %d-page cap — the list may be incomplete",
			provider, what, providerMaxPages))
	}
}

func jsonArray(raw string) ([]any, error) {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return nil, internalErrorf("response is not JSON: %v", err)
	}
	list, ok := doc.([]any)
	if !ok {
		return nil, internalErrorf("response is not a JSON array")
	}
	return list, nil
}

// sameOrigin reports whether raw resolves to the same scheme and host as
// base. Every server-supplied URL is checked before a credential-bearing
// read: a compromised or malicious response would otherwise move a bearer
// token or PAT to another host.
func sameOrigin(base, raw string) bool {
	_, ok := resolveOnOrigin(base, raw)
	return ok
}

// resolveOnOrigin resolves raw against base and reports whether the result
// keeps base's scheme and host. The resolved URL is the safe fetch target:
// relative links stay on the trusted origin and absolute links elsewhere
// are refused.
func resolveOnOrigin(base, raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	b, err := url.Parse(base)
	if err != nil || b.Scheme == "" || b.Host == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	u = b.ResolveReference(u)
	if !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) {
		return "", false
	}
	return u.String(), true
}

// offOriginRefusal refuses a credentialed read of a server-supplied URL
// outside the configured origin. fetch-failed keeps the shared remediation
// hint; the message names only the host so a query-string cursor never
// reaches stderr.
func offOriginRefusal(provider, raw string) *core.RefusalError {
	host := "an unknown host"
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host = u.Host
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
		"%s answered with a URL on another origin (%s); refusing to send credentials", provider, host)}
}
