package readcommon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

const localPrefix = "local-"

const corpusPrefix = "corpus-"

const prSuffix = "/PR.json"

const postSuffix = "/post.json"

const (
	fieldHeadSHA = "head_sha"
	fieldBaseSHA = "base_sha"
	fieldState   = "state"
	fieldAuthor  = "author"
)

// ResolveBundleDir resolves the bundle dir under the reviewed repository
// root: local-* and corpus-* slugs map straight to their own bundle
// directory; bare slugs with a matching local-* dir (and no PR bundle)
// resolve local. Every run artifact for the bundle lives here — the diff,
// the normalized metadata, the review/store/history/receipts, and the
// typed config evidence — never in a cwd-relative legacy directory.
func ResolveBundleDir(root, pr string) string {
	state := filepath.Join(root, config.VerdictDir, config.StateDirName)
	if strings.HasPrefix(pr, localPrefix) || strings.HasPrefix(pr, corpusPrefix) {
		return filepath.Join(state, pr)
	}
	if IsDir(filepath.Join(state, localPrefix+pr)) && !Exists(filepath.Join(state, pr, "PR.json")) {
		return filepath.Join(state, localPrefix+pr)
	}
	return filepath.Join(state, pr)
}

// repoRootTimeout bounds the git top-level probe: a hung git must not hang
// the verb.
const repoRootTimeout = 5 * time.Second

// absentCheckoutError marks a probe that ran and answered "no repository here";
// every other probe failure (spawn, cap, cancellation) proves nothing.
type absentCheckoutError struct{}

func (absentCheckoutError) Error() string { return "no checkout" }

var errNoCheckout error = absentCheckoutError{}

// repoRootOutputCap bounds the probe's captured streams: a top-level path is
// far shorter, so a capped stdout is never parsed as a legitimate root.
const repoRootOutputCap = 4096

// ErrRootUnresolved reports a root probe that could not answer: a failing git,
// a canceled or expired context, an unreadable working directory, or a probe
// that failed inside a checkout. It is never reported as ErrNoCheckout, so a
// broken probe cannot be mistaken for a verified absence of policy.
var ErrRootUnresolved = errors.New("repository root unresolved")

// rootUnresolvedError seals a failed probe behind ErrRootUnresolved: callers
// match that sentinel, and [errors.Is] still answers for the underlying cause
// (cancellation, a capability error), so neither is lost in the diagnostic.
type rootUnresolvedError struct{ cause error }

func (e rootUnresolvedError) Error() string {
	return ErrRootUnresolved.Error() + ": " + e.cause.Error()
}

// Unwrap exposes the sentinel and the cause, in that order.
func (e rootUnresolvedError) Unwrap() []error { return []error{ErrRootUnresolved, e.cause} }

// rootUnresolved seals a probe failure behind ErrRootUnresolved.
func rootUnresolved(cause error) error { return rootUnresolvedError{cause: cause} }

// noCheckoutError seals a probe that ran and answered "no repository here".
// Only this answer may be read as an absent checkout, and the detail git
// printed stays in the diagnostic.
type noCheckoutError struct{ detail string }

func (e noCheckoutError) Error() string { return errNoCheckout.Error() + ": " + e.detail }

func (e noCheckoutError) Unwrap() error { return errNoCheckout }

// noCheckout seals the observed "no repository" answer behind errNoCheckout.
func noCheckout(detail string) error { return noCheckoutError{detail: detail} }

// probeFailureError labels one failed probe step, keeping the cause matchable.
type probeFailureError struct {
	cause error
	label string
}

func (e probeFailureError) Error() string { return e.label + ": " + e.cause.Error() }

func (e probeFailureError) Unwrap() error { return e.cause }

// probeFailure labels one probe step, keeping the cause matchable.
func probeFailure(label string, cause error) error {
	return probeFailureError{cause: cause, label: label}
}

// RepoRoot resolves the reviewed repository root for the working directory:
// the git top level, so a subdirectory invocation yields the same bundles and
// policy as the root. The root itself reads "." so repo-relative output stays
// stable; elsewhere the resolved top level is named. Outside a verified
// checkout it reads ".", which is the documented cwd-anchored workflow. A
// probe that fails inside a checkout, a canceled or expired context, and an
// unreadable working directory report ErrRootUnresolved wrapped with the
// cause, so no caller can mistake a broken probe for an absent checkout.
func RepoRoot(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", rootUnresolved(err)
	}
	top, err := gitTopLevel(ctx)
	if err == nil {
		if sameDir(top) {
			return ".", nil
		}
		return top, nil
	}
	if probeFailed(err) {
		return "", rootUnresolved(err)
	}
	evidence, eerr := checkoutEvidence()
	if eerr != nil {
		return "", rootUnresolved(eerr)
	}
	if evidence {
		return "", rootUnresolved(err)
	}
	return ".", nil
}

// probeFailed reports whether a probe error must surface as ErrRootUnresolved
// instead of standing as proof that there is no checkout here: only a probe
// that actually answered "no repository" may be read as absence.
func probeFailed(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return !errors.Is(err, errNoCheckout)
}

