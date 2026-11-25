---
"@stainless-code/verdict": patch
---

A two-finder `run` now launches both finders at once, then both cross-checks at once, instead of one after the other. A refusal in one phase stops its sibling; an agent failure lets the sibling finish so the run can report what completed. On timeout or Ctrl-C, an agent's process group now gets `SIGTERM` and five seconds to exit before `SIGKILL` on macOS and Linux (Windows still ends the job object), and `agent-timeout` is reported only for a real timeout, never for a cancel. A re-run on a re-fetched bundle moves stale finder and cross-check directories to `x-superseded/` instead of reading them.
