# Pipeline exit status masking

A pipeline returns the status of its last command by default. The left side's
failure disappears unless the shell supports `pipefail` and the script turns
it on.

## When to flag

- `cmd | tee file` in a deploy or migration script where `cmd` failing must
  stop the script; the pipeline's status is `tee`'s.
- `set -e` with no `set -o pipefail` in a bash script, where a pipeline's
  failing producer is ignored.
- `if cmd | grep -q pattern; then` expecting `cmd`'s failure to be visible;
  only `grep`'s status reaches `if`.
- `cmd | while read ...` where `cmd`'s failure is discarded and the loop
  proceeds on partial output.
- A `#!/bin/sh` script that sets `set -o pipefail` on a shell without it.
  POSIX did not include `pipefail` before POSIX.1-2024 (Issue 8); older
  `/bin/sh` and `dash <= 0.5.12-6` (Debian 12, Ubuntu 24.04 LTS) exit with
  `Illegal option -o pipefail`, while `dash >= 0.5.12-7`/`0.5.13` added it.
  Either declare bash, check the target shell, or handle the pipeline
  explicitly.

## Do NOT flag

- A bash/ksh/zsh script with `set -o pipefail` (or `set -euo pipefail`)
  active for the pipeline; that is the fix.
- A pipeline whose only meaningful status is the last command's, such as
  `printf ... | sort` where the producer is a builtin that cannot fail.
- `cmd | head -n 10` under `pipefail` that returns 141 because `head` closed
  the pipe: SIGPIPE is expected there, not a masked failure.
- A POSIX `sh` script that avoids `pipefail` and checks the producer
  separately or uses a temporary file, when the target shell predates
  POSIX.1-2024 or is an older `dash`.
- A pipeline explicitly allowed to fail (`|| true`) with a comment that the
  failure is non-fatal.
- A pipeline in a test fixture where the status is asserted downstream.

## Evidence

Quote the pipeline and the shell/shebang and whether `pipefail` is active.
Name the producer failure that is lost and what the script does anyway.
