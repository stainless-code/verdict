// Package sarif owns SARIF 2.1.0 tier mapping, safe path normalization, and
// bounded parsing shared by ingest and report.
package sarif

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	skillfs "github.com/stainless-code/verdict"
)

const (
	tierMapFile      = "schemas/sarif-map.json"
	defaultTier      = "important"
	floatBitSize     = 64
	maxSecurityScore = 10
	fileScheme       = "file:"
	pathSeparator    = "/"
	windowsSeparator = `\`
)

// Table describes schemas/sarif-map.json: level -> tier, numeric
// security-severity thresholds -> tier, and verdict tier -> SARIF level.
type Table struct {
	Levels                     map[string]string   `json:"levels"`
	TierToLevel                map[string]string   `json:"tier_to_level"`
	SchemaVersion              string              `json:"schema_version"`
	DefaultTier                string              `json:"default_tier"`
	SecuritySeverityThresholds []SeverityThreshold `json:"security_severity_thresholds"`
}

// SeverityThreshold maps a minimum numeric security-severity score to a tier.
type SeverityThreshold struct {
	Tier string  `json:"tier"`
	Min  float64 `json:"min"`
}

var loadTableOnce = sync.OnceValues(func() (*Table, error) {
	raw, err := skillfs.Read(tierMapFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tierMapFile, err)
	}
	var tbl Table
	if err := json.Unmarshal(raw, &tbl); err != nil {
		return nil, fmt.Errorf("decode %s: %w", tierMapFile, err)
	}
	slices.SortStableFunc(tbl.SecuritySeverityThresholds, func(a, b SeverityThreshold) int {
		return cmp.Compare(b.Min, a.Min)
	})
	return &tbl, nil
})

// Mapping returns the loaded SARIF tier/level mapping table from
// schemas/sarif-map.json.
func Mapping() (*Table, error) {
	return loadTableOnce()
}

// LevelForTier maps a verdict severity value to its SARIF 2.1.0 level using
// schemas/sarif-map.json, defaulting to "note" for absent or unknown values.
func LevelForTier(sev any) string {
	tbl, err := Mapping()
	if err == nil {
		if s, ok := sev.(string); ok {
			if lvl, found := tbl.TierToLevel[s]; found && lvl != "" {
				return lvl
			}
		}
	}
	return "note"
}

// TierForResult maps a SARIF result's security-severity (optional float or
// numeric string) and level to a verdict severity tier via
// schemas/sarif-map.json. Valid finite security-severity in [0, 10] takes
// precedence over level.
func TierForResult(level string, secSev any) (tier, warn string) {
	tbl, err := Mapping()
	if err != nil {
		return defaultTier, err.Error()
	}
	if tier, ok := tierFromScore(tbl, secSev); ok {
		return tier, ""
	}
	return tierFromLevel(tbl, level)
}

func tierFromScore(tbl *Table, secSev any) (string, bool) {
	score, ok := parseSecuritySeverity(secSev)
	if !ok {
		return "", false
	}
	for _, th := range tbl.SecuritySeverityThresholds {
		if score >= th.Min {
			return th.Tier, true
		}
	}
	return "", false
}

func tierFromLevel(tbl *Table, level string) (tier, warn string) {
	norm := strings.ToLower(strings.TrimSpace(level))
	if norm == "" {
		return tbl.DefaultTier, ""
	}
	if mapped, ok := tbl.Levels[norm]; ok {
		return mapped, ""
	}
	return tbl.DefaultTier, fmt.Sprintf("unknown SARIF level %q — defaulting to %s", level, tbl.DefaultTier)
}

func parseSecuritySeverity(v any) (float64, bool) {
	score, ok := decodeScore(v)
	if !ok || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > maxSecurityScore {
		return 0, false
	}
	return score, true
}

func decodeScore(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		parsed, err := n.Float64()
		return parsed, err == nil
	case string:
		trimmed := strings.TrimSpace(n)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, floatBitSize)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// CleanURI validates and normalizes a SARIF artifactLocation.uri into a
// repository-relative POSIX path. A plain relative URI, or a path-only `file:`
// URI (`file:src/a.go`), keeps its repo-relative path; Windows separators
// become `/` and percent escapes are decoded once. Everything that names a
// location outside the checkout is refused: NUL bytes, ASCII control chars,
// invalid UTF-8, leading or trailing whitespace, an absolute path, a Windows
// drive path, any colon, a foreign scheme, a `..` traversal segment, and every
// authority-bearing `file://` reference. RFC 8089 gives `file://<authority>`
// its authority component (empty only in `file:///`), so `file:///abs/path`,
// `file://localhost/etc/passwd`, `file://[::1]/x`, and `file://host/share/x`
// all escape this repository and are never read as repo-relative paths.
func CleanURI(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	body := normalizeSeparators(raw)
	if rest, ok := fileReferenceBody(body); ok {
		body = rest
	}
	if reason := unsafeURIReason(body); reason != "" {
		return "", unsafeURIError(raw, reason)
	}
	unescaped, err := url.PathUnescape(body)
	if err != nil {
		//nolint:wrapcheck // local URI syntax rejection
		return "", fmt.Errorf("invalid SARIF URI escape in %q: %w", raw, err)
	}
	unescaped = normalizeSeparators(unescaped)
	if !utf8.ValidString(unescaped) {
		return "", unsafeURIError(raw, "invalid UTF-8")
	}
	if reason := unsafeURIReason(unescaped); reason != "" {
		return "", unsafeURIError(unescaped, reason)
	}
	cleaned, ok := cleanRelative(unescaped)
	if !ok {
		return "", errors.New("unsafe SARIF artifact URI " + strconv.Quote(raw)) //nolint:wrapcheck // local validation failure
	}
	return cleaned, nil
}

// cleanRelative reduces an already-graded path to its cleaned repository form,
// reporting false when cleaning leaves nothing readable (the current directory
// itself or a parent of it).
func cleanRelative(s string) (string, bool) {
	cleaned := strings.TrimPrefix(path.Clean(s), "./")
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, pathSeparator) || strings.HasPrefix(cleaned, "..") {
		return "", false
	}
	return cleaned, true
}

