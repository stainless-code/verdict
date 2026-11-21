---
"@stainless-code/verdict": minor
---

`verdict init --ci github` writes `.github/workflows/verdict-review.yml`: one committed workflow that runs the whole review on every pull request with the operator's own agent and model key, and no hosted service. The steps are inline and pinned to full 40-hex commits — checkout, `setup-verdict`, `verdict skill install`, `fetch`, `ready --check`, `run --agent`, `post`, an advisory `report`, then `verdict gate --max-severity critical --require-head --live auto`. The pipeline deviates from the ticket's prose in one deliberate way: `post` precedes `gate`, because the gate's `--require-head` and coverage dimensions read the receipt `post` writes.

The generated job runs on `pull_request` (never `pull_request_target`) and skips fork pull requests with a job-level guard, since a fork run gets no repository secrets. No secret is written into a script: each arrives as a step `env:` entry, and the agent step clears `GH_TOKEN` and `GITHUB_TOKEN`.

The workflow installs verdict, never the agent CLI it launches: the operator installs the selected agent on the runner before the `Agent review` step, and the catalog's `Install` command rides along inside that step as a comment, never as a step running an unpinned installer.

`--agent <name>` takes any `run --agent` catalog name (default `claude`), `--key-secret <NAME>` renames the repository secret while keeping the variable the agent reads (`--agent claude --key-secret REVIEW_KEY` renders `ANTHROPIC_API_KEY: ${{ secrets.REVIEW_KEY }}`), and `--print` renders the file without writing anything. The skill step installs into the selected agent's own skills directory, or into the shared `agents` directory when its catalog row declares none — naming such an agent there is `unknown-agent`. The catalog gained `Launch.KeyEnv`/`KeyEnvCitation` on `claude` and `codex`, so the default key variable stays single-sourced. `init`'s overwrite guard now covers the workflow, a refused run leaves zero filesystem side effects, and the JSON envelope gains the optional `ci`, `workflow_path`, `workflow_status`, and `workflow` fields.
