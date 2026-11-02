// Package overrides ports the P5 section-override fragments: operator-owned
// per-block templates under .review/overrides that reword fixed blocks.
// Absent fragments pass silently (parity: no overrides reads today's
// bytes); any violation fails closed with a typed bad-override refusal.
package overrides

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
)

// Dir and fragment paths, mirroring the shell's OVERRIDES_DIR globals.
const (
	Dir             = ".review/overrides"
	Preamble        = Dir + "/preamble.md"
	FooterOrderPath = Dir + "/footer-order.md"
	Threshold       = Dir + "/walkthrough-threshold.md"
	Attribution     = Dir + "/attribution.md"
)

var (
	markerLine    = regexp.MustCompile(`(?m)^> ✅|^> ℹ️|^> \[!IMPORTANT\]|^> \[!CAUTION\]`)
	sectionLine   = regexp.MustCompile(`(?m)^### `)
	reportMeta    = regexp.MustCompile(`<!--[[:space:]]*report-machine`)
	verdictDiv    = regexp.MustCompile(`<!--[[:space:]]*verdict-`)
	reviewMetaLit = "Review metadata"
	newline       = "\n"
)

// CheckLocked ports override_check_locked over one fragment's raw text.
func CheckLocked(path, text string) error {
	switch {
	case markerLine.MatchString(text):
		return cliRef(path, "marker-first-line", "the verdict marker stays the body first line — fragments never carry it")
	case sectionLine.MatchString(text):
		return cliRef(path, "findings-structure", "body ### sections stay agent-owned — fragments never add them")
	case reportMeta.MatchString(text):
		return cliRef(path, "metadata-presence", "machine metadata comments stay script-owned — fragments never carry them")
	case strings.Contains(text, reviewMetaLit):
		return cliRef(path, "metadata-presence", "machine metadata comments stay script-owned — fragments never name them")
	case verdictDiv.MatchString(text):
		return cliRef(path, "divider-shape", "machine dividers stay byte-exact — fragments never carry them")
	default:
		return nil
	}
}

// cliRef builds the bad-override refusal for one invariant.
func cliRef(path, invariant, why string) error {
	return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf(
		"override %s violates locked invariant '%s' (%s)", path, invariant, why)}
}

// FooterOrder ports override_footer_order: one field per line from
// tier|cost|files|link, unique, non-empty; defaults to the full order.
func FooterOrder() ([]string, error) {
	def := []string{"tier", "cost", "files", "link"}
	if raw, err := os.ReadFile(FooterOrderPath); err == nil {
		return parseOrderOrDef(raw)
	}
	// A missing fragment file reads as the default order.
	return def, nil
}

// parseOrderOrDef parses the fragment, refusing when it lists no fields.
func parseOrderOrDef(raw []byte) ([]string, error) {
	order, ref := parseOrderLines(string(raw))
	if ref != nil {
		return nil, ref
	}
	if len(order) == 0 {
		return nil, &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates 'footer-order-subset' (no fields "+
			"listed — want a non-empty subset of tier, cost, files, link)", FooterOrderPath)}
	}
	return order, nil
}

// parseOrderLines parses the footer-order fragment lines.
func parseOrderLines(raw string) ([]string, error) {
	var order []string
	seen := map[string]bool{}
	for ln := range strings.SplitSeq(raw, newline) {
		// The shell reads with `|| [[ -n "$line" ]]` so a final unterminated
		// line still counts; Split keeps it as the last element. A trailing
		// newline yields a final empty element that skips below.
		line := strings.TrimSpace(strings.ReplaceAll(ln, "\r", ""))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if ref := checkOrderField(line, seen); ref != nil {
			return nil, ref
		}
		seen[line] = true
		order = append(order, line)
	}
	return order, nil
}

// checkOrderField gates one footer-order field: known subset, listed once.
func checkOrderField(line string, seen map[string]bool) error {
	switch line {
	case "tier", "cost", "files", "link":
	default:
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates 'footer-order-subset' "+
			"(unknown field '%s' — want only tier, cost, files, link)", FooterOrderPath, line)}
	}
	if seen[line] {
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates 'footer-order-subset' "+
			"(duplicate field '%s' — list each field once)", FooterOrderPath, line)}
	}
	return nil
}

// Thresholds ports override_thresholds: `files: N` / `bytes: N` lines (both
// `:` and `=` forms after whitespace removal), defaulting to 8 files and
// Override threshold parsing constants.
const (
	// defaultMaxFiles and defaultMaxBytes are the degenerate-guard defaults.
	defaultMaxFiles = 8
	defaultMaxBytes = 24576
	// maxThresholdDigits caps threshold values to 64-bit arithmetic.
	maxThresholdDigits = 15
	// decimalBase is the radix for threshold parsing.
	decimalBase = 10
	// codeBadOverride names the refusal code for fragment violations.
	codeBadOverride = "bad-override"
	// keyFiles names the files threshold key.
	keyFiles = "files"
)

