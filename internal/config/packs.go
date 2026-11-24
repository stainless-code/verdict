package config

import (
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitsource"
)

// Org pin validation bounds: a commit SHA is 40 hex characters and a
// control rune ends the printable range.
const (
	orgSHAHexLen = 40
	controlFloor = 0x20
	controlDEL   = 0x7f
	rootDot      = "."
)

// The built-in pack toggle grammars: a pack entry is a full
// `<namespace>/<slug>` name or a bare slug, a lens id is the pack loader's
// lowercase slug, and a rule id is the knowledge-base grammar the loader
// enforces.
var (
	packToggleNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
	packToggleSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	packToggleRuleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,64}$`)
)

// buildPacks validates the built-in pack composition section.
func buildPacks(raw any, path string) (*core.ConfigPacks, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigPacks{}
	var err *core.RefusalError
	if out.MergeBuiltin, err = presentValue(obj, "merge_builtin", path, asBool); err != nil {
		return nil, err
	}
	if out.DisablePacks, err = presentStrings(obj, "disable_packs", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleNameRE, packToggleSlugRE)
	}); err != nil {
		return nil, err
	}
	if out.DisableLenses, err = presentStrings(obj, "disable_lenses", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleSlugRE)
	}); err != nil {
		return nil, err
	}
	if out.DisableRules, err = presentStrings(obj, "disable_rules", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleRuleRE)
	}); err != nil {
		return nil, err
	}
	if out.OrgPack, err = presentNested(obj, "org_pack", path, buildOrgPack); err != nil {
		return nil, err
	}
	return out, nil
}

// buildOrgPack validates the central-pack pin: repo and ref are required,
// path defaults to the repository root. Every value is carried verbatim
// into the typed pin; the pack layer re-validates before any vendoring.
func buildOrgPack(raw any, path string) (*core.ConfigOrgPack, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	repo, ref := requiredString(obj, "repo", path, CodeBadConfig)
	if ref != nil {
		return nil, ref
	}
	if err := checkOrgRepo(repo, path); err != nil {
		return nil, err
	}
	pinRef, ref := requiredString(obj, "ref", path, CodeBadConfig)
	if ref != nil {
		return nil, ref
	}
	if err := checkOrgRef(pinRef, path); err != nil {
		return nil, err
	}
	pinPath, ref := orgPinPath(obj, path)
	if ref != nil {
		return nil, ref
	}
	return &core.ConfigOrgPack{Repo: repo, Ref: pinRef, Path: pinPath}, nil
}

// orgPinPath reads the optional pack root: absent reads empty (the pack
// layer normalizes it to the repository root), present must be confined.
func orgPinPath(obj map[string]any, path string) (string, *core.RefusalError) {
	present, ok := obj["path"]
	if !ok {
		return "", nil
	}
	parsed, err := nonEmptyString(present, path+".path")
	if err != nil {
		return "", err
	}
	if err := checkOrgPath(parsed, path); err != nil {
		return "", err
	}
	return parsed, nil
}

// checkOrgRepo refuses an unusable central repository spelling: empty was
// already refused, and what remains must carry no control character, no
// leading option dash, and no query or fragment — the pin is recorded in
// provenance, so nothing after the path may carry a token. A remote-shaped
// source the git allowlist rejects refuses with bad-config without echoing
// credentials. The allowlist itself is internal/gitsource, the same grammar
// the pack layer clones through; a config persistence value carries the ref in
// its own field, so no @suffix is read as one here.
func checkOrgRepo(repo, path string) *core.RefusalError {
	if strings.HasPrefix(repo, "-") {
		return refuse(CodeBadConfig, "%s.repo: %q starts with '-'", path, maskedOrgRepo(repo))
	}
	if strings.ContainsAny(repo, "?#") || !cleanOrgString(repo) {
		return refuse(CodeBadConfig, "%s.repo: %q is not a repository spelling", path, maskedOrgRepo(repo))
	}
	if looksLikeOrgGitSource(repo) || orgSecretShaped(repo) {
		if _, ok := gitsource.ParseURL(repo); !ok {
			return refuse(CodeBadConfig, "%s.repo: %q is not an allowlisted git remote", path, maskedOrgRepo(repo))
		}
	}
	return nil
}

// looksLikeOrgGitSource reports whether a repo value asks to be a git remote,
// so a rejected remote shape refuses instead of reading as a local path.
func looksLikeOrgGitSource(s string) bool { return gitsource.LooksLike(s) }

// orgSecretShaped reports whether a repo value carries a URL or userinfo shape
// that must never be echoed in a refusal.
func orgSecretShaped(s string) bool { return gitsource.SecretShaped(s) }

// maskedOrgRepo renders one repo value for a refusal: a secret-shaped value
// becomes a fixed placeholder so no refusal can echo a credential. A value
// carrying a query or fragment is masked too: local-path-shaped values such
// as `central?token=…` are refused by checkOrgRepo on the `?`/`#` arm, and
// the refusal beside that arm must never echo the query text.
func maskedOrgRepo(repo string) string {
	if orgSecretShaped(repo) || strings.ContainsAny(repo, "?#") {
		return "<invalid source>"
	}
	return repo
}

