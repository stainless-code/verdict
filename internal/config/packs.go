package config

import (
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Org pin validation bounds: a commit SHA is 40 hex characters and a
// control rune ends the printable range.
const (
	orgSHAHexLen    = 40
	controlFloor    = 0x20
	controlDEL      = 0x7f
	rootDot         = "."
	maxHostLen      = 253
	maxHostLabelLen = 63
	maxPortLen      = 5
	maxUserLen      = 64
	schemeHTTPS     = "https://"
	schemeSSH       = "ssh://"
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
// credentials.
func checkOrgRepo(repo, path string) *core.RefusalError {
	if strings.HasPrefix(repo, "-") {
		return refuse(CodeBadConfig, "%s.repo: %q starts with '-'", path, repo)
	}
	if strings.ContainsAny(repo, "?#") || !cleanOrgString(repo) {
		return refuse(CodeBadConfig, "%s.repo: %q is not a repository spelling", path, repo)
	}
	if (looksLikeOrgGitSource(repo) || orgSecretShaped(repo)) && !parseOrgGitSource(repo) {
		return refuse(CodeBadConfig, "%s.repo: %q is not an allowlisted git remote", path, maskedOrgRepo(repo))
	}
	return nil
}

func parseOrgGitSource(s string) bool {
	if strings.ContainsAny(s, "?#") {
		return false
	}
	if strings.HasPrefix(strings.ToLower(s), "https://") {
		return parseOrgHTTPS(s)
	}
	if strings.HasPrefix(strings.ToLower(s), "ssh://") {
		return parseOrgSSH(s)
	}
	return parseOrgSCP(s)
}

func parseOrgHTTPS(s string) bool {
	rest := s[len(schemeHTTPS):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return false
	}
	auth := rest[:slash]
	if strings.Contains(auth, "@") || !validOrgHostPort(auth) {
		return false
	}
	return validOrgPath(rest[slash+1:])
}

func parseOrgSSH(s string) bool {
	rest := s[len(schemeSSH):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash+1 >= len(rest) {
		return false
	}
	auth := rest[:slash]
	at := strings.IndexByte(auth, '@')
	if at <= 0 || strings.Contains(auth[at+1:], "@") {
		return false
	}
	user := auth[:at]
	host := auth[at+1:]
	if !validOrgUser(user) || !validOrgHostPort(host) {
		return false
	}
	return validOrgPath(rest[slash+1:])
}

//nolint:revive // scp branch is one of three scheme variants; factoring further hurts readability
func parseOrgSCP(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return false
	}
	user := s[:at]
	rest := s[at+1:]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return false
	}
	host := rest[:colon]
	rel := rest[colon+1:]
	if strings.HasPrefix(host, "[") {
		if !validOrgBracketedIPv6(host) {
			return false
		}
	} else if !validOrgHost(host) {
		return false
	}
	return validOrgUser(user) && validOrgPath(rel)
}

func validOrgBracketedIPv6(host string) bool {
	end := strings.IndexByte(host, ']')
	if end < 0 || end+1 != len(host) {
		return false
	}
	inner := host[1:end]
	if !strings.ContainsRune(inner, ':') {
		return false
	}
	return allOrgHexColonDot(inner)
}

func allOrgHexColonDot(s string) bool {
	for i := range len(s) {
		c := s[i]
		if isOrgHex(c) || c == ':' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func isOrgHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func validOrgHost(s string) bool {
	if s == "" || len(s) > maxHostLen {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if !validOrgHostLabel(label) {
			return false
		}
	}
	return true
}

func validOrgHostLabel(label string) bool {
	if len(label) == 0 || len(label) > maxHostLabelLen {
		return false
	}
	return allOrgHostLabelBytes(label)
}

func allOrgHostLabelBytes(label string) bool {
	for j := range len(label) {
		c := label[j]
		if isOrgAlnum(c) {
			continue
		}
		if c == '-' && j != 0 && j != len(label)-1 {
			continue
		}
		return false
	}
	return true
}

func isOrgAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

//nolint:revive // host/port helper necessarily branches over bracketed and named hosts
func validOrgHostPort(s string) bool {
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 || !validOrgBracketedHostPortInner(s, end) {
			return false
		}
		return validOrgPortSuffix(s[end+1:])
	}
	host, port, hasPort := strings.Cut(s, ":")
	if hasPort {
		if len(port) == 0 || len(port) > maxPortLen {
			return false
		}
		if !validOrgPort(port) {
			return false
		}
	}
	return validOrgHost(host)
}
func validOrgBracketedHostPortInner(_ string, _ int) bool { return true }
func validOrgPortSuffix(suffix string) bool {
	if suffix == "" {
		return true
	}
	if !strings.HasPrefix(suffix, ":") {
		return false
	}
	port := suffix[1:]
	if len(port) == 0 || len(port) > maxPortLen {
		return false
	}
	return validOrgPort(port)
}

func validOrgPort(port string) bool {
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func validOrgUser(u string) bool {
	if u == "" || len(u) > maxUserLen {
		return false
	}
	return allOrgUserRunes(u)
}

func allOrgUserRunes(u string) bool {
	for i := range len(u) {
		c := u[i]
		if isOrgUserByte(c) {
			continue
		}
		return false
	}
	return true
}

func isOrgUserByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
}

func validOrgPath(p string) bool {
	if p == "" || p[0] == '-' {
		return false
	}
	for i := range len(p) {
		c := p[i]
		if c <= ' ' || c == controlDEL || c == '@' {
			return false
		}
	}
	return true
}

func looksLikeOrgGitSource(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	if i := strings.Index(s, ":/"); i >= 2 && validOrgScheme(s[:i]) {
		return true
	}
	at := strings.IndexByte(s, '@')
	if at > 0 && !strings.ContainsAny(s[:at], `/\`) {
		return strings.IndexByte(s[at+1:], ':') > 0
	}
	return false
}

func orgSecretShaped(s string) bool {
	return strings.Contains(s, "://") || schemeColonSlashOrg(s) ||
		(strings.Contains(s, "@") && strings.ContainsRune(s, ':'))
}

func maskedOrgRepo(repo string) string {
	if orgSecretShaped(repo) {
		return "<invalid source>"
	}
	return repo
}

func validOrgScheme(s string) bool {
	if s == "" || !isOrgSchemeLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isOrgSchemeLetter(c) && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}
func isOrgSchemeLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func schemeColonSlashOrg(s string) bool {
	i := strings.Index(s, ":/")
	return i >= 2 && validOrgScheme(s[:i])
}

// checkOrgRef refuses any ref that is not an immutable pin: only a 40-hex
// commit SHA or a semver tag (such as `v1.2.3`) is accepted, so a branch
// name like `main` can never float the governed rule set.
func checkOrgRef(ref, path string) *core.RefusalError {
	if hex40Ref(ref) || orgTagRE.MatchString(ref) {
		return nil
	}
	return refuse(CodeBadConfig,
		"%s.ref: %q is not immutable — use a 40-hex commit SHA or a semver tag like v1.2.3, never a branch name",
		path, ref)
}

// checkOrgPath refuses a pack root that could escape the central
// repository: absolute spellings, backslashes, and `..` segments are
// refused; `.` names the repository root.
func checkOrgPath(pinPath, base string) *core.RefusalError {
	if strings.HasPrefix(pinPath, "/") || strings.HasPrefix(pinPath, "-") ||
		strings.Contains(pinPath, `\`) || !cleanOrgString(pinPath) {
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
var orgTagRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

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
