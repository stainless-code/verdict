---
"@stainless-code/verdict": minor
---

`verdict init --ci github` writes `.github/workflows/verdict-review.yml`: one committed workflow that runs the whole review on every pull request with the operator's own agent and model key, and no hosted service. The steps are inline and pinned to full 40-hex commits — checkout, `setup-verdict`, `verdict skill install`, `fetch`, `ready --check`, `run --agent`, `post`, an advisory `report`, then `verdict gate --max-severity critical --require-head --live auto`. The pipeline deviates from the ticket's prose in one deliberate way: `post` precedes `gate`, because the gate's `--require-head` and coverage dimensions read the receipt `post` writes.

The generated job runs on `pull_request` (never `pull_request_target`) and skips fork pull requests with a job-level guard, since a fork run gets no repository secrets. No secret reaches a shell: each arrives as an `env:` entry, and the agent step clears `GH_TOKEN` and `GITHUB_TOKEN`.

`--agent <name>` takes any `run --agent` catalog name (default `claude`), `--key-secret <NAME>` overrides the repository secret, and `--print` renders the file without writing anything. The catalog gained `Launch.KeyEnv`/`KeyEnvCitation` on `claude` and `codex`, so the default key variable stays single-sourced. `init`'s overwrite guard now covers the workflow, a refused run leaves zero filesystem side effects, and the JSON envelope gains the optional `ci`, `workflow_path`, `workflow_status`, and `workflow` fields.
