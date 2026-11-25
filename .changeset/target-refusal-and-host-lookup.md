---
"@stainless-code/verdict": minor
---

**Breaking:** a verb with no resolvable target now exits 2, the usage-error class, with the typed `target-unresolved` refusal naming the arguments to pass, followed by the verb's usage line (under `--json`, a full refusal envelope). Scripts that treated exit 1 as "no target" must accept 2. The TypeScript client throws `VerdictRefusalError` for it, and the Python example raises `VerdictRefusal`.

Verbs that already talk to the host (`fetch`, `pr-wait`, `ready`, `threads`, `threads resolve`, `drain --post`) now ask the host CLI for the branch's open pull request when no recorded bundle names it. The answer must pass the same checks as a pasted URL, and the pull request's head must live in the `origin` repository, so a fork's PR with the same branch name never counts. The chosen target is echoed. Offline verbs never reach the host. See `docs/manual.md` § Targets.
