---
"@stainless-code/verdict": major
---

Add typed configuration in `.verdict/config.json`, with path scopes, captured explanations, review profiles, grouped spend and diff limits, and recorded deep validation. Local run artifacts now live under `.verdict/state/`; retired `.review/` knob files are no longer read.

Cost records use schema version 2 with grouped lens and cost evidence. The Go Commander interface now takes a command pointer and preserves its working directory.
