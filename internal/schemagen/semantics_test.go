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
	// additive author.schema.json (new bundle document; the advisory author
	// fingerprint fetch detects). No existing schema property, required key,
	// enum, or type changed.
	"author.schema.json":      "f7d48bf8cc56baf70899b6e02caceae111838bb7ee3ed707eb7e4a9e7b809a70",
	"excluded.schema.json":    "da38e86c8f27bc41b8255f7fc12cbf0a81fa181eee2be172c46b4ae015b041ac",
	"panel-plan.schema.json":  "c4ac13bb03fb5918bc5b60ff9eba10776f10b999fdc88049409792439b1c043a",
	"panel.schema.json":       "60fee20b395837e41c0bb8044dcaed55fa7a38d8d8a504a9f228be95b28319ef",
	"calibration.schema.json": "9e061b60ab7c2252b86146af16742a3e59c64a28e9f6441fd0f30f1f5e2f9581",
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
	// kb_policy.max_chars: additive optional non-null integer >= 1 controlling KB
	// recall character budget.
	"config.schema.json": "e7bca2256eaa3417f675cd3428f9a9bcfc84ccefc7bf23ed68ff2102ab58a716",
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
	// additive corpus.schema.json: provenance gains base_sha and head_sha, the
	// commits a real case's diff spans and its bundle stamps. Two new optional
	// string properties; no existing property, required key, or type changed.
	"corpus.schema.json": "e1206318dc186aa479ae702384edba263ce229f921e68858e142efb16b4e5785",
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
	// pack lock/add: additive $defs/packAddData and the drift description on
	// packEntry; no existing property, required key, enum, or type changed.
	// pack test: additive $defs/packTestData, packTestCase, and
	// packTestSummary, plus the generated envelope description. Existing shapes
	// remain unchanged.
	// summary comment: additive nullable postData.summary and $defs/postSummary
	// (optional; no existing property, required key, enum, or type changed).
	// risk score: additive optional risk-stale-head problem code,
	// postData/recordData.risk ($defs/riskClaim), reportData.risk ($defs/reportRisk +
	// $defs/riskInputs), gateDimension.id enum value "risk", and trendData.by_risk /
	// trendBundleRow.risk. No existing property, required key, or type changed.
	// sarif ingest: additive $defs/ingestData, $defs/trendSources, and
	// optional by_source on reportCounts, trendData, and trendBundleRow. No
	// existing property, required key, enum, or type changed.
	// incident memory: additive $defs/learnIncident, $defs/trendIncidents, and
	// $defs/trendIncidentBundle plus the nullable optional learnData.incident
	// and trendData.incidents keys, and the regenerated learnData/trendData
	// descriptions. No existing property, required key, enum, or type changed.
	// provenance receipt (--provenance): additive $defs/provenanceReceipt,
	// $defs/provenanceInput, $defs/provenanceTruncatedRow, and additive array
	// widening on envelope data.truncated (boolean|provenanceTruncatedRow[]).
	// receipt declaration: additive data.receipt ($ref provenanceReceipt) and
	// additive optional reportData/postData receipt and truncated members
	// ($ref provenanceReceipt, boolean|provenanceTruncatedRow[]). No existing
	// property, required key, enum, or type changed.
	// trend provenance: additive optional trendBundleRow.run_id and
	// truncated_inputs (the row count the bundle's receipt reported cut, not a
	// count of inputs). No existing property, required key, or type changed.
	// triage score: additive optional reportData.risk.score ($defs/riskScore
	// with $defs/riskReach, riskChurn, riskCategory, and riskRung). No existing
	// property, required key, enum, or type changed.
	// triage queue: additive $defs/riskData, $defs/riskBands, and $defs/riskRow
	// pinning the risk verb's data profile (the ranked queue), carrying the same
	// optional flat receipt keys (truncated, truncation_reason, returned, total,
	// next_offset) trendData and indexData publish. Additions only; no existing
	// property, required key, enum, or type changed.
	// pr-wait inconsistent reads: additive required
	// $defs/prWaitData.inconsistent_observations, plus the regenerated
	// observations and prWaitData property descriptions. No existing property,
	// required key, enum, or type changed.
	// fix batch: additive optional fixData.min_severity, fixData.verify_cmd,
	// fixData.patch (the fix.patch path), and fixData.paths (the --paths globs),
	// plus fixData.batch ($ref fixBatchRow) and $defs/fixBatchRow. No existing
	// property, required key, enum, or type changed.
	// author/reviewer pair: additive optional trendData.by_pair
	// ($defs/trendPairCounts) plus required trendBundleRow.author and
	// trendBundleRow.review_models. No existing property, enum, or type changed.
	// handoff projection: additive $defs/handoffData and $defs/handoffRow
	// pinning the handoff verb's data profile (open findings with their brief
	// text and verify command), plus the regenerated envelope description
	// naming handoffData. No existing property, required key, enum, or type
	// changed.
	// The validation pass adds data.validation (optional, post only), and
	// validationData joins the envelope description's published data-profile
	// list beside postData. No existing property, required key, enum, or type
	// changed.
	// Reaction-scoped fix: additive optional fixData.selection (the
	// fetch-written selected.json a --selected batch reads). No existing
	// property, required key, enum, or type changed.
	"envelope.schema.json": "27103488dcbcb9966b36d5f4f78241832b63f304d4710cb7d745afbbc20cc255",
	// Repair brief: additive $defs/brief + $defs/briefSite and a nullable
	// entry.brief (optional; no existing property, required key, or type
	// changed).
	// rigor provenance: additive optional entry.rigor_source enum. No existing
	// property, required key, or type changed.
	// finding categories: additive optional entry.category enum (absent and
	// null group as untagged; never part of the sig). No existing property,
	// required key, or type changed.
	// sarif ingest: additive optional entry.source enum (model|tool) and
	// optional tool, rule_id, and fingerprint strings on findings entries. No
	// existing property, required key, or type changed.
	"findings.schema.json": "273c22cb1804ca71acc28ca2f7912d8ec60f9efeb5c3772feb8b33286f107207",
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
	// summary comment: additive optional nullable summary object with
	// $defs/postSummary recording create/update/none outcome for --summary
	// comment. No existing property, required key, enum, or type changed.
	// risk score: additive optional nullable risk claim ($defs/riskClaim). No
	// existing property, required key, enum, or type changed.
	"post.schema.json": "0e6fcf560007fd66da8605a73e5a2df912d5334d8b999341e850a7b9c1a1e0f2",
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
	"pack.schema.json": "be5fbfe989cf2dbae0b4a3ba67be5e8512e3784db734896b7f142d7df579ae73",
	// additive recipes.schema.json (new control document: the declared review
	// pipelines a run resolves by name at the PR base). No existing schema
	// property, required key, enum, or type changed.
	// key agreement: the recipes map now publishes propertyNames with the
	// loader's own name grammar (a lowercase slug of 1 to 64 characters), so a
	// third-party validator refuses the keys the parser refuses. The lens
	// model-hint description now names the characters its pattern admits
	// (._:/@+~-) instead of a pipe the pattern never carried. Additive keyword
	// and prose only; no existing property, required key, enum, or type
	// changed.
	// bounds: version maxLength 64, lenses maxItems 64, and the lens id pattern
	// bounded at 64 characters, the same three caps the parser now refuses
	// past, so one declared recipe can no longer grow the directive and argv
	// without limit. Constraints on existing properties; no property, required
	// key, enum, or type changed.
	// published plan: additive $defs/plan, $defs/stage, and $defs/lensUse — the
	// shape of data.recipe and launcher.json's recipe key, which had none. No
	// existing property, required key, enum, or type changed.
	// stage count: $defs/plan.stages now publishes minItems 4 and maxItems 4 —
	// the four stages a run always compiles, in pipeline order, the count the
	// directive and data.recipe always carry. A constraint on an additive
	// property only: no existing property, required key, enum, or type changed.
	// stage render: additive $defs/stageRender — the data.stage object a
	// run --print --stage render fills, positioned into the plan's stages. A
	// new definition only: no existing property, required key, enum, or type
	// changed.
	"recipes.schema.json": "4ccac4c5b2e8b92fdab6778870dcdfb1578b2fcaf83b86373bb9c43dfc6c7c4a",
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
	// risk score: additive optional nullable risk claim ($defs/riskClaim). No
	// existing property, required key, enum, or type changed.
	// author/reviewer pair: additive required-nullable author object
	// ($defs/author) and required review_models string array. No existing
	// property, enum, or type changed; older stores simply carry neither key.
	// The validation pass adds required-nullable validation_hash and required
	// validation_counts/validation_mode/rigor_floors after chunk_coverage.
	"record.schema.json": "37628cfde409c8b33bf7a9a23dba5f2875a4d43a63e0589b2134323385edd384",

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
	// risk score: additive optional nullable risk claim ($defs/riskClaim). No
	// existing property, required key, enum, or type changed.
	"review.schema.json": "58b8cafc67abfc6fe394f0cd7dff77182def6cd6652851e037ded38dc57defbe",
	// additive selected.schema.json (new bundle document: the reaction-scoped
	// fix selection fetch writes and fix --selected reads). A new document
	// only: no existing schema property, required key, enum, or type changed.
	"selected.schema.json": "40bcc26a3e73ea2a17ad02cbba06548215f9ee5b1aad04d16885275842e88021",
	// additive threads.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). The PR-conversation
	// contract extends the first cut with the thread's host id, PR/head/time
	// envelope, root author kind, finding_sig, and the comments[] array: all
	// additions to the new document, no previously published shape changed.
	"threads.schema.json": "e24857002ee5c60fb38a3a2a0867eb453526dd35c075b4b4adde33f41ebe69dc",
	// additive threads-resolve.schema.json (new bundle document; no existing
	// schema property, required key, or type changed). The receipt proves the
	// resolve mutation: provider, repo, pr, head, timestamp, dry_run, the
	// closed ids, and the skipped candidates with their reasons.
	"threads-resolve.schema.json": "e550441f9990bf2109f4f078b42e4a86728582e45f661745e725c8d06012c1d4",
	// additive usage.schema.json (new handoff document; no existing schema
	// property, required key, or type changed — the envelope fetchEstimate entry
	// from f3a22047 is untouched).
	"usage.schema.json": "143473d20d0e0d140432356d9254554b969b9c71290d61ec3f5ab821e2b6bc69",
	// additive validation.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). It pins the trusted pass: one
	// result per finding sig with its verdict, reason, optional one-rank
	// downgrade pair, and checks.
	"validation.schema.json": "c018104ce060fdaa7d45b5cc5a02d3b0046a4d5c5c121b7d5b64f8eb02bd58f0",
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
