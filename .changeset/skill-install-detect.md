---
"@stainless-code/verdict": minor
---

One command adds the review skill everywhere it is wanted. `verdict skill
install --detect` installs the embedded skill into every coding agent whose
skills dir already exists and holds a file (the same test `install-skill.sh
--detect` applies, except that it never creates the shared `~/.agents/skills`
dir and it also covers Codex), instead of making operators look up the
per-agent path. The run names each target and its result, and `--json` reports
one envelope whose `data.agents` lists the detected agents in the agent table's
order with each target's status, path, written count, and changed files. With no
agent set up it refuses typed `skill-no-detected-agent` naming the agents it
knows and pointing at `--agent <name>`, and `--detect` cannot be combined with
`--dir` or `--agent`. Each target keeps the receipt and `--force` rules, and a
blocked or conflicting target refuses before any target is written. `install.sh` and `install.ps1` now end a successful install
with the exact next command, and the manual documents the flag.

Signature verification now matches the certificate the release actually mints:
the Release workflow runs on a push to `main`, so its keyless identity is
`.../release.yml@refs/heads/main`, and `install.sh`, `install.ps1`, `verdict
upgrade`, and `setup-verdict` check that instead of a tag identity no release
run ever had — an install with `cosign` on the PATH used to abort. `verdict
upgrade` also recognizes an install made with `VERDICT_INSTALL`: it reads the
installer receipt beside the running binary, so the self-replace stages in that
directory instead of refusing the channel as unknown.
