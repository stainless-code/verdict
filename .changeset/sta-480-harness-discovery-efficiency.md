---
"@stainless-code/verdict": minor
---

`run` now launches discovery read-only. Every finder, cross-check, repair round and drain launch passes the agent's own documented tool restriction, so an agent cannot spend its budget on builds and tests that `verify --sandbox` runs better; file reads and writing the review files stay allowed. Three agents document such a flag: `claude` gets `--disallowedTools=Bash,PowerShell`, `bob` gets `--disable-tool-groups execute,mcp`, and `pi` gets `--exclude-tools bash,powershell`. Other agents launch unchanged. **Breaking:** pass `--allow-shell` if your agent needs to run builds during review. No flag you pass after `--` drops the restriction (pi's `--exclude-tools` and bob's `--disable-tool-groups` get its value merged in), and `--resume` refuses `resume-drift` if the restriction changed.

Chunked fetches now keep related files together. Files are ordered by package directory, then implementation beside its test (`foo.go` and `foo_test.go`), then path, before they are packed. Per-group runs never mix groups in one chunk, and chunks are cut from the privacy-filtered diff, so a withheld section never reaches `chunk-NN.diff`.
