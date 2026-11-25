---
"@stainless-code/verdict": minor
---

`verdict init --ci forgejo` writes `.forgejo/workflows/verdict-review.yml`, the same every-PR pipeline as `--ci github` on verdict's native Forgejo provider. Host and token come from the runner (`FORGEJO_HOST: ${{ github.server_url }}`, `FORGEJO_TOKEN: ${{ github.token }}`), never a repository secret, and the agent step clears `GH_TOKEN`, `GITHUB_TOKEN` and `FORGEJO_TOKEN`. The Forgejo workflow drops the `permissions:` blocks, which Forgejo lacks, and the `ready --check` step, which Forgejo bundles do not support; the gate still blocks. The install step uses the full `https://github.com/stainless-code/verdict/setup-verdict@<pin>` reference, because a short reference resolves through a mirror that lacks it. The manual gains a _Forgejo Actions_ section.
