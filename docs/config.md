# Typed configuration — foundation slice

Status: implemented. The foundation slice (models, strict parser, pure
resolver, state layout, trusted-base source) is consumed by `fetch` and
`fetch-local` (two-phase policy, spend gates, privacy, plan surface),
`post` (compliance, bind, floor), `report` (display floor, observations),
and `verdict config` (explain/preview) with the MCP tool, the TypeScript
client, and the Go facade following the same data profile. The rule table
lives in `docs/policy-contract.md`.

## Layout

```text
.verdict/config.json            tracked, the typed configuration
.verdict/kb.jsonl               tracked, shared knowledge corpus
.verdict/learnings.md           tracked, shared heading memory
.verdict/overrides/             tracked, shared override files
.verdict/.gitignore             tracked-shape, carries /state/, /backtest/, and /index/
.verdict/state/                 ignored, local review state
.verdict/backtest/              ignored, mined backtest runs
.verdict/index/                 ignored, local code index
```

Only `/state/` is ignored, through the nested file.
`Layout.EnsureStateIgnore` writes `.verdict/.gitignore` before any state
write, and `Layout.EnsureStateDir` adds a second `*` layer at
`.verdict/state/.gitignore` so parent negations naming generated files
cannot re-admit them; reads never create files. With no base
configuration the run follows the no-init rule on personal and built-in
defaults.

Writes never follow symlinks: a symlinked `.verdict`, `.verdict/state`,
or `.verdict/.gitignore` refuses instead of landing on an unrelated
target, and resolved paths must stay under the repository root. An
existing ignore file is preserved byte-for-byte except for the appended
`/state/` line, but a negation able to match the `state` directory
itself (`!/state/`, `!state`, `!st*`, `!*`, `!**`, `!state/*`) refuses
loudly naming the line instead of being silently honored.

## Parse order

`config.Parse` refuses in one fixed order so one input produces one
diagnostic: size (over 65536 bytes refuses `config-too-large`; the D1 caps
apply first, so above 256 KiB the file refuses as `controls-too-large`
before this parser runs), JSON shape (malformed, trailing data, duplicate
keys naming the repeated key, top-level non-objects and misplaced nulls
refuse `bad-config` with the offset or path), `schema_version` (must be
the string `"1"`), credential scan (any key exactly matching the
16-name published list refuses `bad-config-credentials`; profile names
under top-level `profiles` are user-defined map keys and exempt),
unknown fixed-shape fields (refuse `bad-config` naming the path), then
semantic checks (`defaults` problems refuse `bad-config`, requirement
problems `bad-requirement`, scope problems `bad-scope`).

An explicit JSON `null` on any present key refuses exactly like a wrong
type: absent reads as missing (the key takes its default or stays
unset), present `null` refuses `bad-config` (or `bad-requirement` /
`bad-scope` inside those objects) naming the field. The published
`config.schema.json` agrees: optional keys are absent-or-typed, never
nullable, every fixed-shape object (including every `$defs` entry) is
closed, profile values validate through the `profile` definition, and
known scalar and array bounds (`budget_usd` above zero, non-empty
panels/clause lists/includes/check sets, bounded severities, efforts,
depths, categories, and checks) are encoded. Structural parity tests
assert schema and parser agree on every case.

## Schema/runtime split

JSON Schema structurally cannot express: duplicate keys (objects
collapse them before validation), cross-section references
(`default_profile` and scope `profile` naming), clause/scope name
uniqueness, the profile-plus-`defaults` exclusivity, scope input
presence (at least one of `profile`/`defaults`/`requirements`), and glob
portability — these stay runtime-specific and the parity tests pin the
split instead of weakening either side.

`$schema` is recognized metadata, never fetched; non-string refuses
`bad-config`. Personal settings (`settings.json` `review`/`spend`/`privacy`
sections) parse under the same order via `config.ParsePersonal`; a
`budget_usd` inside personal `review` refuses as misplaced.

## Resolver

`config.Resolve` is pure over explicit `config.Inputs`: flags, the five
consumed environment names and values (`ParseEnv`; never the whole
environment), the trusted-base configuration, personal
settings, the base SHA, the file list with pre-rename paths, and the
settings path when one was read. Ordinary defaults merge flag, then
environment, then repository scopes and shared defaults, then personal review,
then built-in; nested objects merge per field and arrays replace, with per-field origins and
shadowed values. Personal spend and privacy are constraints, never
defaults: the repository can never raise the personal limit, and scopes
and flags can never narrow, remove, or except exclusions.

