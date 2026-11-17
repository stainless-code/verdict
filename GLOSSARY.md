# Glossary

## Bootstrap stub

The installed `verdict/` skill: `SKILL.md`, `NOTICE.md`, and `scripts/verdict`.
It names when to use Verdict and how to reach the binary. It carries no
Procedure, prompts, packs, references, or schemas, so a skill install writes
three files and a receipt. `stub_version` in `SKILL.md` frontmatter pins the
stub contract; `verdict skill status` compares that value with the running
binary, not the file's other bytes.

## Procedure

The review instructions served by the binary: `procedure/PROCEDURE.md` and
everything under `procedure/` (prompts, lens packs, and references), plus the
published `schemas/`. `verdict skill` prints the Procedure index,
`verdict skill files` lists the served paths, and `verdict skill show <path>`
prints one of them byte-identically. The checkout and the npm tarball do not
ship the Procedure; the Go binary embeds it at build time, so the instructions
are the same version as the checker.

## Shim resolution

`scripts/verdict` resolves the binary in a fixed order: `VERDICT_BIN`, the
enclosing checkout's `./node_modules/.bin/verdict`, the skill's own
`bin/verdict`, then `verdict` on `PATH`. A miss is a typed `missing-go-binary`
refusal naming `scripts/verdict bootstrap`; bootstrap is explicit and no review
path downloads a binary.
