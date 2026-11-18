---
"@stainless-code/verdict": minor
---

Every fetched bundle now carries `procedure.md`, the review Procedure assembled by the binary from its embedded payload. Drain plans carry `drain.procedure.md` for reply drafting. The `run`, stage, and drain directives name those files as authoritative, so an installed skill that is older can no longer displace them. The fetch `next:` hints and `--show-prompts` output point at the bundle Procedure, and provenance receipts record its sha256 as a bundle input. `PromptsHash` now reads the embedded payload, so an installed user gets the real prompt-set hash without a `procedure/` checkout.
