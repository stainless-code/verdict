// Package provenance ports the bundle-provenance triple (tool version,
// prompt-set hash, cost-record model) plus the P1 footer receipt, the P1
// confidence basis line, and the P2 permalink evidence-link garnish. In the
// record verb these only surface as warnings: the body they annotate is a
// post-time artifact, so record computes the garnish, warns, and discards.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// promptFiles lists the assembled agent prompt in hash order.
var promptFiles = []string{"SKILL.md", "prompts/diagrams.md", "prompts/review.md", "prompts/system.md"}

// buildVersion is the release version injected at link time (GoReleaser
// -X main.version); empty for source builds, which read package.json.
var buildVersion string

// SetBuildVersion pins the release version for ToolVersion (and the footer
// derived from it). Call once at startup; empty keeps the source-build
// fallback. This is the test seam for the linked-version preference.
func SetBuildVersion(v string) {
	buildVersion = v
}

// ToolVersion reads the tool version beside the binary root, defaulting to
// [core.VersionUnknown] when the checkout marker is absent (the same degrade
// every version surface reports). A linked release version (installed
// binaries ship no package.json) wins over the file read.
func ToolVersion(root string) string {
	if buildVersion != "" {
		return buildVersion
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return core.VersionUnknown
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return core.VersionUnknown
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return core.VersionUnknown
	}
	v := jq.StrOf(m["version"])
	if v == "" {
		return core.VersionUnknown
	}
	return v
}

// PromptsHash hashes the prompt set behind a review: each present file feeds
// "== <path> ==\n" plus its bytes plus "\n" into sha256, in sorted path
// order. Missing files contribute nothing; the empty set hashes as the
// sha256 of nothing.
func PromptsHash(root string) string {
	h := sha256.New()
	for _, f := range promptFiles {
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "== %s ==\n", f)
		h.Write(raw)
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CostModel returns the first model of the latest cost record, or "" when
// absent or unreadable (callers store null, never invent).
func CostModel(dir string) string {
	records := costRecords(dir)
	if len(records) == 0 {
		return ""
	}
	rec, ok := latestRecord(records)
	if !ok {
		return ""
	}
	models, ok := rec["models"].([]any)
	if !ok || len(models) == 0 {
		return ""
	}
	m, ok := models[0].(map[string]any)
	if !ok {
		return ""
	}
	return jq.StrOf(m["name"])
}

// latestRecord returns the latest cost record as a map.
func latestRecord(records []any) (map[string]any, bool) {
	switch t := records[len(records)-1].(type) {
	case map[string]any:
		return t, true
	default:
		return nil, false
	}
}

// BundleProvenance emits the compact provenance triple for a bundle dir.
func BundleProvenance(root, dir string) string {
	ver, hash, model := Triple(root, dir)
	var m any
	if model != "" {
		m = model
	}
	return jq.NewObj(
		"tool_version", ver,
		"prompts_hash", hash,
		"model", m,
	).MarshalCompact()
}

// Triple returns the provenance triple as separate values for embedding in
// larger stores.
func Triple(root, dir string) (toolVersion, promptsHash, model string) {
	return ToolVersion(root), PromptsHash(root), CostModel(dir)
}

// Spend rolls the latest cost record into the footer tier and spend words,
// degrading silently (garnish, never a refusal).
func Spend(dir string) (tier, cost string) {
	tier, cost = "standard", unrecorded
	records := costRecords(dir)
	if len(records) == 0 {
		return tier, cost
	}
	tier = recordTier(records[len(records)-1], tier)
	sum, n := sumEstimates(records)
	if n == 0 {
		return tier, unrecorded
	}
	return tier, "$" + jqTostring(readcommon.Round4(sum))
}

// costRecords reads the cost log as a record list (an oversized store reads
// absent, exactly like an unreadable one).
func costRecords(dir string) []any {
	raw, err := jq.CappedFile(filepath.Join(dir, "cost.json"), jq.MaxCostBytes)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	switch t := doc.(type) {
	case []any:
		return t
	case map[string]any:
		return []any{t}
	default:
		return nil
	}
}

// recordTier reads the latest record's effort tier.
func recordTier(rec any, dflt string) string {
	m, ok := rec.(map[string]any)
	if !ok {
		return dflt
	}
	e, ok := m["effort"]
	if !ok || e == nil || e == false {
		return dflt
	}
	switch tier := readcommon.ASCIILower(jq.Str(e)); tier {
	case "cheap", "standard", "deep":
		return tier
	default:
		return dflt
	}
}

// sumEstimates sums the est_usd estimates, bailing to unrecorded on a
// non-object record (mirrors the shell's jq failure).
func sumEstimates(records []any) (float64, int) {
	sum, n := 0.0, 0
	for _, r := range records {
		rm, ok := r.(map[string]any)
		if !ok {
			return sum, 0
		}
		add, count := recordEstimate(rm)
		sum += add
		n += count
	}
	return sum, n
}

// recordEstimate reads one record's est_usd estimate.
func recordEstimate(rm map[string]any) (float64, int) {
	est, ok := rm["est_usd"]
	if !ok {
		return 0, 0
	}
	f, ok := numVal(est)
	if !ok {
		return 0, 0
	}
	return f, 1
}

// jqTostring renders a float the way jq's tostring does for ordinary
// magnitudes: integral values print without exponent below 1e21, and the
// shortest round-trip form otherwise.
func jqTostring(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < maxExactInt {
		return strconv.FormatInt(int64(f), decimalBase)
	}
	return strconv.FormatFloat(f, 'g', -1, floatBits)
}

// FileCount ports publish_file_count: "N file(s)" or "files unrecorded".
func FileCount(dir string) string {
	if n, ok := prFileCount(dir); ok {
		return fileWords(n)
	}
	if n, ok := diffFileCount(dir); ok {
		return fileWords(n)
	}
	return "files unrecorded"
}

// prFileCount reads the PR record's file list length.
func prFileCount(dir string) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, prFile))
	if err != nil {
		return 0, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return 0, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return 0, false
	}
	files, ok := m["files"].([]any)
	if !ok {
		return 0, false
	}
	return len(files), true
}