// Thresholds ports override thresholds: 8 files / 24576 bytes defaults,
// degenerate-guarded by the locked invariants.
func Thresholds() (files, bytes int, ref error) {
	files, bytes = defaultMaxFiles, defaultMaxBytes
	raw, err := os.ReadFile(Threshold)
	if err != nil {
		return files, bytes, nil
	}
	seen := map[string]bool{}
	vals := map[string]int{}
	for ln := range strings.SplitSeq(string(raw), newline) {
		key, val, skip, ref := parseThresholdEntry(ln, seen)
		if ref != nil {
			return 0, 0, ref
		}
		if skip {
			continue
		}
		seen[key] = true
		vals[key] = val
	}
	applyThresholds(vals, &files, &bytes)
	return files, bytes, nil
}

// applyThresholds overlays parsed values onto the defaults.
func applyThresholds(vals map[string]int, files, bytes *int) {
	if v, ok := vals[keyFiles]; ok {
		*files = v
	}
	if v, ok := vals["bytes"]; ok {
		*bytes = v
	}
}

// parseThresholdEntry parses one threshold file line: blanks and comments
// skip, invalid lines refuse, valid lines return the parsed value.
func parseThresholdEntry(ln string, seen map[string]bool) (key string, val int, skip bool, ref error) {
	line := strings.TrimSpace(strings.ReplaceAll(ln, "\r", ""))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", 0, true, nil
	}
	k, v := splitThreshold(line)
	if r := checkThreshold(line, k, v, seen); r != nil {
		return "", 0, false, r
	}
	return k, atoi(v), false, nil
}

// checkThreshold gates one threshold entry: unknown keys, non-numeric and
// overlong values, then duplicates — in shell order.
func checkThreshold(line, key, val string, seen map[string]bool) error {
	if key == "" {
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates locked invariant 'degenerate-guard' "+
			"(unknown key in '%s' — want only 'files: N' and 'bytes: N')", Threshold, line)}
	}
	if !isDigits(val) {
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf(
			"override %s violates locked invariant 'degenerate-guard' (value '%s'"+
				" for '%s' is not a non-negative integer — thresholds must stay satisfiable)",
			Threshold, val, key)}
	}
	if len(val) > maxThresholdDigits {
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf(
			"override %s violates locked invariant 'degenerate-guard' (value"+
				" '%s' for '%s' exceeds 15 digits — thresholds must fit 64-bit arithmetic)",
			Threshold, val, key)}
	}
	if seen[key] {
		return &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates locked invariant "+
			"'degenerate-guard' (duplicate '%s' — list each key once)", Threshold, key)}
	}
	return nil
}

// splitThreshold removes all whitespace, maps `:` to `=`, and splits
// files=/bytes= keys.
func splitThreshold(line string) (key, val string) {
	flat := strings.ReplaceAll(removeSpaces(line), ":", "=")
	if rest, ok := strings.CutPrefix(flat, "files="); ok {
		return keyFiles, rest
	}
	if rest, ok := strings.CutPrefix(flat, "bytes="); ok {
		return "bytes", rest
	}
	return "", ""
}

// removeSpaces strips ASCII whitespace, like the shell's tr -d '[:space:]'.
func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return -1
		default:
			return r
		}
	}, s)
}

// isDigits reports whether s is all ASCII digits (empty reads false — the
// shell's `”|*[!0-9]*` case matches empty too).
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// atoi parses a validated digit string.
func atoi(s string) int {
	n := 0
	for i := range len(s) {
		n = n*decimalBase + int(s[i]-'0')
	}
	return n
}

// AttributionText ports override_attribution_text: the raw fragment, which
// must fit on one line. The shell's command substitution strips trailing
// newlines before the line count, so interior newlines refuse but a single
// trailing newline reads as the one line.
func AttributionText() (string, error) {
	raw, err := os.ReadFile(Attribution)
	if err != nil {
		//nolint:nilerr // an absent override file means "no override", not a failure
		return "", nil
	}
	text := strings.TrimRight(strings.ReplaceAll(string(raw), "\r", ""), newline)
	if strings.Count(text, newline) != 0 {
		return "", &cli.RefusalError{Code: codeBadOverride, Message: fmt.Sprintf("override %s violates "+
			"'attribution-single-line' (want one sentence on one line)", Attribution)}
	}
	return text, nil
}

// Substitute ports substitute_override_placeholders: v1 inventory replaced
// with bundle values. version/tier/cost/files/head/model come from the
// provided bundle-value callbacks so this package stays I/O-free.
func Substitute(text string, vals *Values) string {
	text = strings.ReplaceAll(text, "{attribution}", vals.BaseAttribution)
	text = strings.ReplaceAll(text, "{version}", vals.Version)
	text = strings.ReplaceAll(text, "{tier}", vals.Tier)
	text = strings.ReplaceAll(text, "{product_url}", vals.ProductURL)
	text = strings.ReplaceAll(text, "{cost}", vals.Cost)
	text = strings.ReplaceAll(text, "{files_reviewed}", vals.FilesReviewed)
	text = strings.ReplaceAll(text, "{head_sha_short}", vals.HeadShort)
	text = strings.ReplaceAll(text, "{model}", vals.Model)
	return text
}

// Values carries the bundle values for placeholder substitution.
type Values struct {
	BaseAttribution string
	Version         string
	Tier            string
	ProductURL      string
	Cost            string
	FilesReviewed   string
	HeadShort       string
	Model           string
}
