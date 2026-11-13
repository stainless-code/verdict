// Package sarif owns SARIF 2.1.0 tier mapping, safe path normalization, and
// bounded parsing shared by ingest and report.
package sarif

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	skillfs "github.com/stainless-code/verdict"
)

const mapAssetPath = "schemas/sarif-map.json"

// Table describes schemas/sarif-map.json: level -> tier, numeric
// security-severity thresholds -> tier, and verdict tier -> SARIF level.
type Table struct {
	SchemaVersion              string              `json:"schema_version"`
	DefaultTier                string              `json:"default_tier"`
	Levels                     map[string]string   `json:"levels"`
	SecuritySeverityThresholds []SeverityThreshold `json:"security_severity_thresholds"`
	TierToLevel                map[string]string   `json:"tier_to_level"`
}

// SeverityThreshold maps a minimum numeric security-severity score to a tier.
type SeverityThreshold struct {
	Tier string  `json:"tier"`
	Min  float64 `json:"min"`
}

var loadTableOnce = sync.OnceValues(func() (*Table, error) {
	raw, err := skillfs.Read(mapAssetPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", mapAssetPath, err)
	}
	var tbl Table
	if err := json.Unmarshal(raw, &tbl); err != nil {
		return nil, fmt.Errorf("decode %s: %v", mapAssetPath, err)
	}
	sort.SliceStable(tbl.SecuritySeverityThresholds, func(i, j int) bool {
		return tbl.SecuritySeverityThresholds[i].Min > tbl.SecuritySeverityThresholds[j].Min
	})
	return &tbl, nil
})

// Mapping returns the loaded SARIF tier/level mapping table from schemas/sarif-map.json.
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
// numeric string) and level to a verdict severity tier via schemas/sarif-map.json.
// Valid finite security-severity in [0, 10] takes precedence over level.
func TierForResult(level string, secSev any) (tier string, warn string) {
	tbl, err := Mapping()
	if err != nil {
		return "important", err.Error()
	}
	if score, ok := parseSecuritySeverity(secSev); ok {
		for _, th := range tbl.SecuritySeverityThresholds {
			if score >= th.Min {
				return th.Tier, ""
			}
		}
	}
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
	if v == nil || v == false {
		return 0, false
	}
	var score float64
	switch n := v.(type) {
	case float64:
		score = n
	case float32:
		score = float64(n)
	case int:
		score = float64(n)
	case int64:
		score = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		score = parsed
	case string:
		trimmed := strings.TrimSpace(n)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		score = parsed
	default:
		return 0, false
	}
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 10 {
		return 0, false
	}
	return score, true
}

// CleanURI validates and normalizes a SARIF artifactLocation.uri into a
// repository-relative POSIX path, rejecting NUL bytes, backslashes, Windows
// drive prefixes, URL schemes, leading slashes, and ".." traversal segments
// (both raw and percent-decoded).
func CleanURI(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if err := checkRawURISafety(raw); err != nil {
		return "", err
	}
	unescaped, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid SARIF URI escape in %q", raw)
	}
	if err := checkRawURISafety(unescaped); err != nil {
		return "", err
	}
	cleaned := path.Clean(unescaped)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("unsafe SARIF artifact URI %q", raw)
	}
	return cleaned, nil
}

func checkRawURISafety(s string) error {
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("unsafe SARIF artifact URI %q (NUL byte)", s)
	}
	if strings.Contains(s, "\\") {
		return fmt.Errorf("unsafe SARIF artifact URI %q (backslash)", s)
	}
	if strings.HasPrefix(s, "/") {
		return fmt.Errorf("unsafe SARIF artifact URI %q (absolute path)", s)
	}
	if len(s) >= 2 && isASCIIAlpha(s[0]) && s[1] == ':' {
		return fmt.Errorf("unsafe SARIF artifact URI %q (drive path)", s)
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		return fmt.Errorf("unsafe SARIF artifact URI %q (URI scheme %q)", s, u.Scheme)
	}
	if strings.Contains(s, "://") {
		return fmt.Errorf("unsafe SARIF artifact URI %q (URI scheme)", s)
	}
	if slices.Contains(strings.Split(s, "/"), "..") {
		return fmt.Errorf("unsafe SARIF artifact URI %q (parent traversal)", s)
	}
	return nil
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
