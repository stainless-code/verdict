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
	// additive backtest.schema.json (new run-artifact document for the backtest
	// score report; no existing schema property, required key, or type changed).
	// The catch_rate description now calls the rate a match rate over
	// blame-derived candidates instead of a lower bound on catch.
	"backtest.schema.json": "23ea7e2b720c097bf41cdbebf9a4d828ec69d738c916deb475df1c1bc8216b89",
	// additive config.schema.json (new typed-repository-configuration
	// document; no existing schema property, required key, or type
	// changed). The scope $def now carries the presence contract the
	// parser enforces: at least one of profile, defaults, or requirements
	// (anyOf), never profile with inline defaults, and never a scope's own
	// defaults.kb_policy — the knowledge-base policy is global-only, which
	// the parser refuses with bad-scope.
	// additive index caps: an optional top-level index object and its
	// $defs/configIndex shape. Additions only; no existing property, required
	// key, enum, or type changed. The configIndex description now calls the
	// index a code index rather than a Go one.
	"config.schema.json": "db98cce6cb648527523180d9011e324b8907d27fbf6880ab5c649a517e21d59d",
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
	// Cost model names: prose only. The description now states that every
	// models[] entry must name its model and that the name feeds attribution
	// while the rollup sums one total. The models array accepts one shape
	// only; the reader dropped its object-form coercion in the same change.
	// No property, required key, enum, or type changed.
	"cost.schema.json": "c6e712e91b587b6f0d5c5f74548f46f37f0ed996a8530f637e985576163507d6",
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
	// honest capabilities: fetchData/fetchLocalData.capabilities stops being an
	// untyped placeholder and becomes $defs/fetchCapabilities — the booleans,
	// the derived review_verdicts sum, and the unsupported reason map (whose
	// values are typed strings). Only provider and fetch are required, because
	// those are the only keys every previously written envelope carried; the
	// split keys stay optional so legacy envelopes keep validating, while new
	// writers always emit all of them. No other property, enum, or type changed.
	// gate coverage dimension: additive `coverage` member on gateDimension.id
	// (the gate's stored diff-coverage verdict), with the id/count descriptions
	// and the reportGate order note updated to name it. No existing member,
	// property, required key, or type changed; a consumer that switches
	// exhaustively on the dimension id gets the unknown-member case it would
	// already need for a future dimension.
	// verify sandbox: additive optional verifyData.sandboxed boolean (absent
	// means the run was not sandboxed). No existing property, required key,
	// enum, or type changed.
	// chunked-bundle coverage: the coverage dimension's id and count
	// descriptions now say a chunked bundle is graded on the chunk
	// attestation its receipt stores, and counts unattested chunks. No
	// member, property, required key, enum, or type changed.
	// thread resolve: additive $defs/threadsResolveData and
	// $defs/threadsResolveSkip entries pinning the resolve half of the
	// threads --json data profile. Additions only; no existing property,
	// required key, enum, or type changed.
	// repair-exhausted problems: the error.problems description now covers
	// repair-exhausted (errors first) alongside bad-review-input. Description
	// only; no property, required key, enum, or type changed.
	// index: additive $defs/indexData and its sub-shapes. Additions only; no
	// existing property, required key, enum, or type changed.
	// index graph: additive kind enum values (tests-for, co-changed, owners),
	// optional query.path, window, target, and the row/counts fields those
	// kinds publish. One data object serves every kind, so the caller row's
	// line/kind/resolution and the caller counts' callers/unresolved relax to
	// optional and the schema widens rather than narrowing: an older consumer
	// validating a caller envelope still passes.
	// index data honesty: the top-level data.path property goes away, because
	// no wire branch emitted it — tests-for, co-changed, and owners publish
	// query.path and owners publishes tree/commit — and a typed consumer must
	// not see a field the wire cannot produce. The indexStatus and indexCounts
	// descriptions now name every kind that publishes them. One property
	// removed, prose only elsewhere.
	// indexTarget prose: the description now names all three owner shapes —
	// no CODEOWNERS file (source null too), no matching rule (source named),
	// and an ownerless rule that cleared ownership ([] with the source
	// named). Description only; no property, required key, enum, or type
	// changed. Pack list: additive $defs/packData and $defs/packEntry; the
	// envelope data object was already open, so no existing shape moved.
	// blast radius: additive indexData properties input, symbols, callers,
	// tests, co_changed, and owners with their $defs entries, an additive
	// blast-radius kind enum value, and one optional description on the shared
	// index property. count/unresolved/truncated are nullable integers on the
	// new list objects, so an absent index reads null instead of 0; no existing
	// property, required key, or type changed.
	// co-change units: additive indexBlastPartners.partners integer (the
	// distinct partner paths) beside the existing row count, because count is
	// a (target, partner) pair count; one description now names both units.
	// Additions only; no existing property, required key, enum, or type
	// changed.
	// scanned languages: additive $defs/indexLangRow and the build branch's
	// optional languages[] (required language enum, required file count), plus
	// the caller row's optional language enum. No existing property, required
	// key, enum, or type changed.
	// transitive tests: additive indexRow.via enum value "transitive-import",
	// additive optional indexRow.depth and indexData.depth/depth_truncated
	// integers. No existing property, required key, or type changed; the
	// depth_truncated name keeps the row limit's `truncated` distinct.
	// blast-radius depth cap: the same integer added to indexBlastTests, whose
	// count/truncated/rows are already all required, so it is required there
	// too and reads null when no index answered. No existing property, required
	// key, enum, or type changed.
	// serve: additive $defs/serveData (and its source/error/root/counts/row/
	// artifact/cache/skipped/discovery defs plus threadsFile/thread/comment).
	// Additive skipped.gitignore_patterns counts unsupported ignore patterns;
	// the envelope's data slot stays free-form per verb.
	// pack lock/add: additive $defs/packAddData and the drift description on
	// packEntry; no existing property, required key, enum, or type changed.
	"envelope.schema.json": "1ad42f01e23aa377e91011a56b6d95b4745f45ee25593d8de31939b2894bff64",
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
	// withheld capability: additive optional withheld[] with its
	// $defs/withheldCapability entry (required capability and reason, optional
	// requested_event), so a degraded post stays legible in the receipt after
	// the envelope is gone. No existing property, required key, enum, or type
	// changed.
	// secret-shaped values: additive optional secret_shapes_allowed string
	// array naming the value shapes --allow-secret-values published, so a
	// receipt proves the override and what it let through. No existing
	// property, required key, enum, or type changed.
	// chunked-bundle coverage: additive optional chunk_coverage object with
	// $defs/chunkCoverage (required attested and total), the chunked
	// bundle's stored chunk[] attestation. No existing property, required
	// key, enum, or type changed.
	"post.schema.json": "9427aab9df7a862b173089506d63fdf86bf2929fd16cbb507c81d8939ebc10c1",
	// patch identity: additive $defs/identity root (new bundle document) —
	// no existing property, required key, or type changed.
	"identity.schema.json": "efe9a7b331e41715bc55b8824e95154add584c2d63f670811ef03c350d2e5bdb",
	// additive pack.schema.json (new document; no existing schema property,
	// required key, or type changed). The lens-pack manifest fetch resolves
	// from skills/verdict/packs/builtin: a closed object plus one closed lens
	// row (id, optional globs, and the dispatch caps).
	// lens count prose: the description now names the loader as the enforcer
	// of the 64-lens cap (pack-too-large) instead of promising a maxItems the
	// published keyword subset does not implement. Prose only; no property,
	// required key, enum, or type changed.
	// additive index.schema.json (new local-tree document; no existing
	// schema property, required key, or type changed).
	// scanned languages: additive optional file.language enum, so a stored tree
	// names the scanner that read each file. No existing property, required
	// key, enum, or type changed.
	"index.schema.json": "2104f9b00c2909f29e5cbd155931ba4765f4b6647f8bd602199a8761c57b9126",
	// rules and layering: additive replace[], severity map, rules[] with a
	// closed $defs/rule. No existing property, required key, enum, or type
	// changed; the loader's exact-key, regular-file, and bounded-read rules
	// are prose only. The rule id carries the KB-id pattern the loader
	// enforces.
	// refused manifests: minItems 1 on rule globs, minLength 1 on a rule
	// severity key, and the three-value enum on the severity map's values, so
	// the published schema rejects the empty shapes the loader already
	// refuses instead of accepting a manifest that cannot load.
	// empty replace: the replace array no longer carries minItems 1, because
	// the loader accepts an empty list and the schema must not refuse a
	// manifest that loads.
	// lock: the new verdict.lock document joins the published set; no existing
	// schema moved. The envelope gains an additive $defs/packAddData and a
	// drift description on packEntry, with no existing property, required key,
	// enum, or type changed.
	// source grammar: the lock row's source gains the pattern the loader
	// enforces — a dir: payload with no control character, backslash, or
	// leading "-", or a git: source pinned to a 40-hex sha with no http/https
	// userinfo and only a bare ssh/scp user. The hash description now names
	// the length-delimited tree framing. No property, required key, or type
	// changed.
	// source allowlist: the git: pattern narrows to the three accepted remote
	// forms — https://host/path with no userinfo, ssh://user@host/path, and
	// user@host:path — with a DNS/IPv4/bracketed-IPv6 host, the bare
	// [A-Za-z0-9._-]{1,64} ssh user, no whitespace/control/@ in the path, and
	// the lower-case scheme pack add stores. A row the old pattern accepted
	// (any userinfo shape, http, a one-slash scheme) now refuses, so this is
	// an intentional narrowing of one pattern; no property, required key, or
	// type changed.
	// source query: a ? or # in a git source refuses before any git call (a
	// source is recorded in the committed lock), so the pattern's git paths
	// exclude both and the description names them. One pattern narrowed
	// further; nothing else changed.
	// source agreement: the git: pattern bounds each DNS label at 63 bytes
	// (the maxHostLabel cap the loader enforces) and refuses a "://" inside an
	// scp path (the loader's scheme guard). One pattern narrowed to the
	// loader's own grammar; no property, required key, enum, or type changed.
	"lock.schema.json": "c88951e4ac18781f93881f6654493ad8a7345ea990fca44bbd1149c7eefc4714",
	"pack.schema.json": "fc8dc3ae76bfee94157f23e03ad2173f643d96974896c05307f89491f2a207b9",
	// patch identity: additive optional patch_id/patch_id_kind plus the
	// optional forced[] audit with $defs/forcedPost. No existing property,
	// required key, or type changed.
	// review normalization: additive optional repairs[] plus $defs/repair. No
	// existing property, required key, or type changed.
	// patch identity: additive optional patch_id/patch_id_kind. No existing
	// property, required key, or type changed.
	// chunked-bundle coverage: additive optional chunk_coverage object with
	// $defs/chunkCoverage (required attested and total), the chunked
	// bundle's stored chunk[] attestation. No existing property, required
	// key, enum, or type changed.
	"record.schema.json": "467a9c5b0d2b6040d301e99dddb10d84f3d3bc8c68dc0d01f3c6fc9d786751e6",
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
	"review.schema.json": "25d13e382b0aac5cfb146909f388603895506aa4491dbc39b22608621d715a6f",
	// additive threads.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). The PR-conversation
	// contract extends the first cut with the thread's host id, PR/head/time
	// envelope, root author kind, finding_sig, and the comments[] array: all
	// additions to the new document, no previously published shape changed.
	"threads.schema.json": "0dfe2a58cc8e51beede189ccc676fee3446e847336e2b72270539eb4c4eed3bc",
	// additive threads-resolve.schema.json (new bundle document; no existing
	// schema property, required key, or type changed). The receipt proves the
	// resolve mutation: provider, repo, pr, head, timestamp, dry_run, the
	// closed ids, and the skipped candidates with their reasons.
	"threads-resolve.schema.json": "e550441f9990bf2109f4f078b42e4a86728582e45f661745e725c8d06012c1d4",
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
