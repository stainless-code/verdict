---
"@stainless-code/verdict": minor
---

`verdict init --ci forgejo` writes `.forgejo/workflows/verdict-review.yml`: the same every-PR pipeline `--ci github` renders, wired to verdict's native Forgejo provider. Only the fetch step passes `--provider forgejo`; `run`, `post`, `report`, and `gate` inherit the bundle's provider marker. Host and token come from the runner's own context — `FORGEJO_HOST: ${{ github.server_url }}` and `FORGEJO_TOKEN: ${{ github.token }}` — never a repository secret, and the agent step clears `GH_TOKEN`, `GITHUB_TOKEN`, and `FORGEJO_TOKEN`, because Forgejo injects both token names into every step.

The Forgejo render drops both `permissions:` blocks (Forgejo has no such key) and omits the `ready --check` step: `ready` refuses `provider-unimplemented` on a Forgejo bundle, so the step would fail every pull request, while the merge gate still blocks. The install step is fully qualified as `https://github.com/stainless-code/verdict/setup-verdict@<pin>`, because a short-form reference resolves through `DEFAULT_ACTIONS_URL`, which does not mirror this repository.

`--ci` now accepts `github` and `forgejo` from one provider registry, and `--ci` hints, usage errors, and `--print`/`--agent`/`--key-secret` scoping print both values. GitHub's rendered bytes are unchanged. The manual gains a _Forgejo Actions_ section naming the runner label, `cosign`, the checksum-only opt-out, and the air-gapped mirror.