// diffFileCount counts the diff's touched files.
func diffFileCount(dir string) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "full.diff"))
	if err != nil {
		return 0, false
	}
	n := 0
	for ln := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return n, true
}

// fileWords renders the file count words.
func fileWords(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// HeadShort ports override_head_short: 7-char head, dirty, or unknown.
func HeadShort(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, prFile)); err == nil {
		if hs := prHead(dir); hs != "" {
			return readcommon.ShortSHA(hs)
		}
		return unknown
	}
	return sourceHeadShort(dir)
}

// sourceHeadShort reads the local bundle head from source.json.
func sourceHeadShort(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		return unknown
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return unknown
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return unknown
	}
	if hs := jq.StrOf(m["head_sha"]); hs != "" {
		return readcommon.ShortSHA(hs)
	}
	// The shell reads `jq -r '.dirty // false'` and compares to "true", so a
	// JSON string "true" counts as dirty just like boolean true (R() mirrors
	// the -r rendering, including its pretty containers).
	if d, ok := m["dirty"]; ok && jq.R(jq.Alt(d, false)) == "true" {
		return "dirty"
	}
	return unknown
}

// prHead reads the head SHA via the provider-aware field rules.
func prHead(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, prFile))
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	prov := jq.StrOf(m["provider"])
	if prov == "" {
		hs := jq.StrOf(m["headRefOid"])
		return hs
	}
	hs := jq.StrOf(m["head_sha"])
	return hs
}

// OverrideModel ports override_model: first cost model name, REVIEW_MODEL,
// or unrecorded.
func OverrideModel(dir, reviewModel string) string {
	if name := firstCostModel(costRecords(dir)); name != "" {
		return name
	}
	if reviewModel != "" {
		return reviewModel
	}
	return unrecorded
}

// firstCostModel reads the first cost record's first model name.
func firstCostModel(records []any) string {
	if len(records) == 0 {
		return ""
	}
	fm, ok := records[0].(map[string]any)
	if !ok {
		return ""
	}
	models, ok := fm["models"].([]any)
	if !ok || len(models) == 0 {
		return ""
	}
	mm, ok := models[0].(map[string]any)
	if !ok {
		return ""
	}
	name, ok := mm["name"]
	if !ok || name == nil || name == false {
		return ""
	}
	return jq.Str(name)
}

// ReviewLink ports publish_review_link: a factual review ref, or "".
func ReviewLink(dir string) string {
	m := postRecord(dir)
	if m == nil {
		return ""
	}
	rev, ok := m["review"].(map[string]any)
	if !ok {
		rev = nil
	}
	id, url := reviewRef(rev)
	if id == "" && url == "" {
		return ""
	}
	if id == "" {
		id = "posted"
	}
	if postedHeadMatches(dir, m) {
		return ""
	}
	if url != "" {
		return "[review `" + id + "`](" + url + ")"
	}
	return "review `" + id + "`"
}

// postRecord reads the posted-review record.
func postRecord(dir string) map[string]any {
	raw, err := os.ReadFile(filepath.Join(dir, "post.json"))
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	return m
}

// reviewRef reads the review id and URL, tolerating a missing review block.
func reviewRef(rev map[string]any) (string, string) {
	if rev == nil {
		return "", ""
	}
	var id string
	if rid, ok := rev["id"]; ok && rid != nil && rid != false {
		id = jq.Str(rid)
	}
	var url string
	if u, ok := rev["html_url"]; ok {
		url = jq.StrOf(u)
	}
	return id, url
}

// postedHeadMatches reports whether the posted head still matches the bundle.
func postedHeadMatches(dir string, m map[string]any) bool {
	posted := jq.StrOf(m["head"])
	if posted == "" {
		return false
	}
	current := bundleHead(dir)
	return current != "" && posted == current
}

// bundleHead reads the bundle's current head from the provider record or
// the local source.
func bundleHead(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, prFile)); err == nil {
		return prHead(dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	sm, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(sm["head_sha"])
}

func numVal(v any) (float64, bool) {
	switch t := v.(type) {
	case jq.Number:
		f, err := strconv.ParseFloat(t.String(), floatBits)
		if err != nil {
			return 0, false
		}
		return f, true
	case json.Number:
		f, err := strconv.ParseFloat(t.String(), floatBits)
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return t, true
	default:
		return 0, false
	}
}
