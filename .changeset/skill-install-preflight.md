---
"@stainless-code/verdict": patch
---

`verdict skill install --detect` no longer half-installs when a later target
cannot be written. Every target is planned and preflighted before the first
byte is written, so a read-only skills dir anywhere in the set refuses with
`skill-install-failed`, names that target, and leaves the earlier targets
absent instead of already installed. A receipt path that cannot be replaced
refuses with `skill-foreign-conflict` the same way, before the payload lands.
A repeated `--agent` and a single-target install refuse the same way, before
anything is written.
