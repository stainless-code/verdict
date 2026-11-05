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
	// additive controls.schema.json (new bundle document; no existing schema
	// property, required key, or type changed).
	// strict_input: an optional controls.json field (default off) that lets
	// record promote review-input warnings to errors. Additive only. The
	// description now names its env/file source; shape unchanged.
	"controls.schema.json": "cb9cbf497e1054727c8ac645cd9e7edae1d2f754898dae2a29702456162991c2",
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
	"corpus.schema.json": "86ad631c0e4a7b10bf2160f231adf2639ba06fa595dec47cd5dd0d954b2c6391",
	"cost.schema.json":   "81e6a11f4731b7060f8975db80b01d595a3dd2e0f06bbf6372d1501810609be2",
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
	"envelope.schema.json": "d28b52d31fbb9c85ceb6d5112ea2e198ef55aea5a9c038be60e151572ff04b45",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable
	// entry.brief (optional; no existing property, required key, or type
	// changed).
	// rigor provenance: additive optional entry.rigor_source enum. No existing
	// property, required key, or type changed.
	// finding categories: additive optional entry.category enum (absent and
	// null group as untagged; never part of the sig). No existing property,
	// required key, or type changed.
	"findings.schema.json": "aa0f9fc9500bb90f2c0d7582225633e53d81b56808f5903d96d8764f0239eced",
	"post.schema.json":     "6429347b9ff1240a736c420bba9fbdf3f0d80acfd7c9333ea5c6723bf3f77704",
	// review normalization: additive optional repairs[] plus $defs/repair. No
	// existing property, required key, or type changed.
	"record.schema.json": "59fc476e273fc69cce49a3dc22f1275c0c0d8ea58d2bc934f3b1884df3ff7044",
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
