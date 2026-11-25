---
"@stainless-code/verdict": minor
---

The review handoff is now lean and layered. `<bundle>/procedure.md` carries a short core (role, method, the findings-only contract and a capability map), and sections for chunks, prior threads, knowledge-base rules, lenses and handed context appear only when the bundle has them. The checklist and reference material go to `<bundle>/refs/` for the agent to read on demand, and `verdict skill show` keeps the full workflow. `--show-prompts` prints the handoff and names the refs.

The agent is told to write `review.json` first and update it as it goes. After a clean exit with no review file, `run` salvages the last fenced `json` block on stdout that holds a `findings` array, records `salvaged: true` in `launcher.json`, and warns. A salvaged or written review that fails the checker refuses `bad-review-input` and feeds the next round. **Behavior change:** a review with no findings must carry a non-empty `summary`. `prompts_hash` now covers every file that feeds the handoff.
