---
"@stainless-code/verdict": minor
---

`run --agent` now supports fourteen verified official agent CLIs, including `agy`, `amp`, `copilot`, `crush`, `droid`, `gemini`, `goose`, `kiro`, `pi`, and `qwen`, with prompt flags placed immediately before the directive for agents that take it as a flag value. Operators can define more agents under the new `agents` key of their own settings file; the shape is prefs-only, closed, and refuses name collisions and invalid binaries as `bad-settings`.
