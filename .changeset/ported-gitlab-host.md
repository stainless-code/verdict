---
"@stainless-code/verdict": patch
---

A self-hosted GitLab on a non-default port (`gitlab.corp.example:8443`) works for a URL or `origin` target and for `post`, instead of refusing `host-mismatch`. glab's `--hostname` cannot carry a port, so verdict runs such a call unpinned, but only when the environment already makes glab's default host exactly the target: `GITLAB_HOST` names the same host and port, `GITLAB_API_HOST` (if set) and, under glab's CI auto-login, `CI_SERVER_FQDN` name it too, and `GITLAB_REPO` is unset. Any other environment refuses, naming the variable to set. An ssh `origin` still refuses when `GITLAB_HOST` names a port, and GitHub hosts with a port still refuse.