// gitTopLevel probes the git top level under the caller's context.
func gitTopLevel(ctx context.Context) (string, error) {
	probe, stop := context.WithTimeout(ctx, repoRootTimeout)
	defer stop()
	res, err := (coreexec.Runner{OutputCap: repoRootOutputCap}).Run(probe, &core.Command{
		Argv: []string{"git", "rev-parse", "--show-toplevel"}, OutputCap: repoRootOutputCap,
	})
	if perr := probe.Err(); perr != nil {
		return "", probeFailure("probe git rev-parse --show-toplevel", perr)
	}
	if err != nil {
		return "", probeFailure("run git rev-parse --show-toplevel", err)
	}
	if res.StdoutTruncated {
		return "", &core.OutputCappedError{Stream: "stdout", Limit: repoRootOutputCap}
	}
	if res.ExitCode != 0 {
		if cause := strings.TrimSpace(res.Stderr); cause != "" {
			return "", noCheckout("git rev-parse --show-toplevel: " + cause)
		}
		return "", noCheckout(fmt.Sprintf("git rev-parse --show-toplevel: exit status %d", res.ExitCode))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// checkoutEvidence reports whether anything positively identifies a checkout
// whose probe failed: a .git entry (directory or linked-worktree file) between
// the working directory and the filesystem root, or an explicit Git root
// override. An unreadable directory is an error, never absence.
func checkoutEvidence() (bool, error) {
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CEILING_DIRECTORIES"} {
		if os.Getenv(key) != "" {
			return true, nil
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return false, probeFailure("read working directory", err)
	}
	return gitEntryAbove(dir)
}

// gitEntryAbove walks dir and its ancestors for a .git entry: a directory for a
// normal checkout, a file for a linked worktree.
func gitEntryAbove(dir string) (bool, error) {
	for {
		found, err := gitEntryAt(dir)
		if err != nil || found {
			return found, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

// gitEntryAt reports whether dir itself carries a .git entry. An unreadable
// entry is an error, never absence.
func gitEntryAt(dir string) (bool, error) {
	entry := filepath.Join(dir, ".git")
	switch _, err := os.Lstat(entry); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, probeFailure("stat "+entry, err)
	}
}

// sameDir reports whether the repository top level is the process working
// directory, so the repo-relative spelling needs no directory prefix.
func sameDir(top string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	realCwd, cwdErr := filepath.EvalSymlinks(cwd)
	realTop, topErr := filepath.EvalSymlinks(top)
	if cwdErr != nil || topErr != nil {
		return cwd == top
	}
	return realCwd == realTop
}

// DisplayPath renders path for operator-facing output: repository-relative
// when it lives under root, so a human line never carries an absolute local
// path. A path outside root, or an empty root, renders unchanged.
func DisplayPath(root, path string) string {
	if root == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

// IsLocalBundleDir reports whether dir is a local-bundle directory: the
// last segment carries the local- prefix, whatever root anchors it.
func IsLocalBundleDir(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), localPrefix)
}

// IsCorpusBundleDir reports whether dir is a corpus-bundle directory:
// the last segment carries the corpus- prefix, whatever root anchors it.
func IsCorpusBundleDir(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), corpusPrefix)
}

// BundleName renders the bundle slug for display: the last segment of
// the bundle directory.
func BundleName(dir string) string {
	return filepath.Base(dir)
}

// StateDir returns the bundle home under root: the ignored local review
// state directory every bundle lives in.
func StateDir(root string) string {
	return filepath.Join(root, config.VerdictDir, config.StateDirName)
}

// RepoKey returns the bundle's repository identity: a local bundle's
// source.json repo, else a PR bundle's record URL with its trailing review
// segment removed (so the github pull and the gitlab merge-request paths
// both fold to their host plus repository path). It returns "" when the
// bundle carries neither (legacy or hand-made records): such bundles never
// match, so patch identity cannot cross repository or host boundaries by
// default, and legacy bundles simply fall back to SHA behavior.
func RepoKey(dir string) string {
	if repo := JSONStr(dir+"/source.json", "repo"); repo != "" {
		return "local:" + repo
	}
	if url := OpenBundle(dir).StrField("url"); url != "" {
		return "pr:" + repoPrefix(url)
	}
	return ""
}

// repoPrefix folds a review URL to its repository collection: the URL
// without its trailing segment and slash (github .../pull/7 and gitlab
// .../-/merge_requests/7 both shed the review number).
func repoPrefix(url string) string {
	trimmed := strings.TrimSuffix(url, "/")
	if i := strings.LastIndex(trimmed, "/"); i > 0 {
		return trimmed[:i]
	}
	return trimmed
}

// SameRepo reports whether two bundles share a known repository identity.
// Unknown keys never match: a bundle that cannot prove its repository does
// not participate in patch matching.
func SameRepo(aDir, bDir string) bool {
	a, b := RepoKey(aDir), RepoKey(bDir)
	return a != "" && a == b
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

// Head reads the provider-aware head SHA or a local bundle's captured head.
func (b *Bundle) Head() string {
	head := b.PrField(fieldHeadSHA)
	if head != "" || !strings.HasPrefix(filepath.Base(b.dir), localPrefix) {
		return head
	}
	if _, err := os.Stat(b.dir + prSuffix); !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	return JSONStr(filepath.Join(b.dir, "source.json"), fieldHeadSHA)
}

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

// PostedHead reads `.head // empty` from post.json: the stored reviewed
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

func (b *Bundle) prMap() map[string]any {
	if !b.loadPR() {
		return nil
	}
	m, _ := jq.AsMap(b.pr)
	return m
}

func (b *Bundle) loadPR() bool {
	if !b.prLoaded {
		b.pr, b.prOK = decodeStore(b.dir+prSuffix, jq.MaxPRBytes)
		b.prLoaded = true
	}
	return b.prOK
}

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

// PrField reads the provider-aware PR meta (head_sha,
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

// ShortSHA renders the 7-character short form of a full SHA.
func ShortSHA(head string) string {
	if len(head) > shortSHALen {
		return head[:shortSHALen]
	}
	return head
}

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
