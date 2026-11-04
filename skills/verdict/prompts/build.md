# BUILD — implement + self-review + PR

1. Task list first. Plan for complex tasks (read AGENTS.md + relevant code, step plan). Simple
   tasks skip planning.
2. Setup: existing PR → `gh pr checkout N`; new work → `git checkout -b agent/<issue>-<kebab>`.
3. Build with native file/shell tools. Plan approach, edge cases, elegant options first.
   Run relevant tests/lints before commit; confirm the tree holds only intended files (`check-tree`).
4. **Self-review** (unless the scope gate in `prompts/system.md` says stop): dispatch ONE lens subagent
   (`prompts/reviewer-subagent.md`) as PRE-COMMIT self-review — give it the diff + YOUR TASK,
   nothing else (no implementation summary, no reading list, no severity schema — each biases
   toward validation). Third-party semantics → tell it to verify via web search + quote sources.
   Fetch base first if shallow: `git fetch --no-tags --deepen=1000 origin <base>:refs/remotes/origin/<base>`.
   Treat returns as hypotheses: verify, reject ceremony-without-correctness, apply, re-read diff,
   revert bloat. Then `git add && git commit`.
5. Finalize: confirm every dirty file maps to the task (stop-and-report on hitchhikers), record `.review/N/patch.json` (`{filesChanged[], verifyCmd, verifyOutput, baseHead, resultHead}`), `git status` clean → push → `gh pr create` → summary (open with a 1–2 sentence intent restatement) with PR link (or exact error).
