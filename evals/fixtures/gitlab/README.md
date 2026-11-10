# GitLab MR fixtures (hermetic: no glab binary, no network)

The payloads `fetch --provider gitlab`, `post --dry-run`, and `doctor
--provider gitlab` read from `glab`, as the CLI prints them.
`evals/cases/gitlab-fetch-post.sh`, `evals/cases/gitlab-adopt.sh`,
`evals/cases/gitlab-ready.sh`, `evals/cases/gitlab-calibrate.sh`, and
`evals/cases/doctor-provider.sh` register each argv through
`evals/lib/hoststub.sh`; nothing here reaches a host, and an unregistered argv
fails the stub instead of fleetingly passing.

| File                             | `glab` argv                                           | Payload                                                                      |
| -------------------------------- | ----------------------------------------------------- | ---------------------------------------------------------------------------- |
| `MR.json`                        | `mr view 7 -R acme/app -F json`                       | MR metadata: head `aaaa…`, base `bbb…`, `opened`                             |
| `project-id.txt`                 | `api projects/acme%2Fapp --jq .id`                    | the numeric project id `4242`                                                |
| `diffs.json`                     | `api projects/4242/merge_requests/7/diffs --paginate` | one changed file (`notes.txt`, +1/−1)                                        |
| `full.diff`                      | `mr diff 7 -R acme/app --raw`                         | the whole one-hunk diff                                                      |
| `notes.json`                     | `api …/merge_requests/7/notes?per_page=100&page=1`    | one MR comment and one `DiffNote`                                            |
| `discussions.json`               | `api …/merge_requests/7/discussions?…&page=1`         | the same two notes as one unresolved and one resolved thread                 |
| `glab-version.txt`               | `--version`                                           | the one-line version `doctor` reports                                        |
| `glab-auth-status.txt`           | `auth status`                                         | a logged-in `glab auth status`                                               |
| `glab-user.json`                 | `api user`                                            | the read `doctor` proves API reachability with                               |
| `expected-hunks.tsv`             | —                                                     | the `hunks.tsv` the fetch must index                                         |
| `expected-argvs.txt`             | —                                                     | the exact `glab` call sequence, in order, fetch then post                    |
| `expected-doctor-argvs.txt`      | —                                                     | the exact `glab` call sequence `doctor --provider gitlab` makes              |
| -------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------                 |
| `MR-8.json`                      | `mr view 8 -R acme/app -F json`                       | the same MR at head `dddd…` for the failed-compare leg                       |
| `compare.json`                   | `api …/repository/compare?from=aaaa…&to=cccc…`        | `calibrate` leg: `notes.txt` changed since the review head                   |
| `expected-argvs-incremental.txt` | —                                                     | the exact `glab` call sequence for the `--base-sha` fetch                    |
| `expected-incremental.diff`      | —                                                     | the literal `incremental.diff` the compare join must write                   |
| `expected-note-fail.txt`         | —                                                     | the note a failed compare writes while the fetch succeeds                    |
| ----------------------------     | ----------------------------------------------------- | ---------------------------------------------------------------------------- |
| `MR-ready-blocked.json`          | `api projects/acme%2Fapp/merge_requests/7`            | `ready` leg: draft, `has_conflicts`, `conflict` status, failed head pipeline |
| `MR-ready-clean.json`            | `api projects/acme%2Fapp/merge_requests/8`            | `ready` leg: clean merge request, passing head pipeline                      |
| `MR8.json`                       | `mr view 8 -R acme/app -F json`                       | `ready` leg: the clean leg's fetch metadata (`iid` 8)                        |
| `MR-calibrate.json`              | `api projects/acme%2Fapp/merge_requests/7`            | `calibrate` leg: live head `cccc…` (stale vs reviewed `aaaa…`)               |
| `pipelines-blocked.json`         | `api …/merge_requests/7/pipelines?per_page=100`       | `ready` leg: one passing and one failed pipeline                             |
| `pipelines-clean.json`           | `api …/merge_requests/8/pipelines?per_page=100`       | `ready` leg: one passing pipeline                                            |
| `discussions-resolved.json`      | `api …/merge_requests/N/discussions?per_page=100`     | `ready` legs: two resolved threads, so only MR/CI blockers show              |
| `discussions-calibrate.json`     | `api …/merge_requests/7/discussions?per_page=100`     | `calibrate` leg: one resolved and one open marker-stamped thread             |

`MR.json` also answers `post`'s live-SHA read (`api
projects/acme%2Fapp/merge_requests/7`), so the dry run sees the same head it
fetched. The head and base SHAs are literals the eval pins; the base `bbb…`
is the commit the harness seeds through `git replace`, so the controls gate
resolves against the sandbox checkout.

Bodies are formatted JSON, not the one-line wire bytes `glab` may print: the
fixtures are graded by the parsed result, never by payload shape.
