---
"@stainless-code/verdict": patch
---

Failures a 0.3.1 user could hit are gone. `fetch` on GitLab looks up the
project id with the plain `owner%2Frepo` path — the old request carried a
trailing encoded newline, which could fail the lookup at the first step.
Provider lists are read completely: Forgejo files and reviews, GitLab MR notes,
and Azure change entries and threads page instead of stopping at the first
page, and a page cap, a failed page, or a failed thread read lands an envelope
warning instead of silently under-covering. `fetch` retries GitHub's transient
"internal error, please try again" on the same three-attempt budget `post`
already had, while auth and validation failures still fail fast. Machine mode
never prompts, even on a TTY: `init --json` and `install --json` without
`--yes`/`--force` refuse typed `needs-confirm`, write nothing, and print the
hint instead of waiting on a `Proceed?` prompt. Malformed agent input fails
closed with `bad-review-input` — unparsable `review.json`, non-object
`comments`/`triage`/`sections` entries, non-string scalars, misshapen
verification claims — and a corrupt findings store now refuses typed
`corrupt-store` across `report`, `show`, `calibrate`, and `trend` instead of
crashing mid-render with no envelope, never starting fresh over history.

A value flag given without its value now fails as a usage error naming what
it wanted (`--provider needs a value (want <name>)`, exit 2) before any
network call, instead of falling back to a default target. A bare `--stash`
still means `stash@{0}`.

Cancellation is clean. SIGTERM now cancels a running verb the way Ctrl-C
does: exit 3 with a `canceled` envelope, and child process groups are killed
instead of orphaned. A cancelled verb returns promptly even when a grandchild
process still holds its output pipe. Interrupting `verify` refuses
`canceled` and records nothing, where it used to record a false
`timeout after 300s` proof and exit 0. On Windows, `verdict upgrade` stages its
helper beside the installed binary, so an install directory on another drive
no longer makes the final move fail after the upgrade reported success.
`threads.json` always labels a thread's author kind, including a thread with
no readable comments, as the published schema requires.
