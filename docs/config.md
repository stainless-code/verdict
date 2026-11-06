# Typed configuration — foundation slice

Status: intermediate. This slice lands the typed configuration models, the
strict parser, the pure resolver, the state layout, and the trusted-base
source adapter. No verb reads these yet: `fetch`, `record`, `post`,
`report`, MCP, the TypeScript client, and the router gain their consumers
in later slices. The schema key set is therefore intentionally ahead of
its consumers; nothing below is inert — every key resolves, validates, or
constrains through `internal/config`.

## Layout

```text
.verdict/config.json            tracked, the typed configuration
.verdict/kb.jsonl               tracked, shared knowledge corpus
.verdict/learnings.md           tracked, shared heading memory
.verdict/overrides/             tracked, shared override files
.verdict/.gitignore             tracked-shape, contains the single line /state/
.verdict/state/                 ignored, local review state only
```

Only `/state/` is ignored, through the nested file. Legacy `.review/` is
untouched: no migration, no rewrite, no shim, no implicit fallback.
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
environment), the trusted-base configuration, personal settings, the base
SHA, the file list with pre-rename paths, and the settings path when one
was read. Ordinary defaults merge flag, then environment, then repository
scopes and shared defaults, then personal review, then built-in; nested
objects merge per field and arrays replace, with per-field origins and
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

## Evidence

`config.Capture` records the allowlisted fetch-time evidence: effective
values with origins, shadowed values, consumed input names (never whole
environment dumps), the base SHA, the configuration hash, per-file scope
sources, applicable clauses with `pending` status, and the selected plan.
Stage flags and consents belong to their own invocation records.

## Remaining interfaces

`config.ReadBase`/`ParseBase` supply the trusted-base read; the fetch
slice wires the inventory row, evidence file, and privacy enforcement.
The compliance slice consumes clauses, checks, `require_bind`, spend
guards, and display/withholding separation. The config verb slice adds
`--explain`/`--preview`, the envelope `configData` profile, and the MCP,
TypeScript, and router entries.
