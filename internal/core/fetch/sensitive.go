package fetchcore

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// sensNameSet lists the exact secret-adjacent basenames.
var sensNameSet = map[string]bool{
	".env": true, ".envrc": true, ".netrc": true, ".npmrc": true,
	".pypirc": true, ".pgpass": true, ".htpasswd": true,
	"credentials": true, "id_rsa": true, "id_dsa": true,
	"id_ecdsa": true, "id_ed25519": true,
}

// sensEnvExempt lists the .env.* suffixes held for examples and templates.
var sensEnvExempt = map[string]bool{
	"example": true, "sample": true, "template": true,
	"dist": true, "defaults": true,
}

// sensExtSet lists the secret-adjacent extensions.
var sensExtSet = map[string]bool{
	"pem": true, "key": true, "p12": true, "pfx": true,
	"keystore": true, "jks": true, "ppk": true,
}

// sensPathReasons ports sens_path_reasons: filename/extension reasons for
// one path, in shell order (filename first).
func sensPathReasons(path string) []string {
	base := path
	if _, after, ok := strings.CutLast(path, pathSep); ok {
		base = after
	}
	if base == "" || base == devNullPath {
		return nil
	}
	lb := readcommon.ASCIILower(base)
	return append(sensNameReason(lb), sensExtReason(lb)...)
}

// sensNameReason ports the basename rule: an exact secret-adjacent name or a
// non-exempt .env.* suffix.
func sensNameReason(lb string) []string {
	if sensNameSet[lb] {
		return []string{keyFilename}
	}
	if strings.HasPrefix(lb, ".env.") && !sensEnvExempt[lb[len(".env."):]] {
		return []string{keyFilename}
	}
	return nil
}

// sensExtReason ports the extension rule: the last dot's suffix.
func sensExtReason(lb string) []string {
	if !strings.Contains(lb, ".") {
		return nil
	}
	if sensExtSet[lb[strings.LastIndex(lb, ".")+1:]] {
		return []string{"extension"}
	}
	return nil
}

