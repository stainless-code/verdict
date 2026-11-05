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
	"strings"

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
	patchHexLen   = 40
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

// Build computes the bundle identity over the raw diff bytes: git patch-id
// --stable through the injected commander, the index-stripped fallback when
// git is missing or fails. An empty diff carries a null patch id of kind
// none, which no guard matches. It never fails: the fallback always hashes.
func Build(ctx context.Context, cmdr core.Commander, diff []byte, head, base string) *Identity {
	sum := hashHex(diff)
	id := &Identity{SchemaVersion: schemaVersion, DiffSHA256: sum, RawDiffSHA256: sum, HeadSHA: head, BaseSHA: base}
	if len(bytes.TrimSpace(diff)) == 0 {
		id.PatchIDKind = KindNone
		return id
	}
	if patch, ok := gitPatchID(ctx, cmdr, diff); ok {
		id.PatchID, id.PatchIDKind = &patch, KindGit
		return id
	}
	fallback := fallbackHex(diff)
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

// Load reads the bundle identity: ok is false when the file is missing,
// unreadable, over cap, undecodable, or malformed (a non-object, a bad kind,
// or a non-string patch id). The caller warns and proceeds on false.
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
	kind, ok := kindOf(m)
	if !ok {
		return nil, false
	}
	patch, ok := patchOf(m)
	if !ok {
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
func patchOf(m map[string]any) (*string, bool) {
	raw, present := m["patch_id"]
	if !present || raw == nil {
		return nil, true
	}
	patch, ok := raw.(string)
	if !ok {
		return nil, false
	}
	return &patch, true
}

// Match reports whether two identities name the same reviewed change: both
// carry a non-empty patch id of the same non-none kind. It never compares
// across kinds, and the empty identity never matches.
func Match(a, b *Identity) bool {
	if a == nil || b == nil || a.PatchID == nil || b.PatchID == nil {
		return false
	}
	if *a.PatchID == "" || *b.PatchID == "" {
		return false
	}
	if a.PatchIDKind == KindNone || b.PatchIDKind == KindNone {
		return false
	}
	return a.PatchIDKind == b.PatchIDKind && *a.PatchID == *b.PatchID
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
