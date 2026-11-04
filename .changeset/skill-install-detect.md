---
"@stainless-code/verdict": minor
---

One command adds the review skill everywhere it is wanted. `verdict skill
install --detect` installs the embedded skill into every coding agent whose
skills dir already exists and holds a file (its own detection rule, the same one
`install-skill.sh --detect` uses), instead of making operators look up the
per-agent path. The run names each target and its result, and `--json` reports
one envelope whose `data.agents` lists the detected agents in the agent table's
order with each target's status, path, written count, and changed files. With no
agent set up it refuses typed `skill-no-detected-agent` naming the agents it
knows and pointing at `--agent <name>`, and `--detect` cannot be combined with
`--dir` or `--agent`. Each target keeps the receipt and `--force` rules, and a
blocked target refuses before any target is written, so a partial install is
never left behind. `install.sh` and `install.ps1` now end a successful install
with the exact next command, and the manual documents the flag.
