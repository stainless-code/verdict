---
"@stainless-code/verdict": minor
---

`fetch` and `fetch-local` read GitLab, GitCafe, Forgejo, Bitbucket, and Azure
alongside GitHub: `--provider gitlab|gitcafe|forgejo|bitbucket|azure` writes the
same `.review/<pr>/` bundle with `PR.json` normalized to the GitHub shape, so
`record`, `report`, `triage`, `show`, `next`, and `threads` read it unchanged,
while `post`, `calibrate`, `adopt`, and `ready` refuse a non-GitHub bundle typed
(`provider-unimplemented`). Large PRs stay reviewable: the file listing pages
past the CLI's own cap with the provider ceiling noted, an upstream
over-file-count refusal names the limit and the recovery, a diff past the
512,000-byte cap refuses with a split-or-narrow hint unless `--chunk` opts into
sliced chunks, and secondary rate limits back off within bounds. `fetch-local`
builds the same bundle from git alone — the dirty tree, `--staged`, `--range`,
`--base`, `--stash`, or an isolated `--worktree` checkout — with no PR and no
host.
