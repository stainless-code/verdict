# Shell lens

Dispatch only when the diff changes what a script expands, what it deletes,
how a failure propagates, whether it evaluates data as code, or where it
writes temporary files. Frame one falsifiable question — which word splitting
changes the target, which failure the pipeline hides, which path can resolve
elsewhere, which string becomes code — and give it a short hypothesis label.

Read the changed script and its shebang; the interpreter decides the answer.
`set -o pipefail` was not in POSIX before POSIX.1-2024 (Issue 8): older
`/bin/sh` and `dash <= 0.5.12-6` (Debian 12, Ubuntu 24.04 LTS) fail with
`Illegal option -o pipefail`, while `dash >= 0.5.12-7`/`0.5.13` added it.
Check the target's shell before calling a `#!/bin/sh` script wrong. `set -e`
makes a failing `cd` exit unless the command is in a condition. Scope the
question to at most ~12 files, cite NEW line numbers, and name the value that
makes the command leave its intended target.
