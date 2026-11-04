---
"@stainless-code/verdict": minor
---

Published machine contracts beside the CLI. `schemas/` ships JSON Schemas for
the review input, findings store, cost and usage records, PR threads, and the
`--json` envelope with per-verb `data` profiles, included in the npm tarball;
`schemas/validate.sh` checks documents against them offline (no `jq`; exit 0
valid, 1 rejected, 2 bad usage), and the drift rule is additive within schema
version 1. An experimental TypeScript client
(`@stainless-code/verdict/client`) wraps each verb with envelope types
generated from those schemas and refusal codes generated from the CLI, with no
runtime dependencies. The experimental Go package `pkg/verdict` embeds the
same operations in-process through a context-first client with typed requests
and responses and a `*RefusalError` for `errors.As`; its API may still change
before 1.0. A new "Build a client" guide documents the `--json` protocol —
envelope keys, exit and refusal codes, versioning, binary resolution,
timeouts, output caps, and when MCP beats spawning a process — with a
dependency-free Python client generated from the schemas and an offline smoke
test.
