---
"@stainless-code/verdict": patch
---

Fail-closed reads: the pure-read verbs now refuse typed `corrupt-store` on
misshapen findings stores instead of crashing mid-render with no envelope.
`report`/`show`/`calibrate` guard every entry (non-object entries and
non-string `sig`/`body`/`heading`/`kind`/`suggestion` refuse; `report` also
refuses on an unreadable `patch.json`); sig-prefix resolution treats a null
sig as no-match instead of dying in `startswith`; `trend` warns and skips
corrupt bundles, rolling up the readable ones. Patch, not minor: valid
stores render byte-identically; only corrupt input changes outcome (typed
refusal or skip instead of a jq exit-5).
