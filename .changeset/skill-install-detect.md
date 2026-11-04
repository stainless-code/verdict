---
"@stainless-code/verdict": minor
---

`verdict skill install --detect` adds the review skill to every coding agent
already set up on the machine, instead of making you look up one path per agent.
It names each target and its result, and `--json` lists each target with its
status, path, and files written. With no agent set up it refuses and points at
`--agent <name>`, and `--detect` is mutually exclusive with `--dir` and
`--agent`. `install.sh` and `install.ps1` now close a successful install with
that exact command.
