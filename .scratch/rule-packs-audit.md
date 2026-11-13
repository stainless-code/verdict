# STA-96 audit

Base: 7ec5560ef730b0a9bbdd96556412944bebfe5adf.

- `internal/core/pack/pack.go:113` already models IDs, severity references, scopes and tools; `:792` loads declared bodies; `:864` hashes sorted content with framed boundaries. Missing category, description and examples metadata.
- `internal/core/learn/service.go:274` appends store objects, but cannot explicitly import pack rules.
- `internal/controls/controls.go:60` snapshots tracked packs; the inventory also snapshots `.verdict/kb.jsonl`. Imported objects and provenance therefore enter controls through the existing byte-hashed KB snapshot, without a second mutable import registry.
- `internal/core/fetch/kbrecall.go:50` already recalls installed pack rules. Installation and store import are distinct operations; do not change existing lens-pack behavior.
- `internal/core/pack/pack.go:272` accepts an explicit directory, with shared bounds and unsafe-path refusal. Reuse it; import must never resolve installed or ambient packs.
- Unknown selected IDs need a refusal before store writes. Existing store duplicate and corrupt-store refusals can be reused.

Ordered build plan:
1. Add optional structured metadata to pack rule objects, retaining compatibility with existing lens packs; regression and mutation proof.
2. Add explicit `learn --pack <directory> --id <rule-id>` import into the existing KB store, preserving resolved severity, structured metadata and pack hash provenance. Document controls recording and explicit paths; regenerate schemas/clients and add a changeset. Test unknown IDs, duplicates, metadata and reproducible bytes; mutation proof.
