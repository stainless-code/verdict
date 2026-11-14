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

	skillfs "github.com/stainless-code/verdict"
)

const (
	tierMapFile      = "schemas/sarif-map.json"
	defaultTier      = "important"
	floatBitSize     = 64
	maxSecurityScore = 10
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
// repository-relative POSIX path, rejecting NUL bytes, ASCII control chars,
// leading or trailing whitespace, backslashes, Windows drive prefixes, URL
// schemes, leading slashes, and ".." traversal segments (both raw and
// percent-decoded).
func CleanURI(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if reason := unsafeURIReason(raw); reason != "" {
		return "", unsafeURIError(raw, reason)
	}
	unescaped, err := url.PathUnescape(raw)
	if err != nil {
		//nolint:wrapcheck // local URI syntax rejection
		return "", fmt.Errorf("invalid SARIF URI escape in %q: %w", raw, err)
	}
	if reason := unsafeURIReason(unescaped); reason != "" {
		return "", unsafeURIError(unescaped, reason)
	}
	cleaned := strings.TrimPrefix(path.Clean(unescaped), "./")
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "..") {
		return "", errors.New("unsafe SARIF artifact URI " + strconv.Quote(raw)) //nolint:wrapcheck // local validation failure
	}
	return cleaned, nil
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
	case strings.Contains(s, "\\"):
		return "backslash"
	case strings.HasPrefix(s, "/"):
		return "absolute path"
	case len(s) >= 2 && isASCIIAlpha(s[0]) && s[1] == ':':
		return "drive path"
	case hasURIScheme(s):
		return "URI scheme"
	case slices.Contains(strings.Split(s, "/"), ".."):
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
