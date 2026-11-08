---
"@stainless-code/verdict": patch
---

Public surfaces now match the binary. The docs site, its home pages, and the CI review example use `.verdict/state/`; the operator manual documents deep validation, path-scoped profiles, the evidence ledger, posting guards, local review coverage, and the shipped CLI flags, and states that `verification.json` is agent attestation rather than tool proof; the TypeScript client throws a typed `VerdictGateError` for a gate-shaped exit 1 instead of a schema error; the Python smoke uses the real bundle path and runs in CI; the Go SDK exposes record dry-run/strict/cost-names and fetch/fetch-local head/worktree controls; `config --explain` human output shows privacy exclusions and plan group ids; `spend-capped` carries a hint; and the README says the Go SDK runs in-process.
