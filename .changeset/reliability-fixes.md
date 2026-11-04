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
`timeout after 300s` proof and exit 0. The `mcp` stdio server takes the same
signal: SIGTERM cancels an in-flight tool call and kills its process tree
instead of orphaning a running `verify` for the rest of the verify budget.
On Windows, `verdict upgrade` stages its
helper beside the installed binary, so an install directory on another drive
no longer makes the final move fail after the upgrade reported success.
On Windows, `verify` starts its command suspended and puts it in its
kill-on-close job object before it runs, so a grandchild spawned in the first
instant can no longer escape a timeout or cancel kill.
`threads.json` always labels a thread's author kind, including a thread with
no readable comments, as the published schema requires. A cancel that lands
inside a read verb's service (`ready`, `trend`, `threads`, `calibrate`,
`check-tree`, `doctor`, `report`, `next`) exits 3 with the `canceled` envelope
like every other verb, where it used to exit 1 as an ordinary refusal.

Provider page walks finish what they start. A GitLab merge request whose diff
list spans host pages (`glab api --paginate` concatenates one JSON array per
page) now yields every changed file instead of a `PR.json` with no files at
all, and a merged Bitbucket pull request (`state: MERGED`) folds to `merged`
rather than `unknown`. A GitCafe review/comment cursor walk that stops early — a
failed page, an unreadable page, or the 20-page cap — keeps the pages it already
read, warns, and marks `threads.json` `truncated: true` instead of quietly
under-covering the conversation; Forgejo's review list does the same at the
provider cap. Hosts with no failures read exactly as before.
A failed or unreadable Bitbucket diffstat/comments page, a GitLab changed-file
payload that is not a diff list, and a failed Azure iteration read no longer
pass as an empty list: each warns, and the Bitbucket threads feed is marked
`truncated: true`.

Report artifacts survive a kill. `report --output`, `report --sarif`, and the
MCP report artifact are staged beside the target and renamed into place, so a
crash mid-write can no longer leave a truncated report behind. A symlinked
`--output` path keeps the link and replaces only the file it names, an existing
file keeps its own mode (a new one is still owner-only), and a device like
`/dev/stdout` still receives the bytes directly. A failed write refuses
`report-failed` and leaves the previous report intact.

Numbers in machine output render the way jq 1.8 renders them. A literal such as
`9.223372036854775808e18` now prints as `9223372036854775808` on every
architecture, where arm64 used to saturate at `9223372036854775807` and amd64
wrap to `-9223372036854775808`.
