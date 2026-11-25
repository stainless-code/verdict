---
"@stainless-code/verdict": minor
---

Every git call verdict makes now runs no code the reviewed checkout chooses. Each call names its repository with `-C`, drops inherited `GIT_INDEX_FILE`, `GIT_DIR`, and git's other location variables plus `GIT_CONFIG_PARAMETERS`, and runs with `core.hooksPath` set to a path with no hooks and `core.fsmonitor=false`. Every diff, log, and show runs with `--no-ext-diff --no-textconv`. Before, an in-tree `core.hooksPath` such as `.husky/`, an fsmonitor, a textconv or external diff driver, or a git-lfs smudge reaching a URL the PR's `.lfsconfig` picked could run during `fetch-local`, `fix --test --commit`, or a `backtest` replay. An inherited `GIT_INDEX_FILE` could also send a call to another index.

`fix --test --commit` no longer runs commit hooks: not `.git/hooks`, and not a hooks path the tree sets. The commit carries reviewed content, so run your hooks yourself before you push.

`fetch-local --worktree` and the `backtest` replay checkout now switch off every configured filter driver, so each file lands as its blob bytes and an LFS-tracked file is its pointer. Status, diffs, and `add` on your own working tree keep your clean filters.
