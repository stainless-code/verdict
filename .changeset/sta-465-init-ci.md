---
"@stainless-code/verdict": minor
---

`verdict init --ci github` writes `.github/workflows/verdict-review.yml`: one workflow that reviews every pull request with your own agent and model key, with no hosted service. Its steps are pinned to full commit SHAs: checkout, `setup-verdict`, `verdict skill install`, `fetch`, `ready --check`, `run --agent`, `post`, an advisory `report`, then `gate --max-severity critical --require-head --live auto`. `post` runs before `gate` because the gate reads the receipt `post` writes. The job runs on `pull_request`, never `pull_request_target`, and skips fork pull requests, which get no secrets. Secrets arrive only through step `env:`, and the agent step clears `GH_TOKEN` and `GITHUB_TOKEN`. The workflow installs verdict but not the agent CLI: install it on the runner before the `Agent review` step, which carries the install command as a comment.

`--agent <name>` takes any `run --agent` name (default `claude`), `--key-secret <NAME>` renames the repository secret, and `--print` renders the file without writing it. The overwrite guard covers the workflow, and the JSON envelope gains `ci`, `workflow_path`, `workflow_status` and `workflow`.
