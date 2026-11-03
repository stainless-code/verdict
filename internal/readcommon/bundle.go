package readcommon

import (
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// localPrefix marks operator-local bundle slugs.
const localPrefix = "local-"

// corpusPrefix marks benchmark corpus bundle slugs.
const corpusPrefix = "corpus-"

// prSuffix is the provider record filename.
const prSuffix = "/PR.json"

// PR field names shared by the GitHub and neutral readers.
const (
	fieldHeadSHA = "head_sha"
	fieldBaseSHA = "base_sha"
	fieldState   = "state"
	fieldAuthor  = "author"
)

// ResolveBundleDir ports resolve_bundle_dir: local-* and corpus-* slugs
// resolve directly; bare slugs with a matching .review/local-* dir (and no
// PR bundle) resolve local.
func ResolveBundleDir(pr string) string {
	if strings.HasPrefix(pr, localPrefix) || strings.HasPrefix(pr, corpusPrefix) {
		return ".review/" + pr
	}
	if isDir(".review/local-"+pr) && !exists(".review/"+pr+prSuffix) {
		return ".review/local-" + pr
	}
	return ".review/" + pr
}

// PrField ports pr_field: the provider-aware PR meta read (head_sha,
// base_sha, state, author). Missing or unreadable records read "".
func PrField(dir, field string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	prov := jq.StrOf(m["provider"])
	if prov == "" {
		return githubField(m, field)
	}
	return neutralField(m, field)
}

// githubField reads the GitHub-shaped record field.
func githubField(m map[string]any, field string) string {
	switch field {
	case fieldHeadSHA:
		return jq.StrOf(m["headRefOid"])
	case fieldBaseSHA:
		return jq.StrOf(m["baseRefOid"])
	case fieldState:
		return strings.ToLower(jq.StrOf(m["state"]))
	case fieldAuthor:
		author, ok := m["author"].(map[string]any)
		if !ok {
			return ""
		}
		return jq.StrOf(author["login"])
	default:
		return ""
	}
}

// neutralField reads the normalized non-GitHub record field.
func neutralField(m map[string]any, field string) string {
	switch field {
	case fieldHeadSHA:
		return jq.StrOf(m["head_sha"])
	case fieldBaseSHA:
		return jq.StrOf(m["base_sha"])
	case fieldState:
		return jq.StrOf(m["state"])
	case fieldAuthor:
		return jq.StrOf(m["author"])
	default:
		return ""
	}
}

// ProviderOf reads .provider from PR.json ("" when missing or unreadable,
// mirroring `jq -r '.provider // empty' ... || true`).
func ProviderOf(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(m["provider"])
}

// ShortSHA ports ${head_full:0:7}.
func ShortSHA(head string) string {
	if len(head) > shortSHALen {
		return head[:shortSHALen]
	}
	return head
}

// shortSHALen is the abbreviated SHA width.
const shortSHALen = 7

// exists reports whether path exists (files or dirs).
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isDir reports whether path is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isRegular reports whether path is a regular file, mirroring [[ -f ]].
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