// normalizeSeparators turns Windows separators into the POSIX one so a
// scanner that reports `pkg\\auth.go` anchors on the same file as
// `pkg/auth.go`. Traversal is judged after this normalization, so `..\\..` is
// refused rather than read as a file name.
func normalizeSeparators(s string) string {
	if !strings.Contains(s, windowsSeparator) {
		return s
	}
	return strings.ReplaceAll(s, windowsSeparator, pathSeparator)
}

// fileReferenceBody strips a `file:` scheme so the path after it is graded like
// any other: `file:src/a.go` is the one repo-relative file form this build
// reads. Every `file://` reference keeps its `//` and is refused as an absolute
// path (RFC 8089 authority), so no host or share name is ever read as a
// directory inside the checkout.
func fileReferenceBody(s string) (string, bool) {
	if len(s) < len(fileScheme) || !strings.EqualFold(s[:len(fileScheme)], fileScheme) {
		return s, false
	}
	return s[len(fileScheme):], true
}

func unsafeURIError(uri, reason string) error {
	//nolint:wrapcheck // local validation failure
	return errors.New("unsafe SARIF artifact URI " + strconv.Quote(uri) + " (" + reason + ")")
}

func unsafeURIReason(s string) string {
	switch {
	case strings.ContainsRune(s, 0):
		return "NUL byte"
	case hasASCIIControl(s):
		return "control character"
	case strings.TrimSpace(s) != s:
		return "leading or trailing whitespace"
	case strings.HasPrefix(s, pathSeparator):
		return "absolute path"
	case len(s) >= 2 && isASCIIAlpha(s[0]) && s[1] == ':':
		return "drive path"
	case hasURIScheme(s):
		return "URI scheme"
	case strings.ContainsRune(s, ':'):
		// A colon never appears in a repository path this build accepts: left in
		// place it would smuggle a drive letter (`localhost/C:/x`) or a scheme
		// past the checks above.
		return "colon in path segment"
	case slices.Contains(strings.Split(s, pathSeparator), ".."):
		return "parent traversal"
	default:
		return ""
	}
}

const (
	minControlByte = 0x20
	asciiDEL       = 0x7f
)

func hasASCIIControl(s string) bool {
	for i := range len(s) {
		if s[i] < minControlByte || s[i] == asciiDEL {
			return true
		}
	}
	return false
}

func hasURIScheme(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme != ""
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
