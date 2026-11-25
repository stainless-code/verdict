---
"@stainless-code/verdict": patch
---

A self-hosted GitLab on a non-default port (`gitlab.corp.example:8443`) works again for a URL or `origin` target and for `post`, instead of refusing `host-mismatch`. glab's `--hostname` cannot carry a port, so verdict cannot pin the call; it now runs it unpinned, with no `--hostname` and `-R` as the bare `group/project`, but only when the environment already makes glab's own default host exactly the target: `GITLAB_HOST` names the same host and port, `GITLAB_API_HOST` (if set) names it too, under glab's CI auto-login `CI_SERVER_FQDN` names it, and `GITLAB_REPO` is unset. Any other environment refuses as before, naming the variable to set, and the runner re-checks the environment glab is spawned with. An ssh `origin` still refuses when `GITLAB_HOST` names a port, since an ssh remote names no web port and the target's authority is ambiguous. GitHub hosts with a port still refuse.
