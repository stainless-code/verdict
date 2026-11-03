---
"@stainless-code/verdict": patch
---

Small additive reads. `post.json` and `record.json` now record `tool_version`,
`prompts_hash`, and `model`, and `calibrate` and `trend` surface them so a
mixed-version history reads as mixed. `show --json` gains a `data.fix` block
beside the unchanged `data.item`: full sig, status, severity, deduped files,
locator, suggestion, a `verify_hint` read from the bundle, and a fixed
untrusted-evidence caution — a pure read with no flags, no execution, and no
store writes, and missing history reads as null rather than invented text.
`doctor` human output opens with a run stamp and closes with one line,
`Summary: healthy|unhealthy — N ok, M warn, K fail`, while `--json` envelopes
and exits stay byte-identical. Builds with no release metadata now say
`0.0.0-unknown` everywhere — `--version`, the skill payload and receipt, and
the posted or reported footer — instead of the footer saying `unknown`.
