---
"@stainless-code/verdict": patch
---

Reviews of other repositories stop carrying this repository's own conventions.
The shared system prompt now defers branch, commit-message, formatting, and
style rules to the target repo's `AGENTS.md`/`CLAUDE.md`/`CONTRIBUTING`, with
verdict's own rules staying in its `AGENTS.md`, so reviews of this repo keep
them. The fix flow opens with the untrusted-data preamble, the abort-if-not-
introduced pin is stated where the flow can follow it, and the manual records
the machine-modes-never-prompt doctrine and a combined
`report --check --completeness` pre-push probe. `--show-prompts` wraps every
untrusted bundle section in a per-run nonce fence named in a trusted prologue,
so a forged close smuggled in a diff stays evidence instead of ending the
section; thread files stay off the initial-review reading list by design.
Missing trusted prompt files no longer vanish silently: a missing
`prompts/system.md` refuses typed, while a missing `SKILL.md`,
`prompts/review.md`, or `prompts/diagrams.md` warns and proceeds; missing
bundle files still skip silently.
