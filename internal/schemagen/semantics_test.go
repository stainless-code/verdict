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
	"controls.schema.json": "2ddd29802d75842d77b50e6af8b7a4701cf6076d2a52ce845d6a557a60eb349c",
	// additive corpus.schema.json (new document; no existing schema property,
	// required key, or type changed).
	"corpus.schema.json": "9541380bcc9a1250f65874d6586db128a543a960e563d0205c6bc5da62448c75",
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
	"envelope.schema.json": "644fb82935827a99228817590b7e9aa0d3ce36ece6652eb7dda1a364c48d2c0b",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable
	// entry.brief (optional; no existing property, required key, or type
	// changed).
	"findings.schema.json": "6ea646f31b6deae105d29f30f0ce892691c329509923a45f96b1b0538b27b4f4",
	"post.schema.json":     "6429347b9ff1240a736c420bba9fbdf3f0d80acfd7c9333ea5c6723bf3f77704",
	"record.schema.json":   "97647229b14efa63c43aca592aa8466b246065f858b28872c6ec1198c428d7b1",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable comment.brief
	// (optional; no existing property, required key, or type changed).
	"review.schema.json": "4b7f8da519724188347957004d7c0b230ab5dba6f58d9c1191fb82a990131a7d",
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
