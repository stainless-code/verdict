---
"@stainless-code/verdict": minor
---

A pair verb with no resolvable target now exits 2 (the usage-error class) everywhere, inside or outside a checkout: the typed `target-unresolved` refusal names the exact arguments to pass, followed in human mode by the verb's own usage line (`--json` carries it as the envelope's error), instead of the full usage block or an exit-1 refusal. Verbs that already talk to the host (`fetch`, `pr-wait`, `ready`, `threads`, `threads resolve`, `drain --post`) now ask the host CLI for the branch's open pull request (`gh pr list --head`, `glab mr list --source-branch`) when no recorded bundle names it, accept the answer only when it passes the same strict checks a pasted URL gets, and echo `target: owner/repo#N (…, from <host> for branch <b>)`. Offline verbs never reach the host. See `docs/manual.md` § Targets.
