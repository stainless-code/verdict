package fetchcore

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// providerMaxPages caps every provider list walk: a broken or hostile host
// that repeats its cursor would otherwise page forever, and ten pages is a
// generous read for every list these hosts serve.
const providerMaxPages = 10

// pageWalk pages one provider list. first is the first page cursor; fetch
// resolves one cursor to its page items and the next cursor ("" when the
// host says the list ended). The walk stops at maxPages or on a repeated
// cursor (truncated), or on the first fetch error (the items collected so
// far ride along so a caller can keep a partial read and warn).
func pageWalk(
	ctx context.Context, first string, maxPages int,
	fetch func(context.Context, string) ([]any, string, error),
) (items []any, truncated bool, err error) {
	seen := make(map[string]bool, maxPages)
	for cursor := first; cursor != ""; {
		if seen[cursor] || len(seen) == maxPages {
			return items, true, nil
		}
		seen[cursor] = true
		page, next, ferr := fetch(ctx, cursor)
		if ferr != nil {
			return items, true, ferr
		}
		items = append(items, page...)
		cursor = next
	}
	return items, false, nil
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
