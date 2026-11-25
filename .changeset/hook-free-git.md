---
"@stainless-code/verdict": minor
---

Every git call verdict makes now runs no code the reviewed checkout chooses; the one exception is your own clean and process filters, which still run when verdict reads or stages your own working tree. Each call names its repository with `-C`, drops inherited `GIT_DIR`, `GIT_INDEX_FILE` and git's other location variables (plus `GIT_EXEC_PATH` and `GIT_CONFIG_PARAMETERS`), and runs with hooks and fsmonitor off; every diff, log and show adds `--no-ext-diff --no-textconv`. Before, an in-tree `core.hooksPath` such as `.husky/`, an fsmonitor, a textconv or external diff driver, or a git-lfs smudge could run during `fetch-local`, `fix --test --commit` or a `backtest` replay.

**Breaking:** `fix --test --commit` no longer runs commit hooks, neither `.git/hooks` nor a hooks path the tree sets. The commit carries reviewed content, so run your hooks yourself before you push. Your clean and process filters still run for that commit, so under a git-lfs pattern it holds the LFS pointer.

`fetch-local --worktree` and the `backtest` replay checkout now switch off every filter driver, so each file lands as its blob bytes. Content naming for `verify` and `fetch-local` runs no filter at all (see the content-bound receipts entry).
