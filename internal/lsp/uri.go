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
	ID    string
	Path  string
	slash string
	host  string
	// physical is the root's resolved slash spelling (symlinks, and Windows
	// 8.3 short names, expanded), or "" when it is not known. A client
	// document spelled through another path to the same tree is matched
	// against it (see Server.relOf).
	physical string
	windows  bool
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
	return r.relUnder(r.slash, path.Clean(p))
}

// relUnder confines one cleaned absolute slash path under base, comparing
// the prefix case-insensitively on Windows.
func (r *servedRoot) relUnder(base, p string) (string, error) {
	prefix := strings.TrimSuffix(base, slash) + slash
	if len(p) <= len(prefix) || !r.samePrefix(p[:len(prefix)], prefix) {
		return "", errOutsideRoot
	}
	rel := p[len(prefix):]
	if confineRel(rel) != nil {
		return "", errOutsideRoot
	}
	return rel, nil
}

// relFromPhysical confines a resolved document path (see physicalPath)
// under the root's resolved spelling. A root whose spelling is unknown, or
// a UNC root, confines nothing this way.
func (r *servedRoot) relFromPhysical(phys string) (string, error) {
	if r.physical == "" || phys == "" || r.host != "" {
		return "", errOutsideRoot
	}
	return r.relUnder(r.physical, phys)
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

// fileKey is the root-independent identity of one file under the root: its
// cleaned absolute slash path (UNC host included), case-folded on Windows.
// Two nested roots that both reach a file agree on its key, so their
// findings merge into one publish.
func (r *servedRoot) fileKey(rel string) string {
	return pathIdentity(r.host, strings.TrimSuffix(r.slash, slash)+slash+rel, r.windows)
}

func pathIdentity(host, p string, windows bool) string {
	if host != "" {
		p = uncPrefix + host + p
	}
	if windows {
		return strings.ToLower(p)
	}
	return p
}

// absFromURI decodes one client `file://` URI into its host (UNC only) and
// cleaned absolute slash path, refusing what relFromURI would refuse.
func absFromURI(raw string, windows bool) (host, p string, err error) {
	u, err := parseFileURI(raw)
	if err != nil {
		return "", "", err
	}
	if u.Host != "" && !strings.EqualFold(u.Host, hostLocal) {
		if !windows {
			return "", "", errOutsideRoot
		}
		host = u.Host
	}
	probe := servedRoot{windows: windows, host: host}
	p = probe.osForm(u.Path)
	if !probe.absolute(p) {
		return "", "", errUnsupportedURI
	}
	return host, path.Clean(p), nil
}

// osPathFromURI maps a client workspace-folder URI onto an absolute OS path,
// with the same decoding and refusals as a document URI.
func osPathFromURI(raw string, windows bool) (string, error) {
	host, p, err := absFromURI(raw, windows)
	if err != nil {
		return "", err
	}
	if host != "" {
		p = uncPrefix + host + p
	}
	return filepath.FromSlash(p), nil
}

// slashForm renders an absolute OS path in the slash convention the URI
// mapping compares: separators normalized on Windows, cleaned, and the drive
// letter upper-cased.
func slashForm(p string, windows bool) string {
	if windows {
		p = strings.ReplaceAll(p, `\`, slash)
	}
	p = path.Clean(p)
	if windows && hasDrive(p) {
		p = strings.ToUpper(p[:1]) + p[1:]
	}
	return p
}

// physicalPath resolves an absolute slash path through eval (symlinks, and on
// Windows 8.3 short names) to the slash form of its physical spelling. A path
// that does not exist yet (a new, unsaved file) resolves through its deepest
// existing ancestor with the rest appended. ok is false when nothing on the
// path resolves. eval takes and returns OS-native spellings; it only ever
// lstats, never opens a file.
func physicalPath(p string, windows bool, eval func(string) (string, error)) (string, bool) {
	cur, rest := slashForm(p, windows), ""
	for {
		if resolved, err := eval(evalProbe(cur, windows)); err == nil {
			return slashForm(path.Join(slashForm(resolved, windows), rest), windows), true
		}
		parent := path.Dir(cur)
		if parent == cur || parent == "." {
			return "", false
		}
		rest = path.Join(path.Base(cur), rest)
		cur = parent
	}
}

// evalProbe is the spelling eval receives: `C:` alone is drive-relative on
// Windows, so the drive root is probed as `C:/`.
func evalProbe(cur string, windows bool) string {
	if windows && len(cur) == 2 && hasDrive(cur) {
		return cur + slash
	}
	return cur
}

// evalOS is the production resolver: [filepath.EvalSymlinks] over the OS form
// of a slash path.
func evalOS(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.FromSlash(p))
	if err != nil {
		return "", &ioError{op: "resolve path", err: err}
	}
	return resolved, nil
}