Every matching scope resolves in file order with last-field-wins
defaults, unioned requirements, and the last named profile; scopes with
none of `profile`, `defaults`, or `requirements` refuse, as does `profile`
combined with inline `defaults`. Requirement-only and
profile-plus-requirements scopes are legal. Renames match both the old and
the new path with provenance retained. Identical clauses normalize to one
entry with all provenances; `require_bind` accumulates by OR, where false
adds nothing.

## Friction derivation

The contract asks both that profile checks cover the accumulated required
checks per group and that a requirement-only scope may add another check
to its group. Refusing there would make requirement-only scopes unusable,
so `effectiveProfile` unions the accumulated required checks into the
planned checks instead of refusing: requirements always add mandatory
work to the plan, whose status stays `pending` until evidence exists. The
per-group clause rule still refuses: a group with `no-open-above-block`
and no applicable clause is `bad-requirement` naming the group.

## Group identity

Each plan group carries a stable `id` beside its `label`. The id is the
full SHA-256 of an unambiguous, length-prefixed encoding of the group's
effective work (defaults, requirement content, profile triple) and its
sorted path membership, so two groups that share a profile label but
differ in requirements or files are different identities. The label is
the explanation name only (`<profile>`, or `group N` when unnamed) and no
observation or refusal uses it as an identity.

## Evidence

`config.Capture` records the allowlisted fetch-time evidence: effective
values with origins, shadowed values, consumed input names (never whole
environment dumps), the base SHA, the configuration hash, per-file scope
sources, applicable clauses with `pending` status, and the selected plan
with its group identities. Stage flags and consents belong to their own
invocation records.

## Preview

`config <owner/repo> <pr> --preview [--file <path>]` resolves what a
prospective review would enforce, with no live call, exec, or write. The
repository layer comes from the recorded trusted-base inventory beside the
run bundle when a verified snapshot exists, else from `--file`. It reports
the same requirement rows the fetch capture stores — the root and
per-scope clauses, the required checks, and `require_bind` — each
`pending`, so a block clause can no longer preview as `requirements: []`
while `--explain` lists it. `checks` stays absent: a preview has no
observation and none is invented. Captured rename identities retain both paths,
so an old-path scope stays applicable in preview. Plan groups carry the checks those
requirements add, because the same resolution feeds the plan.

Provenance is labeled, never implied:

- `trusted base inventory` with `trusted: true` only when the recorded
  snapshot verified and carried `.verdict/config.json`; requirement
  sources then name the captured base commit.
- `recorded worktree inventory` with `trusted: false` for the explicit
  head-controls opt-out: recorded worktree bytes never claim base provenance.
- `no repository policy` only when that verified snapshot records the file
  absent from the base.
- `trusted base unavailable (no run bundle)`, with a warning naming the
  remedy, when no bundle exists: nothing then read the base, so the
  repository layer is unknown and the preview says so instead of claiming
  the repository configures nothing. The personal, environment, and
  built-in layers still resolve, and no requirement is reported that
  nothing proved.
- `proposed file <path>` with `trusted: false` for `--file`: every value
  and requirement the repository layer supplied names that file instead of
  the tracked path the preview never read. Scope, environment, personal,
  and built-in values keep their own labels, and nothing here certifies the
  proposed input as trusted policy or its requirements as met.

The selector spelling is a documented difference from D7 §10: the shipped
CLI takes the bundle selector positionally and the proposed path as
`--file`, so `--bundle` and `--proposed` are not aliases that exist. Both
modes travel inside `data` as the `configData` profile, so the CLI, the MCP
read-only tool, and the TypeScript client read one shape.

## Consumers

`fetch`/`fetch-local` resolve the two-phase policy and persist the run
bundle with the inventory snapshot; `post` enforces clauses, bind, floor,
and the selected typed checks, appending one observation per scope and
reviewed head; `report` enforces the display floor and records no check
evidence; `verdict config` explains and previews. MCP, TypeScript, and the
Go facade expose the same `configData` profile.

## Benchmark severity weights

Root `severity_weights` configures benchmark scoring. Each optional integer
field overrides its built-in value: `critical: 25`, `important: 5`,
`informational: 1`. Values must be in `1..1000000`; null and unknown keys
refuse. These are global benchmark settings, independent of review scopes.
A run snapshots the resolved weights so later configuration edits cannot
change a published score. Missing finding severity uses informational weight.
