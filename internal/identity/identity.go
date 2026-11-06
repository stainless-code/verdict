// Package identity owns the bundle patch identity: the stable diff id
// every fetch writes to .review/<bundle>/identity.json, plus the match rule
// the post guard, adopt --from auto, and report --completeness share. The
// primary algorithm is `git patch-id --stable` over the raw bundle diff; the
// labeled diff-sha256 fallback covers a missing git or a failed spawn. A
// patch id is a dedupe key, never an authorization token.
package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Patch identity kinds: the git algorithm, the labeled local fallback, and
// the empty-diff marker. A guard only matches equal non-none kinds, so a
// fallback id never matches a git-patch-id value.
const (
	KindGit      = "git-patch-id"
	KindFallback = "diff-sha256"
	KindNone     = "none"
)

const (
	schemaVersion = "1"
	identityFile  = "identity.json"
	fullDiffFile  = "full.diff"
	rawDiffFile   = "full.raw.diff"
	patchHexLen   = 40
	shaHexLen     = 64
)

// Identity is schemas/identity.schema.json: the bundle's stable diff id
// beside its head/base SHAs. DiffSHA256 stays the filtered store hash;
// RawDiffSHA256 is the identity input hash (equal while no exclusion pass
// ships a separate raw diff).
type Identity struct {
	SchemaVersion string  `const:"1"                            json:"schema_version"`
	PatchID       *string `json:"patch_id"`
	PatchIDKind   string  `enum:"git-patch-id|diff-sha256|none" json:"patch_id_kind"`
	DiffSHA256    string  `json:"diff_sha256"`
	RawDiffSHA256 string  `json:"raw_diff_sha256"`
	HeadSHA       string  `json:"head_sha"`
	BaseSHA       string  `json:"base_sha"`
}

// Wire renders the bundle artifact in field order (a nil patch id reads
// null, matching the empty-diff contract).
func (id *Identity) Wire() *jq.Obj {
	return jq.NewObj(
		"schema_version", schemaVersion,
		"patch_id", core.Wire(id.PatchID),
		"patch_id_kind", id.PatchIDKind,
		"diff_sha256", id.DiffSHA256,
		"raw_diff_sha256", id.RawDiffSHA256,
		"head_sha", id.HeadSHA,
		"base_sha", id.BaseSHA,
	)
}

// Build computes the bundle identity: DiffSHA256 hashes the filtered diff,
// RawDiffSHA256 hashes the raw pre-exclusion diff, and the patch id is
// always computed over the raw bytes (`git patch-id --stable` through the
// injected commander, the index-stripped fallback when git is missing or
// fails). The two hashes stay independent fields even while no exclusion
// pass ships a separate raw diff, so a future filtered write cannot
// silently collapse them. An empty raw diff carries a null patch id of kind
// none, which no guard matches. It never fails: the fallback always hashes.
func Build(ctx context.Context, cmdr core.Commander, filtered, raw []byte, head, base string) *Identity {
	id := &Identity{
		SchemaVersion: schemaVersion,
		DiffSHA256:    hashHex(filtered), RawDiffSHA256: hashHex(raw), HeadSHA: head, BaseSHA: base,
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		id.PatchIDKind = KindNone
		return id
	}
	if patch, ok := gitPatchID(ctx, cmdr, raw); ok {
		id.PatchID, id.PatchIDKind = &patch, KindGit
		return id
	}
	fallback := fallbackHex(raw)
	id.PatchID, id.PatchIDKind = &fallback, KindFallback
	return id
}

// gitPatchID runs `git patch-id --stable` with the diff on stdin, returning
// the first output field when it parses as a patch id.
func gitPatchID(ctx context.Context, cmdr core.Commander, diff []byte) (string, bool) {
	if cmdr == nil || ctx.Err() != nil {
		return "", false
	}
	res, err := cmdr.Run(ctx, core.Command{Argv: []string{"git", "patch-id", "--stable"}, Stdin: bytes.NewReader(diff)})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	patch := ParsePatchID(res.Stdout)
	if len(patch) != patchHexLen {
		return "", false
	}
	if _, err := hex.DecodeString(patch); err != nil {
		return "", false
	}
	return patch, true
}

