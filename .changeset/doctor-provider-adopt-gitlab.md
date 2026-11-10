---
"@stainless-code/verdict": minor
---

`doctor --provider github|gitlab|gitcafe|forgejo|bitbucket|azure` preflights one
host instead of always assuming GitHub: the same enum and `bad-provider`
refusal as `fetch --provider`, defaulting to `github` so a bare `doctor` keeps
its rows byte for byte. Each host has its own probe table — `gh` and its scope
rows for GitHub, `glab` presence, `glab auth status`, and a `glab api user`
reachability read for GitLab, the `cafe` CLI for gitcafe, and `curl`/`az` plus
the credential variables their fetch paths read (`FORGEJO_TOKEN`,
`BITBUCKET_TOKEN`, `AZURE_DEVOPS_EXT_PAT`, …) for the rest, reported by presence
and never by value. A non-GitHub preflight shows no `gh` row.

`adopt` now hands local findings to any bundle verdict can post to, so a GitLab
bundle adopts exactly like a GitHub one; its re-anchor reads the stored hunks
and shells no host CLI. Bundles on a host with no submit path still refuse
`provider-unimplemented`, with the host named in the message.
