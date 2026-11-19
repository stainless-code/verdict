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
// reformat of schemas/*.schema.json must not change it. Moving the schemas
// out of the installed skill to schemas/ re-pinned every hash: only each $id
// URL moved; no property, required key, enum, or type changed.
var publishedSemantics = map[string]string{
	// additive author.schema.json (new bundle document; the advisory author
	// fingerprint fetch detects). No existing schema property, required key,
	// enum, or type changed.
	"author.schema.json":      "0773058075a17e2d53e2d8c6a1bdfad2150bcb41a64c76e24ef4cc88cf4ef6d9",
	"excluded.schema.json":    "0d2646d108ddc5d13c2718b4e5d7f66084e5f5f2ee29883cf375d29ef8b112c4",
	"panel-plan.schema.json":  "a1030d14b80c28e98003ec8f1cbaffc75c5588f19fd2e68c38a23d0547b4bcde",
	"panel.schema.json":       "0899c9570bb2286c9375ba3af82f54caf457d2a9b95fdf2c48860cda5fa559c3",
	"calibration.schema.json": "053bb6495d947663278044fa75308c89e0b72a93b8dbc0c0e84710cf17357f85",
	// additive backtest.schema.json (new run-artifact document for the backtest
	// score report; no existing schema property, required key, or type changed).
	// The catch_rate description now calls the rate a match rate over
	// blame-derived candidates instead of a lower bound on catch.
	"backtest.schema.json": "9d61c62abb2d6cfc0310dd56bf266f1b7119d3c82a7077e034a0a712ff388d1c",
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
	// trigger: additive optional top-level predicate object with seven closed
	// optional keys; no existing property, required key, or type changed.
	"config.schema.json": "416293a853aeb457a2c20cd7b13b3fde7278f7537ce179c2c66278827159943f",
	// additive controls.schema.json (new bundle document; no existing schema
	// property, required key, or type changed).
	// strict_input: an optional controls.json field (default off) that lets
	// record promote review-input warnings to errors. Additive only. The
	// description now names its env/file source; shape unchanged.
	// typed state: the strict_input description names REVIEW_STRICT_INPUT
	// as its only source and the inventory path description names
	// .verdict/. Prose only; no property, required key, or type changed.
	"controls.schema.json": "aedf565818ff8e7b5522a55cbf7a4087929b8637574d54c8435bea8d880f596b",
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
	"corpus.schema.json": "2dadbf27e8e690bbe43ba60d0f38173aadb02773dc9c737f3e41818509c057e4",
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
	"cost.schema.json": "96a8b137f3294e790438a755331a2a7975f11cb1187352ae5151891cd38a0d17",
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
	// additive drain.schema.json (new bundle document; no existing schema
	// property, required key, enum, or type changed). It pins the plan the
	// drain verb writes: pending threads, typed skips, the plan digest, and
	// the later draft/post receipts.
	// additive drain-replies.schema.json (new bundle document; no existing
	// schema property, required key, enum, or type changed). It pins the
	// agent's single output: the plan digest and one disposition per reply.
	// additive drain-dryrun.schema.json (new bundle document; no existing
	// schema property, required key, enum, or type changed). It pins the
	// preview --post --dry-run writes: planned marked bodies and typed skips.
	"drain-dryrun.schema.json":  "1718a109cbe9f1e1f6401810ba90886655f8842194c496986d34fb6973a28032",
	"drain-replies.schema.json": "601cfd36687dab68e858aed5484fa2f654c5d2265da944fc1428753029eb8362",
	"drain.schema.json":         "1fcf8437ef91b1b130923e3600f489559be29ce9f3a5b1d2de52641db945af80",
	// findings-only review contract: additive $defs approvalReceipt, specCheck,
	// specEvidence and receipt, plus approval/spec_checks/receipts on the
	// record data. No existing envelope property, required key, enum, or type
	// changed.
	"envelope.schema.json": "b84749db19b6b7c8618e6e92ac23dfde3e027da103969e2827fb26891c37ff7b",
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
	"findings.schema.json": "1ea91e5684755f4e2045e74781e016d258b0703e689d15632c6abf48f7d20513",
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
	"post.schema.json": "3a8ec3c2c82baa3c0f31a1b80f70de2e27132e4f2472ce04bacbf8c34d262efd",
	// patch identity: additive $defs/identity root (new bundle document) —
	// no existing property, required key, or type changed.
	// fixtures.schema.json gained hallucination.unverifiable for findings whose
	// quote cannot be checked; rate stays null when checked is zero.
	"fixtures.schema.json": "55935e917580e94ec5c622cc66130d10ffa13f6ced1177da0436c5c04eb05f38",
	"identity.schema.json": "993b01b4c6891f2dc2e78454aac8bd2abaa8740c073bd5fc8124d4b47f7e68c9",
	// additive pack.schema.json (new document; no existing schema property,
	// required key, or type changed). The lens-pack manifest fetch resolves
	// from procedure/packs/builtin: a closed object plus one closed lens
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
	"index.schema.json": "6a107567d0432b17571099abf1aad24b53d80581fa3fda357eca6ad0abd49ec7",
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
	"lock.schema.json": "da19e41786cc9ff295a88a68929a8c76b5fbd44a7cbd9c5579fd57b316b16db2",
	// findings cap: the lens max_findings description now says the built-in
	// pack declares none, so a shipped lens states no return cap and returns
	// what its evidence supports. Prose only; no property, required key, enum,
	// or type changed.
	"pack.schema.json": "835b63fcb81fb9b27932f178c505d4e219504972d5bc8582ff8f94eb44085dec",
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
	"recipes.schema.json": "de52a643fb93073e167e9ee23d7bac3b113ce3e58c3e57a2378b9a850929f072",
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
	// validation_counts/validation_mode/rigor_floors after chunk_coverage. The
	// findings-only pass adds optional review_sha256 after diff_sha256: the
	// review bytes record decided on, so post can refuse a review that moved
	// since the decision. Additive only.
	"record.schema.json": "84161920cfda7c97e31612464b47382978d664dee909b9a8ebdeae5b4ca7b56a",

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
	// schema_version optional: descriptions drop the missing-key warning. No
	// property, required key, enum, or type changed.
	// field order: approvalReceipt, specCheck, specEvidence and finding keep the
	// property order the field-alignment analyzer requires of their Go structs,
	// so the same properties, types, enums, and required keys print in a
	// different order. No published value, type, or required key changed.
	// findings-only review contract: the root required keys become
	// schema_version + findings, body/approved/comments[] and the sectionTag
	// definition are removed, and the document is closed (a root key the
	// schema does not publish refuses). finding, specCheck, specEvidence and
	// receipt are new definitions. This is the deliberate pre-v1 break: there
	// is no compatibility shape.
	"review.schema.json": "ea3a754b399738e371f30be23dd2eb5099a0d06e7052eabe7c89f4dcc015f134",
	// additive selected.schema.json (new bundle document: the reaction-scoped
	// fix selection fetch writes and fix --selected reads). A new document
	// only: no existing schema property, required key, enum, or type changed.
	"selected.schema.json": "3a9edd147d5a55e19b6910a18deccb6147a8bef262bab0b109d4938746bdbfce",
	// additive threads.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). The PR-conversation
	// contract extends the first cut with the thread's host id, PR/head/time
	// envelope, root author kind, finding_sig, and the comments[] array: all
	// additions to the new document, no previously published shape changed.
	"threads.schema.json": "bf08d8d1d0d0445dab86134f7128bd990a5d399c56155b664ca01289b20212a4",
	// additive threads-resolve.schema.json (new bundle document; no existing
	// schema property, required key, or type changed). The receipt proves the
	// resolve mutation: provider, repo, pr, head, timestamp, dry_run, the
	// closed ids, and the skipped candidates with their reasons.
	"threads-resolve.schema.json": "fa2df4b546008c43a251c6eb480b2b6cf6b80d10785920286398426ad7343521",
	// additive usage.schema.json (new handoff document; no existing schema
	// property, required key, or type changed — the envelope fetchEstimate entry
	// from f3a22047 is untouched).
	"usage.schema.json": "95da162703b04193308f8c55cef95affefb4ecd278cc09e5ea3801e061be999e",
	// additive validation.schema.json (new bundle document; no existing schema
	// property, required key, or type changed). It pins the trusted pass: one
	// result per finding sig with its verdict, reason, optional one-rank
	// downgrade pair, and checks.
	"validation.schema.json": "1a86ff6e84d7c0c9ef7b524f51105b50e1154feeb22e34cace42877bb87b41bb",
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
