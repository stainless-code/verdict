---
"@stainless-code/verdict": minor
---

`ready` advises on GitLab merge requests and plain `calibrate` joins live
GitLab threads, both through `glab` behind the same per-provider tables the
GitHub reads already sit behind (GitHub output byte-identical). `ready` reads
the merge request (`draft`, `has_conflicts`, `detailed_merge_status`,
`description`, head pipeline) plus the pipeline and discussion lists, so the
same draft, conflict, `ci-failing`, `threads-open`, and `description-thin`
blockers report on either host; `calibrate` matches the posted discussion
bodies (finding marker included) against the live discussions and reads
changed files through the repository compare, so the same fixed,
resolved-unfixed, open, and unknown outcomes join on either host. Bundles on
the remaining hosts still refuse `provider-unimplemented` naming the host.
