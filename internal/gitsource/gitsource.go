// Package gitsource is the repository's one git-remote allowlist: the exact
// https, ssh, and scp source shapes pack add, pack sync, and the base-owned
// org pin accept, with no userinfo password, no query, and no fragment. It
// imports no repository package, so the config parser and the pack layer
// share one grammar instead of two drifting copies.
package gitsource

import (
	"net/netip"
	"regexp"
	"strings"
)

// Source bounds: an ssh user is 1..64 characters, a DNS name is at most 253
// bytes in 1..63-byte labels, and a port is at most 5 digits.
const (
	maxSourceUser = 64
	maxHostLen    = 253
	maxHostLabel  = 63
	maxPortLen    = 5
	controlDEL    = 0x7f
	schemeSep     = "://"
	schemeSlash   = ":/"
	refSep        = "@"
)

// refGrammar gates the optional @ref before it reaches a git argv.
var refGrammar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]*$`)

// Source is one allowlisted git remote and its optional @ref: URL is the
// canonical spelling a lock row records, and Ref is empty when the source
// carried none.
type Source struct {
	URL string
	Ref string
}

// Parse accepts exactly the published git sources: https://host/path with no
// userinfo, ssh://user@host/path, and user@host:path, each with an optional
// @ref. The scheme is matched case-insensitively and stored lower-case. A
// query or fragment refuses: a source is recorded in the committed lock, so
// nothing after the path may carry a token.
func Parse(s string) (Source, bool) {
	if strings.ContainsAny(s, "?#") {
		return Source{}, false
	}
	if base, ref, ok := splitRef(s); ok {
		url, ok := ParseURL(base)
		if !ok {
			return Source{}, false
		}
		return Source{URL: url, Ref: ref}, true
	}
	url, ok := ParseURL(s)
	if !ok {
		return Source{}, false
	}
	return Source{URL: url}, true
}

// ParseURL parses one allowlisted git URL that carries no @ref. It is the
// grammar a configuration value passes: a caller that stores the ref in its
// own field must never have a suffix silently read as one.
func ParseURL(s string) (string, bool) {
	if strings.ContainsAny(s, "?#") {
		return "", false
	}
	if scheme, rest, ok := sourceScheme(s); ok {
		switch strings.ToLower(scheme) {
		case "https":
			return parseHTTPSource(rest)
		case "ssh":
			return parseSSHSource(rest)
		}
		return "", false
	}
	return parseSCPSource(s)
}

// LooksLike reports whether s asks to be a git remote. Anything that does
// refuses when the allowlist rejects it, instead of falling through to a
// directory label.
func LooksLike(s string) bool {
	if strings.Contains(s, schemeSep) || schemeColonSlash(s) {
		return true
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || strings.ContainsAny(s[:at], `/\`) {
		return false
	}
	return strings.IndexByte(s[at+1:], ':') > 0
}

// SecretShaped reports whether s carries a URL or userinfo shape that must
// never be echoed, including inside a dir: label.
func SecretShaped(s string) bool {
	return strings.Contains(s, schemeSep) || schemeColonSlash(s) ||
		(strings.Contains(s, refSep) && strings.ContainsRune(s, ':'))
}

// splitRef cuts the optional @ref at the first @ after a URL authority, or at
// the last @ of a possible scp source. The suffix must match the ref grammar,
// so a bare git@host:path stays one source.
func splitRef(s string) (string, string, bool) {
	if schemeEnd := strings.Index(s, schemeSep); schemeEnd > 0 {
		return splitURLRef(s, schemeEnd)
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || !refGrammar.MatchString(s[at+1:]) {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}

// splitURLRef cuts a URL source at the first @ after its authority.
func splitURLRef(s string, schemeEnd int) (string, string, bool) {
	rest := schemeEnd + len(schemeSep)
	slash := strings.IndexByte(s[rest:], '/')
	if slash < 0 {
		return s, "", false
	}
	rest += slash
	at := strings.IndexByte(s[rest:], '@')
	if at < 0 {
		return s, "", false
	}
	at += rest
	if !refGrammar.MatchString(s[at+1:]) {
		return s, "", false
	}
	return s[:at], s[at+1:], true
}

// sourceScheme splits one scheme:// prefix.
func sourceScheme(s string) (string, string, bool) {
	i := strings.Index(s, schemeSep)
	if i <= 0 {
		return "", "", false
	}
	scheme := s[:i]
	if !validScheme(scheme) {
		return "", "", false
	}
	return scheme, s[i+len(schemeSep):], true
}

// parseHTTPSource accepts https://HOST[:PORT]/PATH with no userinfo.
func parseHTTPSource(rest string) (string, bool) {
	authority, rel, ok := splitAuthorityPath(rest)
	if !ok || strings.Contains(authority, refSep) || !validHostPort(authority) || !validPath(rel) {
		return "", false
	}
	return "https://" + authority + "/" + rel, true
}

// parseSSHSource accepts ssh://USER@HOST[:PORT]/PATH.
func parseSSHSource(rest string) (string, bool) {
	authority, rel, ok := splitAuthorityPath(rest)
	if !ok {
		return "", false
	}
	user, hostPort, ok := splitSourceUser(authority)
	if !ok || !validSourceUser(user) || !validHostPort(hostPort) || !validPath(rel) {
		return "", false
	}
	return "ssh://" + user + "@" + hostPort + "/" + rel, true
}

// splitAuthorityPath cuts a URL authority at its first slash; both sides must
// be non-empty.
func splitAuthorityPath(rest string) (string, string, bool) {
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

// splitSourceUser splits one user@host authority at its single @.
func splitSourceUser(authority string) (string, string, bool) {
	at := strings.IndexByte(authority, '@')
	if at <= 0 || at == len(authority)-1 || strings.Contains(authority[at+1:], refSep) {
		return "", "", false
	}
	return authority[:at], authority[at+1:], true
}

// parseSCPSource accepts the scp form USER@HOST:PATH.
func parseSCPSource(s string) (string, bool) {
	if strings.Contains(s, schemeSep) {
		return "", false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "", false
	}
	user, rest := s[:at], s[at+1:]
	host, rel, ok := splitSCPHost(rest)
	if !ok || !validSourceUser(user) || !validHost(host) || !validPath(rel) {
		return "", false
	}
	return user + "@" + host + ":" + rel, true
}

// splitSCPHost cuts one scp destination at the colon after its host, or after
// a bracketed IPv6 literal.
func splitSCPHost(rest string) (string, string, bool) {
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ':' {
			return "", "", false
		}
		return rest[:end+1], rest[end+2:], true
	}
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return "", "", false
	}
	return rest[:colon], rest[colon+1:], true
}

// validHostPort validates HOST or HOST:PORT, with a bracketed IPv6 host.
func validHostPort(s string) bool {
	if strings.HasPrefix(s, "[") {
		return validBracketedHostPort(s)
	}
	host, port, hasPort := strings.Cut(s, ":")
	return (!hasPort || validPort(port)) && validHost(host)
}

// validBracketedHostPort validates [ipv6] with its optional :port.
func validBracketedHostPort(s string) bool {
	end := strings.IndexByte(s, ']')
	if end < 0 || !validHost(s[:end+1]) {
		return false
	}
	rest := s[end+1:]
	return rest == "" || (strings.HasPrefix(rest, ":") && validPort(rest[1:]))
}

// validHost accepts a DNS name, an IPv4 address, or a bracketed IPv6 literal.
func validHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		return strings.HasSuffix(host, "]") && validIPv6(host[1:len(host)-1])
	}
	if host == "" || len(host) > maxHostLen {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !validHostLabel(label) {
			return false
		}
	}
	return true
}

// validHostLabel accepts one DNS label: 1..63 bytes, alphanumeric at both
// ends, hyphen inside.
func validHostLabel(label string) bool {
	if label == "" || len(label) > maxHostLabel {
		return false
	}
	for i := range len(label) {
		c := label[i]
		if !isAlnum(c) && (c != '-' || i == 0 || i == len(label)-1) {
			return false
		}
	}
	return true
}

// validIPv6 accepts the inside of a bracketed IPv6 literal only when it is a
// syntactically valid IPv6 address with no zone: the grammar guards an argv,
// so a shape a resolver would refuse (`2001:::1`) or one that names an
// interface (`fe80::1%eth0`) must not reach git.
func validIPv6(inner string) bool {
	if inner == "" {
		return false
	}
	addr, err := netip.ParseAddr(inner)
	return err == nil && addr.Is6() && addr.Zone() == ""
}

// validSourceUser accepts the bare ssh user grammar: 1..64 of [A-Za-z0-9._-].
func validSourceUser(user string) bool {
	if user == "" || len(user) > maxSourceUser {
		return false
	}
	for i := range len(user) {
		c := user[i]
		if !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validPath accepts a source path with no whitespace, control character, @,
// or leading '-'.
func validPath(rel string) bool {
	if rel == "" || rel[0] == '-' {
		return false
	}
	for i := range len(rel) {
		c := rel[i]
		if c <= ' ' || c == controlDEL || c == '@' {
			return false
		}
	}
	return true
}

// validPort accepts 1..5 decimal digits.
func validPort(port string) bool {
	if port == "" || len(port) > maxPortLen {
		return false
	}
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// schemeColonSlash matches a scheme and a single slash, the mangled URL shape
// that would otherwise read as a directory path. Two-letter-plus schemes keep
// a Windows drive prefix ("C:/packs") a path.
func schemeColonSlash(s string) bool {
	i := strings.Index(s, schemeSlash)
	return i >= 2 && validScheme(s[:i])
}

// validScheme reports whether s is an RFC 3986 URL scheme.
func validScheme(s string) bool {
	if s == "" || !isSchemeLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isSchemeByte(s[i]) {
			return false
		}
	}
	return true
}

func isSchemeLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSchemeByte(c byte) bool {
	return isSchemeLetter(c) || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}
