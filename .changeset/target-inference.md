---
"@stainless-code/verdict": minor
---

Every verb that takes `<owner/repo> <pr>` now also takes a pull or merge request URL in its place (GitHub, GitLab including nested groups, Forgejo/Codeberg, Bitbucket, Azure DevOps; a self-hosted GitLab or Forgejo only when `GITLAB_HOST` / `FORGEJO_HOST` already maps it). The URL implies `--provider`, and an explicit conflicting `--provider` refuses `bad-target-url`, as do hostile or ambiguous URLs. With no positionals, the repository is read from the `origin` remote and the pull request from the one recorded `.verdict/state` bundle whose head ref is the current branch, offline; an ambiguous or missing match refuses `target-unresolved` with the exact arguments to pass. A derived target is echoed on stderr in human output. `fetch-local` keeps its own syntax. See `docs/manual.md` § Targets.