// ParsePatchID reads the first whitespace-delimited field of git patch-id
// output (the stable id; the second field is the commit the caller fed).
func ParsePatchID(stdout string) string {
	if fields := strings.Fields(stdout); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// ValidPatchID reports whether patch is a well-formed id for its labeled
// algorithm: 40 lowercase hex chars for git-patch-id (exactly what the
// builder accepts from `git patch-id --stable`), 64 lowercase hex chars for
// the sha256-based diff-sha256 fallback, and empty for the none marker.
// Anything else — "not-a-patch-id", a wrong-length hash, uppercase hex —
// is malformed and never matches.
func ValidPatchID(kind, patch string) bool {
	switch kind {
	case KindGit:
		return checkHexID(patch, patchHexLen)
	case KindFallback:
		return checkHexID(patch, shaHexLen)
	case KindNone:
		return patch == ""
	default:
		return false
	}
}

// checkHexID reports whether patch is exactly want lowercase hex chars: the
// decode round-trip rejects uppercase and non-hex input.
func checkHexID(patch string, want int) bool {
	if len(patch) != want {
		return false
	}
	decoded, err := hex.DecodeString(patch)
	if err != nil {
		return false
	}
	return hex.EncodeToString(decoded) == patch
}

// Load reads the bundle identity: ok is false when the file is missing,
// unreadable, over cap, undecodable, or malformed. Malformed covers a
// non-object document, a missing or unsupported schema_version or kind, a
// non-string patch id, an algorithm-invalid patch id, a null id on a
// non-none kind (or vice versa), and missing or non-string sha fields. The
// caller warns and proceeds on false.
func Load(dir string) (*Identity, bool) {
	raw, err := jq.CappedFile(dir+"/"+identityFile, jq.MaxPRBytes)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok || m == nil {
		return nil, false
	}
	if jq.StrOf(m["schema_version"]) != schemaVersion {
		return nil, false
	}
	id, ok := loadMap(m)
	if !ok {
		return nil, false
	}
	return id, true
}

// loadMap validates the decoded identity document: the required
// schema_version, a supported kind, a present patch id that is
// algorithm-valid (null exactly for the none kind), and present string sha
// fields. Anything else is malformed.
func loadMap(m map[string]any) (*Identity, bool) {
	kind, ok := kindOf(m)
	if !ok {
		return nil, false
	}
	rawPatch, present := m["patch_id"]
	if !present {
		return nil, false
	}
	patch, ok := patchOf(rawPatch)
	if !ok {
		return nil, false
	}
	if (patch == nil) != (kind == KindNone) {
		return nil, false
	}
	if patch != nil && !ValidPatchID(kind, *patch) {
		return nil, false
	}
	if !validSHAFields(m) {
		return nil, false
	}
	return &Identity{
		SchemaVersion: schemaVersion,
		PatchID:       patch,
		PatchIDKind:   kind,
		DiffSHA256:    jq.StrOf(m["diff_sha256"]),
		RawDiffSHA256: jq.StrOf(m["raw_diff_sha256"]),
		HeadSHA:       jq.StrOf(m["head_sha"]),
		BaseSHA:       jq.StrOf(m["base_sha"]),
	}, true
}

// validSHAFields reports whether the four sha fields are present strings.
// Empty values pass here (a worktree head can be empty); the byte binding
// in Verify rejects hashes that do not match the bundle.
func validSHAFields(m map[string]any) bool {
	for _, field := range []string{"diff_sha256", "raw_diff_sha256", "head_sha", "base_sha"} {
		if _, ok := m[field].(string); !ok {
			return false
		}
	}
	return true
}

// kindOf reads the labeled kind, accepting only the published values.
func kindOf(m map[string]any) (string, bool) {
	kind, ok := m["patch_id_kind"].(string)
	if !ok {
		return "", false
	}
	switch kind {
	case KindGit, KindFallback, KindNone:
		return kind, true
	default:
		return "", false
	}
}

// patchOf reads the nullable patch id: null reads nil, a string reads
// through, anything else is malformed.
func patchOf(raw any) (*string, bool) {
	if raw == nil {
		return nil, true
	}
	patch, ok := raw.(string)
	if !ok {
		return nil, false
	}
	return &patch, true
}

// Verify loads the bundle identity and binds it to the bundle's present
// bytes, the labeled algorithm recomputed over the raw input, and the
// caller's current head/base before any gate relaxation, adoption
// selection, or provenance stamp: ok is false for a missing or malformed
// identity, for bytes the identity does not describe, for an algorithm
// claim the raw input does not reproduce, for an unreadable present raw
// diff, and for a moved head/base. A stale, tampered, or unprovable
// identity must never relax a gate, so the caller warns and falls back to
// SHA behavior on false.
func Verify(ctx context.Context, cmdr core.Commander, dir, head, base string) (*Identity, bool) {
	id, ok := VerifyBytes(ctx, cmdr, dir)
	if !ok {
		return nil, false
	}
	if id.HeadSHA != head || id.BaseSHA != base {
		return nil, false
	}
	return id, true
}

// VerifyBytes loads the bundle identity, binds it to the bundle's present
// diff bytes, and recomputes the labeled algorithm over the raw input: a
// hex-valid id over the wrong bytes is not binding, so the claim must
// reproduce. full.diff always feeds the filtered hash; full.raw.diff feeds
// the raw hash when present. Only a genuinely absent raw artifact falls
// back to the filtered bytes — a present-but-unreadable raw diff
// invalidates verification instead of certifying the wrong input. A caller
// with no authoritative head — a legacy twin without a source record —
// uses this cheaper check and resolves the head separately.
func VerifyBytes(ctx context.Context, cmdr core.Commander, dir string) (*Identity, bool) {
	id, ok := Load(dir)
	if !ok {
		return nil, false
	}
	full, err := os.ReadFile(dir + "/" + fullDiffFile)
	if err != nil {
		return nil, false
	}
	raw, ok := readRawDiff(dir, full)
	if !ok {
		return nil, false
	}
	if hashHex(full) != id.DiffSHA256 {
		return nil, false
	}
	if hashHex(raw) != id.RawDiffSHA256 {
		return nil, false
	}
	if !verifyClaim(ctx, cmdr, id, raw) {
		return nil, false
	}
	return id, true
}

// readRawDiff reads the raw pre-exclusion input: full.raw.diff when the
// artifact exists, else the filtered bytes. Any read failure other than
// genuine absence invalidates the caller — falling back on a failed read
// would certify the wrong identity input.
func readRawDiff(dir string, filtered []byte) ([]byte, bool) {
	raw, err := os.ReadFile(dir + "/" + rawDiffFile)
	if err == nil {
		return raw, true
	}
	if os.IsNotExist(err) {
		return filtered, true
	}
	return nil, false
}

// verifyClaim recomputes the labeled algorithm over the raw input and
// requires the stored claim to reproduce it: a well-formed id over
// unrelated bytes proves nothing. The empty diff carries no id. The local
// fallback recomputes without a spawn. A git claim that cannot be
// recomputed — no commander, a canceled context, a failed spawn, an
// unparseable result — is not certified: the caller falls back to the
// documented legacy warning instead of trusting an arbitrary string.
func verifyClaim(ctx context.Context, cmdr core.Commander, id *Identity, raw []byte) bool {
	if id.PatchID == nil {
		return true
	}
	switch id.PatchIDKind {
	case KindFallback:
		return fallbackHex(raw) == *id.PatchID
	case KindGit:
		if ctx == nil || cmdr == nil {
			return false
		}
		actual, ok := gitPatchID(ctx, cmdr, raw)
		return ok && actual == *id.PatchID
	default:
		return false
	}
}

// CompletedPatch reads a post.json receipt's patch claim: patch and kind
// are valid only when the receipt proves a completed publication carrying
// an algorithm-valid patch id and kind. The review must carry an identifier
// and a completed state, and the head and posted_at must parse as real
// publication metadata. Incomplete, pending, dry-run, failed, empty-review,
// and invalid head/time receipts (including a bare patch_id/patch_id_kind
// stub) prove nothing, so they never match.
func CompletedPatch(post map[string]any) (patch, kind string, ok bool) {
	if !completeReceipt(post) {
		return "", "", false
	}
	patch = jq.StrOf(post["patch_id"])
	kind = jq.StrOf(post["patch_id_kind"])
	if !ValidPatchID(kind, patch) {
		return "", "", false
	}
	return patch, kind, true
}

// completeReceipt reports whether a post record proves a completed
// publication: schema version 1 with a present review object, and review
// content proving a landed publication.
func completeReceipt(post map[string]any) bool {
	if post == nil {
		return false
	}
	if jq.StrOf(post["schema_version"]) != schemaVersion {
		return false
	}
	review, ok := post["review"].(map[string]any)
	if !ok || review == nil {
		return false
	}
	return completeReview(post, review)
}

// completeReview reports whether a present review object proves a landed
// publication: an identified review in a completed state, no dry-run
// marker, and head and posted_at stamps that parse as real publication
// metadata consistent with what the producer writes.
func completeReview(post, review map[string]any) bool {
	if !validReviewID(review["id"]) {
		return false
	}
	if !completedReviewState(jq.StrOf(review["state"])) {
		return false
	}
	if dry, ok := post["dry_run"].(bool); ok && dry {
		return false
	}
	if !checkHexID(jq.StrOf(post["head"]), patchHexLen) {
		return false
	}
	if _, err := time.Parse(time.RFC3339, jq.StrOf(post["posted_at"])); err != nil {
		return false
	}
	return true
}

// validReviewID reports whether a review identifier is real: a JSON number
// as the producer decodes it (the bundle reader keeps numbers unconverted),
// or a non-empty string. Missing, null, boolean, empty, and structured ids
// prove nothing.
func validReviewID(id any) bool {
	switch v := id.(type) {
	case float64:
		return true
	case json.Number:
		return v.String() != ""
	case jq.Number:
		return string(v) != ""
	case string:
		return v != ""
	default:
		return false
	}
}

// completedReviewState reports whether a review state names a landed
// review: the completed states the producer records, matched
// case-insensitively so the recorded casing never matters. PENDING never
// completed, FAILED never landed, and a missing or unknown state proves
// nothing.
func completedReviewState(state string) bool {
	switch {
	case strings.EqualFold(state, "approved"),
		strings.EqualFold(state, "changes_requested"),
		strings.EqualFold(state, "commented"),
		strings.EqualFold(state, "dismissed"):
		return true
	default:
		return false
	}
}

// Match reports whether two identities name the same reviewed change: both
// carry an algorithm-valid, non-empty patch id of the same non-none kind.
// It never compares across kinds, and the empty identity never matches.
func Match(a, b *Identity) bool {
	if !matchable(a) || !matchable(b) {
		return false
	}
	return a.PatchIDKind == b.PatchIDKind && *a.PatchID == *b.PatchID
}

// matchable reports whether one identity can participate in a match: a
// present, non-empty, algorithm-valid patch id of a non-none kind.
func matchable(id *Identity) bool {
	return id != nil && id.PatchID != nil && *id.PatchID != "" &&
		id.PatchIDKind != KindNone && ValidPatchID(id.PatchIDKind, *id.PatchID)
}

// hashHex renders the lowercase hex SHA-256 of data.
func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fallbackHex hashes the diff with every `index ` line removed, so base
// moves that only rewrite the index line keep the fallback id.
func fallbackHex(diff []byte) string {
	var sb strings.Builder
	for line := range strings.Lines(string(diff)) {
		if !strings.HasPrefix(line, "index ") {
			sb.WriteString(line)
		}
	}
	return hashHex([]byte(sb.String()))
}