// sensContentRe ports sens_content_hit: added-line secret shapes.
var sensContentRe = regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----` +
	`|(AKIA|ASIA)[A-Za-z0-9]{16}` +
	`|gh[pousr]_[A-Za-z0-9]{36,}` +
	`|xox[baprs]-[A-Za-z0-9-]{10,}` +
	`|AIza[A-Za-z0-9_-]{35}` +
	`|(sk|rk)_(live|test)_[A-Za-z0-9]{16,}` +
	`|sk-ant-[A-Za-z0-9_-]{20,}` +
	`|sk-(proj|svcacct|admin)-[A-Za-z0-9]{20,}` +
	`|sk-[A-Za-z0-9]{40}`)

// sensHit is one path/reason hit.
type sensHit struct {
	path   string
	reason string
}

// sensEmitSection ports sens_emit_section: filename/extension reasons for
// the section's old/new paths plus the content reason on the new path when
// an added line carries a secret shape.
func sensEmitSection(lines []string, first, last int) []sensHit {
	var out []sensHit
	if first < 1 || first > len(lines) {
		return nil
	}
	oldP, newP := parseDiffHeader(lines[first-1])
	for _, p := range []string{oldP, newP} {
		out = append(out, sensPathHits(p)...)
	}
	if last >= first {
		if hit, ok := sensContentHit(lines, first, last, newP); ok {
			out = append(out, hit)
		}
	}
	return out
}

// sensPathHits ports the header-path scan: every reason for each usable
// path (empty and /dev/null headers never flag).
func sensPathHits(p string) []sensHit {
	var out []sensHit
	if p == "" || p == devNullPath {
		return nil
	}
	for _, r := range sensPathReasons(p) {
		out = append(out, sensHit{path: p, reason: r})
	}
	return out
}

// sensContentHit ports the added-line scan: added lines joined and
// matched once, flagging the new path on a content hit.
func sensContentHit(lines []string, first, last int, newP string) (sensHit, bool) {
	hi := min(last, len(lines))
	var added []string
	for _, l := range lines[first:hi] {
		if strings.HasPrefix(l, "+") {
			added = append(added, l)
		}
	}
	if len(added) > 0 && sensContentRe.MatchString(strings.Join(added, newline)) {
		return sensHit{path: newP, reason: "content"}, true
	}
	return sensHit{}, false
}

// sensExtraHits ports the extra-paths scan: every reason for each
// non-empty extra path.
func sensExtraHits(extra string) []sensHit {
	var hits []sensHit
	if extra == "" {
		return nil
	}
	for p := range strings.SplitSeq(extra, newline) {
		hits = append(hits, sensPathHits(p)...)
	}
	return hits
}

// sensSectionStarts ports the section scan: 1-based header line numbers.
func sensSectionStarts(lines []string) []int {
	var starts []int
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// parseDiffHeader delegates the header parse to the shared quote/space-safe
// parser used by internal/hunks, so the sensitive manifest and the anchor
// index agree on quoted, spaced, and renamed paths.
func parseDiffHeader(hline string) (oldPath, newPath string) {
	return hunks.HeaderPaths(hline)
}

// writeSensitiveManifest ports write_sensitive_manifest: the advisory
// flagged-path list with a stderr warning naming the first few. No hits:
// no file, no warning.
func writeSensitiveManifest(r *session, dir, extra string) *core.RefusalError {
	// A missing or empty diff carries no sensitive paths; the manifest
	// from a previous run (if any) is stale and goes.
	raw := readOptional(dir + suffixFullDiff)
	if raw == "" {
		_ = os.Remove(dir + "/sensitive.txt")
		return nil
	}
	flagged := dedupeHits(sensAllHits(raw, extra))
	if len(flagged) == 0 {
		_ = os.Remove(dir + "/sensitive.txt")
		return nil
	}
	var sb strings.Builder
	sb.WriteString("# sensitive-path manifest — advisory only, not a gate.\n")
	sb.WriteString("# Flagged paths stay reviewable evidence; never quote flagged values\n")
	sb.WriteString("# into posted review bodies — name the path, not the secret.\n")
	for _, f := range flagged {
		sb.WriteString(f + newline)
	}
	if werr := atomic.WriteFile(dir+"/sensitive.txt", []byte(sb.String())); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/sensitive.txt: %v", dir, werr)}
	}
	warnSensitive(r, dir, flagged)
	return nil
}

// sensAllHits ports the section walk: extra paths first, then each section's
// path and content reasons, with the trailing section ending at EOF.
func sensAllHits(raw, extra string) []sensHit {
	lines := splitLines(raw)
	hits := sensExtraHits(extra)
	var prev int
	for _, start := range sensSectionStarts(lines) {
		if prev != 0 && start > prev {
			hits = append(hits, sensEmitSection(lines, prev, start-1)...)
		}
		prev = start
	}
	if prev != 0 {
		hits = append(hits, sensEmitSection(lines, prev, countNewlines(raw))...)
	}
	return hits
}

// warnSensitive ports the capped advisory line naming the flagged paths.
func warnSensitive(r *session, dir string, flagged []string) {
	shown := flagged
	more := ""
	if len(flagged) > numShownHits {
		shown = flagged[:numShownHits]
		more = fmt.Sprintf(", and %d more", len(flagged)-numShownHits)
	}
	r.Warn(fmt.Sprintf("sensitive paths in bundle (see %s/sensitive.txt): %s%s", dir, strings.Join(shown, ", "), more))
}

// dedupeHits ports the manifest awk: one `path (r1,r2)` line per path with
// first-seen reason order, sorted byte-wise. Lines with empty or /dev/null
// paths never occur (callers skip them); hits with empty reasons cannot
// occur either.
func dedupeHits(hits []sensHit) []string {
	reasons, order := collectHitReasons(hits)
	var out []string
	for _, p := range order {
		out = append(out, fmt.Sprintf("%s (%s)", p, strings.Join(reasons[p], ",")))
	}
	slices.Sort(out)
	return out
}

// collectHitReasons folds hits into per-path reasons in first-seen order,
// skipping empty and /dev/null paths.
func collectHitReasons(hits []sensHit) (map[string][]string, []string) {
	reasons := map[string][]string{}
	seen := map[string]map[string]bool{}
	order := []string{}
	for _, h := range hits {
		if h.path == "" || h.path == devNullPath {
			continue
		}
		if seen[h.path] == nil {
			seen[h.path] = map[string]bool{}
			order = append(order, h.path)
		}
		addHitReason(seen[h.path], &reasons, h)
	}
	return reasons, order
}

// addHitReason records a path's reason once, in first-seen order.
func addHitReason(seen map[string]bool, reasons *map[string][]string, h sensHit) {
	if seen[h.reason] {
		return
	}
	seen[h.reason] = true
	(*reasons)[h.path] = append((*reasons)[h.path], h.reason)
}
