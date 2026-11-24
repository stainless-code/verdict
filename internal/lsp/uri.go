package lsp

import (
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	schemeFile    = "file"
	hostLocal     = "localhost"
	uriPrefix     = "file://"
	slash         = "/"
	uncPrefix     = "//"
	dotDot        = ".."
	driveSepIndex = 1
	// driveRootLen is the length of `C:/`, the shortest absolute drive path.
	driveRootLen = 3
)

// uriError is the way a client URI fails to name a file under a served
// root. Neither kind is ever surfaced as a write target: a refused URI
// simply has no diagnostics.
type uriError string

func (e uriError) Error() string { return string(e) }

const (
	errUnsupportedURI uriError = "unsupported URI"
	errOutsideRoot    uriError = "URI outside every served root"
)

// servedRoot is one root the session serves: its query-layer identity, its
// absolute OS path, and the slash spelling the URI mapping compares against.
type servedRoot struct {
	ID      string
	Path    string
	slash   string
	host    string
	windows bool
}

// newServedRoot derives the URI-mapping form of one absolute root. windows
// selects the drive-letter and UNC rules; production passes the build's OS,
// tests pass both.
func newServedRoot(abs string, windows bool) servedRoot {
	clean := abs
	if windows {
		clean = strings.ReplaceAll(clean, `\`, slash)
	}
	root := servedRoot{ID: query.RootID(abs), Path: abs, windows: windows}
	if windows && strings.HasPrefix(clean, uncPrefix) {
		rest := strings.TrimPrefix(clean, uncPrefix)
		host, tail, _ := strings.Cut(rest, slash)
		root.host = host
		root.slash = path.Clean(slash + tail)
		return root
	}
	clean = path.Clean(clean)
	if windows && hasDrive(clean) {
		clean = strings.ToUpper(clean[:1]) + clean[1:]
	}
	root.slash = clean
	return root
}

// hasDrive reports a `C:` drive prefix.
func hasDrive(p string) bool {
	return len(p) >= 2 && p[driveSepIndex] == ':' && isASCIILetter(p[0])
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// relFromURI maps one client `file://` URI onto a root-relative slash path.
// The mapping is purely lexical: nothing is read, stat'd, or resolved, so a
// symlink cannot widen it. Anything that is not an absolute file URI naming a
// file strictly under the root refuses.
func (r *servedRoot) relFromURI(raw string) (string, error) {
	u, err := parseFileURI(raw)
	if err != nil {
		return "", err
	}
	if !r.ownsHost(u.Host) {
		return "", errOutsideRoot
	}
	return r.relFromPath(u.Path)
}

// parseFileURI accepts one clean absolute `file:` URI with no user, query,
// or fragment; its path is percent-decoded exactly once.
func parseFileURI(raw string) (*url.URL, error) {
	if !cleanText(raw) {
		return nil, errUnsupportedURI
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, schemeFile) || u.Opaque != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || !cleanText(u.Path) {
		return nil, errUnsupportedURI
	}
	return u, nil
}

// ownsHost reports whether a URI authority names this root's machine: empty
// or localhost for a local root, the root's own host for a UNC root.
func (r *servedRoot) ownsHost(host string) bool {
	if host == "" || strings.EqualFold(host, hostLocal) {
		return r.host == ""
	}
	return r.host != "" && strings.EqualFold(host, r.host)
}

// relFromPath confines one decoded absolute URI path to the root.
func (r *servedRoot) relFromPath(p string) (string, error) {
	p = r.osForm(p)
	if !r.absolute(p) {
		return "", errUnsupportedURI
	}
	p = path.Clean(p)
	prefix := strings.TrimSuffix(r.slash, slash) + slash
	if len(p) <= len(prefix) || !r.samePrefix(p[:len(prefix)], prefix) {
		return "", errOutsideRoot
	}
	rel := p[len(prefix):]
	if confineRel(rel) != nil {
		return "", errOutsideRoot
	}
	return rel, nil
}

// osForm rewrites a Windows URI path into drive form: backslashes become
// slashes and the leading slash before a drive letter goes.
func (r *servedRoot) osForm(p string) string {
	if !r.windows {
		return p
	}
	p = strings.ReplaceAll(p, `\`, slash)
	if strings.HasPrefix(p, slash) && hasDrive(p[1:]) {
		return p[1:]
	}
	return p
}

// absolute reports an absolute path in the root's convention.
func (r *servedRoot) absolute(p string) bool {
	if r.windows && r.host == "" {
		return hasDrive(p) && len(p) >= driveRootLen && p[driveRootLen-1] == '/'
	}
	return strings.HasPrefix(p, slash)
}

// samePrefix compares the root prefix: Windows paths fold case (the volume
// is case-insensitive), POSIX paths compare bytes.
func (r *servedRoot) samePrefix(got, want string) bool {
	if r.windows {
		return strings.EqualFold(got, want)
	}
	return got == want
}

// uriFor renders the `file://` URI of one root-relative slash path: each
// segment percent-escaped, the drive letter or UNC host kept.
func (r *servedRoot) uriFor(rel string) string {
	full := strings.TrimSuffix(r.slash, slash) + slash + rel
	switch {
	case r.host != "":
		return uriPrefix + r.host + untrusted.EscapeURLPath(full)
	case r.windows:
		return uriPrefix + slash + untrusted.EscapeURLPath(full)
	default:
		return uriPrefix + untrusted.EscapeURLPath(full)
	}
}

// pathKey is the lookup key of one root-relative path: Windows folds case so
// a client spelling that differs only in case still finds its findings.
func (r *servedRoot) pathKey(rel string) string {
	if r.windows {
		return strings.ToLower(rel)
	}
	return rel
}

// confineRel refuses a root-relative slash path that could name anything
// outside the root: empty, absolute, drive-qualified, backslashed, unclean,
// or carrying a parent segment or a control rune.
func confineRel(rel string) error {
	switch {
	case rel == "", rel == ".", !cleanText(rel):
		return errOutsideRoot
	case strings.HasPrefix(rel, slash), strings.ContainsRune(rel, '\\'), hasDrive(rel):
		return errOutsideRoot
	case path.Clean(rel) != rel:
		return errOutsideRoot
	}
	if slices.Contains(strings.Split(rel, slash), dotDot) {
		return errOutsideRoot
	}
	return nil
}

// cleanText refuses invalid UTF-8, any control rune, and leading or trailing
// space.
func cleanText(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// osRoot is the absolute, cleaned spelling of one operator-supplied root.
func osRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return abs
}