// checkOrgRef refuses any ref that is not an immutable pin: only a 40-hex
// commit SHA or a semver tag (such as `v1.2.3`) is accepted, so a branch
// name like `main` can never float the governed rule set.
func checkOrgRef(ref, path string) *core.RefusalError {
	if hex40Ref(ref) || validOrgTag(ref) {
		return nil
	}
	return refuse(CodeBadConfig,
		"%s.ref: %q is not immutable — use a 40-hex commit SHA or a semver tag like v1.2.3, never a branch name",
		path, ref)
}

// checkOrgPath refuses a pack root that could escape the central
// repository: absolute spellings, backslashes, and `..` segments are refused,
// and so is a colon, which the clone-side grammar ([confinedRoot]) refuses too
// — no config can name a path the vendoring step would then reject; `.` names
// the repository root.
func checkOrgPath(pinPath, base string) *core.RefusalError {
	if strings.HasPrefix(pinPath, "/") || strings.HasPrefix(pinPath, "-") ||
		strings.ContainsAny(pinPath, `\:`) || !cleanOrgString(pinPath) {
		return refuse(CodeBadConfig, "%s.path: %q is not a repository-relative slash path", base, pinPath)
	}
	if pinPath == rootDot {
		return nil
	}
	for seg := range strings.SplitSeq(pinPath, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return refuse(CodeBadConfig, "%s.path: %q escapes the central repository root", base, pinPath)
		}
	}
	return nil
}

// cleanOrgString reports whether s carries no C0 or DEL control character.
func cleanOrgString(s string) bool {
	for _, r := range s {
		if r < controlFloor || r == controlDEL {
			return false
		}
	}
	return true
}

// hex40Ref reports whether s is a lowercase 40-hex commit SHA.
func hex40Ref(s string) bool {
	if len(s) != orgSHAHexLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

// orgTagRE matches the accepted semver tags, with an optional `v` prefix.
// The pattern is strict SemVer 2.0.0: each core component is `0` or a
// non-zero digit run (no leading zero); a prerelease identifier is `0`, a
// non-zero numeric run, or an alphanumeric/dash run containing at least one
// non-digit (so a numeric identifier never carries a leading zero); a
// build identifier is a non-empty alphanumeric/dash run. Identifiers are
// dot-separated and non-empty, so `v1.2.3-alpha..1` never matches.
var orgTagRE = regexp.MustCompile(`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)` +
	`(?:-(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*` +
	`)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// validOrgTag reports whether ref is a strict SemVer tag Git can carry as a
// tag: orgTagRE must match and the value must not end in `.lock`, the suffix
// Git forbids on any ref. The suffix check is separate because the build
// grammar above admits `v1.2.3+build.lock` as SemVer while Git refuses it.
func validOrgTag(ref string) bool {
	if strings.HasSuffix(ref, ".lock") {
		return false
	}
	return orgTagRE.MatchString(ref)
}

// toggleList reads one non-empty array of ids, each matching at least one of
// the grammars the list accepts, with no duplicates: a repeated id would make
// the applied-toggle report ambiguous.
func toggleList(raw any, path string, accept ...*regexp.Regexp) ([]string, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "%s: want an array of ids, got %s", path, jsonKind(raw))
	}
	if len(items) == 0 {
		return nil, refuse(CodeBadConfig, "%s: want at least one id; omit the key to disable nothing", path)
	}
	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, ref := toggleID(item, path, accept)
		if ref != nil {
			return nil, ref
		}
		if seen[id] {
			return nil, refuse(CodeBadConfig, "%s: %q is listed twice", path, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// toggleID validates one entry against the accepted grammars.
func toggleID(item any, path string, accept []*regexp.Regexp) (string, *core.RefusalError) {
	id, ok := item.(string)
	if !ok {
		return "", refuse(CodeBadConfig, "%s: want a string id, got %s", path, jsonKind(item))
	}
	for _, re := range accept {
		if re.MatchString(id) {
			return id, nil
		}
	}
	return "", refuse(CodeBadConfig, "%s: %q is not a lowercase slug%s", path, id, toggleShape(accept))
}

// toggleShape renders the accepted spelling for the refusal.
func toggleShape(accept []*regexp.Regexp) string {
	if len(accept) > 1 {
		return " or a <namespace>/<slug> pack name"
	}
	return ""
}
