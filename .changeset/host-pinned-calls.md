---
"@stainless-code/verdict": minor
---

**Breaking:** every `gh` and `glab` call for a target resolved from a URL or the `origin` remote now names that target's host (`gh api --hostname`, `gh pr … --repo <host>/<owner>/<repo>`, `glab api --hostname`, `glab mr … -R https://<host>/<path>`). `GH_HOST`, `GITLAB_HOST` or the CLI's default host can no longer send a fetch, reply or review to another host's pull request with the same number, and `post` always posts to the host its bundle came from.

When a call cannot be pinned, verdict refuses `host-mismatch` (exit 1) naming both hosts. For example: a Forgejo target on another host than `FORGEJO_HOST`; GitHub on a non-default port; GitLab on a non-default port unless the environment already selects that exact host (see the ported-GitLab entry); a GitLab target while `GITLAB_API_HOST` or `CI_SERVER_FQDN` names another server; or a bundle fetched from another host than the target. Two hosts that differ only by port are different hosts. If `post` refuses `host-mismatch`, re-fetch the target or pass a target on the bundle's host. A typed `<owner/repo> <pr>` names no host, so its reads still follow the host CLI's default. See `docs/manual.md` § Targets.
