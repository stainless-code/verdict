package schemagen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// publishedSemantics pins the SHA-256 of each schema's canonical JSON as
// published before generation (9f20c140). The generator that replaced the
// hand-written files was a re-serialization: every key, type, required list,
// enum and description had to stay identical. An intentional published-schema
// change updates this map in its own change, with the reason recorded; a mere
// reformat of schemas/*.schema.json must not change it. The relocation of the
// schemas to skills/verdict/schemas/ (so the registry install ships only the
// skill) re-pinned all ten hashes: only each $id URL moved; no property,
// required key, enum, or type changed.
var publishedSemantics = map[string]string{
	// additive config.schema.json (new typed-repository-configuration
	// document; no existing schema property, required key, or type
	// changed). The scope $def now carries the presence contract the
	// parser enforces: at least one of profile, defaults, or requirements
	// (anyOf), never profile with inline defaults, and never a scope's own
	// defaults.kb_policy — the knowledge-base policy is global-only, which
	// the parser refuses with bad-scope.
	"config.schema.json": "d6860823b067abcc43e2cdb77dbb8243ffe232d5d51272aa674b73b63c99ae1e",
	// additive controls.schema.json (new bundle document; no existing schema
	// property, required key, or type changed).
	// strict_input: an optional controls.json field (default off) that lets
	// record promote review-input warnings to errors. Additive only. The
	// description now names its env/file source; shape unchanged.
	// typed state: the strict_input description names REVIEW_STRICT_INPUT
	// as its only source and the inventory path description names
	// .verdict/. Prose only; no property, required key, or type changed.
	"controls.schema.json": "c98231d08509db237d0622839407995e50f74428a55c1cbad5d03f0d2fc1503e",
	// additive corpus.schema.json (new document; no existing schema property,
	// required key, or type changed).
	// additive corpus bug keywords (optional string array naming the defect in
	// the review's own words) plus corrected matching prose and the default
	// line_tolerance; no existing property, required key, enum, or type changed.
	// additive corpus bug also_lines (optional integer array of alternate
	// anchors for the same defect, same window and naming rule, one match per
	// bug); no existing property, required key, or type changed.
	// also_lines items now carry minimum 1 to match the calibrate runtime,
	// which rejects nonpositive anchors as corpus-invalid; outer array
	// unchanged.
	// typed state: the manifest description names the .verdict/state run
	// bundle path. Prose only.
	"corpus.schema.json": "2439162a90d51d8ce33fe819df64dbbcfc32f7eaf45ed888326950a433310189",
	// intentional pre-v1 break: lenses items become per-group
	// {group,labels[]} entries (an empty labels list is that group's explicit
	// zero) and schema_version pins 2. No other property, required key, or
	// type changed; the group identity comes from the verified run.json plan,
	// never a second plan document.
	// Accepted cost schema 2: schema_version const 1 -> 2 and lenses[].items
	// becomes $defs/lensUsage {group, labels} (grouped labels, additional
	// properties open for the optional run/inputs_sha256 binding). Both are
	// intentional additions of the accepted record shape over v1.
	// Lens certification split by consumer: prose only. The description now
	// states that aggregation ignores unknown top-level fields while
	// certification is version-scoped, so an unregistered field refuses by
	// name. No property, required key, enum, or type changed;
	// additionalProperties stays open for the aggregators.
	"cost.schema.json": "131e9b7639b81e8b3851030c43d522ec7d08525479697f34d0d6f9c3c6a008f2",
	// additive fetchEstimate $defs entry (measured-usage basis fields).
	// The additive showData/showFix/verifyHint $defs entries (show --json
	// data.fix). Neither change touched an existing property, required key,
	// or type.
	// description prose corrected (nine keys; derived from RenderEnvelope;
	// show listed as pinned). No property, required key, or type changed.
	// additive retry_at on $defs/error (optional RFC 3339 property; no existing
	// property, required key, or type changed).
	// data-profiles: additive $defs entries pinning the data profile of every
	// verb that previously fell back to the open object (triage, ready, adopt,
	// next, fix, learn, init, doctor, check-tree, verify, threads, trend,
	// calibrate, fetch, fetch-local), plus the nested $defs they reference.
	// Additions only: data keeps additionalProperties, no existing property,
	// required key, or type changed.
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable
	// showFix.brief, with the descriptions findings and review already carry
	// (optional; no existing property, required key, or type changed).
	// gate: an optional, nullable reportData.gate property plus the additive
	// $defs/reportGate and $defs/gateDimension entries. No existing property,
	// required key, enum, or type changed.
	// review-input problems: additive $defs/problem + $defs/checkSummary,
	// optional error.problems/problems_truncated, and dry_run/check on
	// recordData. Additions only.
	// review normalization: additive repair codes on problem.code, additive
	// $defs/repair, and optional normalized/repairs on recordData. Additions
	// only; no existing property, required key, or type changed.
	// recordData.normalized now carries "type": "object" so the schema refuses
	// a string or array where the description promises the review object.
	// finding categories: additive comment/entry category enum, additive
	// reportCounts.by_category (fixed keys) and reportFilters.category (null
	// when unfiltered), additive trendData/trendBundleRow by_category, and the
	// precision prose naming by_category. No existing property, required key,
	// enum, or type changed.
	// patch identity: additive optional same_patch/reviewed_patch_id/
	// posted_patch_id on $defs/reportCompleteness. No existing property,
	// required key, or type changed.
	// typed state: the check-tree description names the ignored
	// .verdict/state paths instead of the legacy .review ones. Prose only.
	// plan identity: additive configPlanGroup id/label and configCheck head,
	// each now required because every resolved plan and appended observation
	// carries them. Pre-v1 deliberate break: an older consumer reading a plan
	// without the identity fields fails validation instead of guessing one.
	// Plan identity: verify.planGroup now requires id and label, and
	// verify.checks requires head, so a verified envelope names the group it
	// resolved rather than an anonymous profile/depth pair.
	"envelope.schema.json": "4428b98a022211d0f14cbdcddaa42ca0b662af3b711cf6c28370ec62d71ef627",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable
	// entry.brief (optional; no existing property, required key, or type
	// changed).
	// rigor provenance: additive optional entry.rigor_source enum. No existing
	// property, required key, or type changed.
	// finding categories: additive optional entry.category enum (absent and
	// null group as untagged; never part of the sig). No existing property,
	// required key, or type changed.
	"findings.schema.json": "aa0f9fc9500bb90f2c0d7582225633e53d81b56808f5903d96d8764f0239eced",
	// post_floor_scoped: additive optional per-path posting floor list with its
	// $defs/floorScope entry (required path, enum floor). No existing
	// property, required key, or type changed.
	"post.schema.json": "7a40a685ebf050858a07a6e4e319590fc49c0bad501de19c398e91a500097dd2",
	// patch identity: additive $defs/identity root (new bundle document) —
	// no existing property, required key, or type changed.
	"identity.schema.json": "efe9a7b331e41715bc55b8824e95154add584c2d63f670811ef03c350d2e5bdb",
	// patch identity: additive optional patch_id/patch_id_kind plus the
	// optional forced[] audit with $defs/forcedPost. No existing property,
	// required key, or type changed.
	// review normalization: additive optional repairs[] plus $defs/repair. No
	// existing property, required key, or type changed.
	// patch identity: additive optional patch_id/patch_id_kind. No existing
	// property, required key, or type changed.
	"record.schema.json": "1b4dd1183dafdb938513fb5765b0f0f5f64dd82c2e38083135dd25902b7a2d65",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable comment.brief
	// (optional; no existing property, required key, or type changed). The
	// descriptions now state the checker's exact error/warning split; shape
	// unchanged.
	// out-of-range: descriptions now say brief approach/question bounds refuse
	// and unset rigor records as said; the warning list no longer claims brief
	// caps warn. No property, required key, enum, or type changed.
	// finding categories: additive optional comment.category enum; the
	// checker warns (never refuses) on unknown or non-string values and the
	// normalizer lowercases case variants. No existing property, required key,
	// or type changed.
	"review.schema.json": "5da600e4619e2943b061bc35a686d1a14fdb96709ff1b05d23713018efd2ed1b",
	// additive threads.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). The PR-conversation
	// contract extends the first cut with the thread's host id, PR/head/time
	// envelope, root author kind, finding_sig, and the comments[] array: all
	// additions to the new document, no previously published shape changed.
	"threads.schema.json": "0dfe2a58cc8e51beede189ccc676fee3446e847336e2b72270539eb4c4eed3bc",
	// additive usage.schema.json (new handoff document; no existing schema
	// property, required key, or type changed — the envelope fetchEstimate entry
	// from f3a22047 is untouched).
	"usage.schema.json": "143473d20d0e0d140432356d9254554b969b9c71290d61ec3f5ab821e2b6bc69",
}

// TestGeneratedSchemasPreservePublishedSemantics proves the generated files
// parse to the exact documents the hand-written schemas published.
func TestGeneratedSchemasPreservePublishedSemantics(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != len(publishedSemantics) {
		t.Fatalf("generated %d schemas, semantic baseline has %d", len(files), len(publishedSemantics))
	}
	for _, file := range files {
		checkSemantics(t, file)
	}
}

// checkSemantics compares one generated file with its pinned document hash.
func checkSemantics(t *testing.T, file File) {
	t.Helper()
	want, ok := publishedSemantics[file.Name]
	if !ok {
		t.Errorf("%s has no semantic baseline", file.Name)
		return
	}
	got, err := semanticHash(file.Data)
	if err != nil {
		t.Fatalf("semantic hash %s: %v", file.Name, err)
	}
	if got != want {
		t.Errorf("%s changed published semantics; got %s, want %s (update the baseline only for an intentional schema change)", file.Name, got, want)
	}
}

// semanticHash re-encodes the parsed document with sorted keys and no
// insignificant whitespace, then hashes it.
func semanticHash(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
