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

// postSuffix is the completed-post receipt filename.
const postSuffix = "/post.json"

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
	if IsDir(".review/local-"+pr) && !Exists(".review/"+pr+prSuffix) {
		return ".review/local-" + pr
	}
	return ".review/" + pr
}

// Bundle is one bundle's decoded provider records. PR.json and post.json are
// read and decoded at most once per Bundle, so a verb that needs the
// provider, head, base, state, author, and post receipt pays one store read
// instead of one per field. It is not safe for concurrent use: a run owns its
// Bundle.
type Bundle struct {
	pr         any
	post       any
	dir        string
	prLoaded   bool
	prOK       bool
	postLoaded bool
	postOK     bool
}

// OpenBundle returns a Bundle for dir without reading anything yet; each
// record decodes on first use. Missing, over-cap, and undecodable records
// read as absent — callers keep their own defaults and refusal postures on
// top (post defaults the provider to github, record refuses not-local, the
// finalizer distinguishes an unreadable post receipt from an absent one).
func OpenBundle(dir string) *Bundle {
	return &Bundle{dir: dir}
}

// Provider reads .provider with `jq -r '.provider // empty'` semantics: ""
// when the record is missing, unreadable, or carries no string provider.
func (b *Bundle) Provider() string {
	m := b.prMap()
	return jq.StrOf(m["provider"])
}

// ProviderMarker reads .provider as a present marker: the rendered value and
// whether the record carries one at all (a missing, null, or false marker is
// absent, and a present marker renders even when it is not a string,
// mirroring `jq -r '.provider // empty'`). Callers with a routing default use
// this; [Bundle.Provider] keeps the string-only read.
func (b *Bundle) ProviderMarker() (string, bool) {
	m := b.prMap()
	v, has := m["provider"]
	if !has || jq.Falsey(v) {
		return "", false
	}
	return jq.R(v), true
}

// PrField is [PrField] over this Bundle's decoded record.
func (b *Bundle) PrField(field string) string {
	m := b.prMap()
	if prov := jq.StrOf(m["provider"]); prov == "" {
		return githubField(m, field)
	}
	return neutralField(m, field)
}

// Head reads the provider-aware head SHA.
func (b *Bundle) Head() string { return b.PrField(fieldHeadSHA) }

// StrField reads one record field with `jq -r '.<field> // empty'`
// string-only semantics, bypassing the provider-aware mapping (callers that
// need a schema-specific field, e.g. a GitHub baseRefOid).
func (b *Bundle) StrField(name string) string {
	return jq.StrOf(b.prMap()[name])
}

// Base reads the provider-aware base SHA.
func (b *Bundle) Base() string { return b.PrField(fieldBaseSHA) }

// State reads the provider-aware state (GitHub states are lowercased).
func (b *Bundle) State() string { return b.PrField(fieldState) }

// Author reads the provider-aware author.
func (b *Bundle) Author() string { return b.PrField(fieldAuthor) }

// PostedHead ports `jq -r '.head // empty' post.json`: the stored reviewed
// head, with readable reporting whether the record decoded at all. Missing,
// unreadable, and non-object records never arm the finalizer; a JSON null
// document reads ("", true), exactly like the old per-verb reader.
func (b *Bundle) PostedHead() (string, bool) {
	if !b.loadPost() {
		return "", false
	}
	if b.post == nil {
		return "", true
	}
	m, ok := jq.AsMap(b.post)
	if !ok {
		return "", false
	}
	return scalarOrEmpty(m["head"]), true
}

// PostedAt reads .posted_at with jq's `//` presence rule: present is false
// when the record or the field is missing, null, or false, so the caller
// supplies its own default (post renders "unknown time").
func (b *Bundle) PostedAt() (value string, present bool) {
	if !b.loadPost() {
		return "", false
	}
	m, ok := jq.AsMap(b.post)
	if !ok {
		return "", false
	}
	v, ok := m["posted_at"]
	if !ok || jq.Falsey(v) {
		return "", false
	}
	return jq.R(v), true
}

// PostMap returns the decoded post record, or nil when it is missing,
// unreadable, or not an object. Callers that inspect the whole receipt (the
// review id, provenance notes) read it through here so the file is capped and
// decoded once, like every other bundle record.
func (b *Bundle) PostMap() map[string]any {
	if !b.loadPost() {
		return nil
	}
	m, _ := jq.AsMap(b.post)
	return m
}

// PostRecord is [Bundle.PostMap] for a whole directory: the shared
// post.json read for callers that do not otherwise need a Bundle.
func PostRecord(dir string) map[string]any {
	return OpenBundle(dir).PostMap()
}

// prMap returns the decoded PR record, or nil when absent.
func (b *Bundle) prMap() map[string]any {
	if !b.loadPR() {
		return nil
	}
	m, _ := jq.AsMap(b.pr)
	return m
}

// loadPR decodes PR.json once.
func (b *Bundle) loadPR() bool {
	if !b.prLoaded {
		b.pr, b.prOK = decodeStore(b.dir+prSuffix, jq.MaxPRBytes)
		b.prLoaded = true
	}
	return b.prOK
}

// loadPost decodes post.json once.
func (b *Bundle) loadPost() bool {
	if !b.postLoaded {
		b.post, b.postOK = decodeStore(b.dir+postSuffix, jq.MaxPRBytes)
		b.postLoaded = true
	}
	return b.postOK
}

// readStoreFile is [jq.CappedFile] behind a seam so tests can count the
// store reads a [Bundle] performs.
var readStoreFile = jq.CappedFile

// decodeStore reads one capped bundle record and decodes it, reporting
// whether a document was read. The cap is PRE-01's, so an oversized record is
// unreadable here exactly as it is for every other store read.
func decodeStore(path string, maxBytes int64) (any, bool) {
	raw, err := readStoreFile(path, maxBytes)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	return doc, true
}

// PrField ports pr_field: the provider-aware PR meta read (head_sha,
// base_sha, state, author). Missing or unreadable records read "" (an
// over-cap record is unreadable too, never allocated).
func PrField(dir, field string) string {
	return OpenBundle(dir).PrField(field)
}

// ProviderOf reads .provider from PR.json ("" when missing or unreadable,
// mirroring `jq -r '.provider // empty' ... || true`). Callers apply their
// own default (post/record use github for a missing marker).
func ProviderOf(dir string) string {
	return OpenBundle(dir).Provider()
}

// githubField reads the GitHub-shaped record field.
func githubField(m map[string]any, field string) string {
	switch field {
	case fieldHeadSHA:
		return jq.StrOf(m["headRefOid"])
	case fieldBaseSHA:
		return jq.StrOf(m["baseRefOid"])
	case fieldState:
		return ASCIILower(jq.StrOf(m["state"]))
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

// scalarOrEmpty renders a `jq -r '... // empty'` value: null, false, and
// missing read "", everything else renders -r style.
func scalarOrEmpty(v any) string {
	if jq.Falsey(v) {
		return ""
	}
	return jq.R(v)
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

// Exists reports whether path exists (files or dirs), mirroring `[[ -e ]]`.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether path is a directory, mirroring `[[ -d ]]`.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// IsFile reports whether path is a regular file, mirroring `[[ -f ]]`.
func IsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
