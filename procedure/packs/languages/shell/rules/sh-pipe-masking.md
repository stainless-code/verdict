Report a failure the script cannot see: a pipeline whose left side's exit status
is discarded (`cmd | tee`, `cmd | grep -q`), a `while read` fed by a pipeline,
or a `set -e` script with no `pipefail`.
