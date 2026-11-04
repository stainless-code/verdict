---
"@stainless-code/verdict": patch
---

Global flags work anywhere on the command line — before the verb, between the
verb and the repo, or after the pr — so `verdict --no-json report acme/app 9`
and `verdict report acme/app 9 --no-json` print the same output. A bare `--` now
ends positional parsing, so everything after it is a positional and
`verdict fetch-local acme/app -- --json` is a usage error instead of a stray flag.
