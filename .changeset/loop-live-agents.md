---
"@stainless-code/verdict": patch
---

`verdict run --agent claude` now writes its review without a manual permission flag. Every claude launch carries one grant scoped to that launch's own bundle directory (`--allowedTools=Edit(./.verdict/state/<bundle>/**)`), so the agent writes `review.json` and nothing else: a review still cannot edit source. A tool-permission flag you pass after `--` drops the grant along with the Bash/PowerShell restriction. The `no-review-artifact` hint no longer suggests the broader `--permission-mode acceptEdits` for claude, and `init --ci` workflows name the grant in their comment.

`verdict loop` now works with stock agent setups. Its fix spawn carries the agent's documented edit grant (claude `--permission-mode acceptEdits`, codex `--sandbox workspace-write`, none for other agents), and `verdict loop … -- <agent args>` passes arguments to every spawn, replacing that grant. `--print` shows each grant in the exact argv.

The review handoff (`procedure.md` and each finder's procedure) now asks the agent for the `coverage[]` attestation the default `gate` requires (`[]` when nothing changed; `chunks[]` on a chunked bundle), and the two-finder union keeps an empty attestation. Before, every `run --agent` review — including the workflows `init --ci` generates — failed the default gate on coverage, and `verdict loop` on a clean tree could never pass. Loop's stop line also names a refusal's code once instead of twice.

`verdict run --require-coverage` carries `record --require-coverage` into the launch: a review missing its coverage attestation gets a repair round naming the new `coverage-incomplete` problem, and a run that ends with only that gap refuses `coverage-incomplete` as record would. The loop's review step passes it, so a review that omits `coverage[]` is repaired instead of stopping the loop at record.
