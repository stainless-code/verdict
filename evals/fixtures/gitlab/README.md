# GitLab MR fixtures (hermetic: no glab binary, no network)

The payloads `fetch --provider gitlab` and `post --dry-run` read from `glab`,
as the CLI prints them. `evals/cases/gitlab-fetch-post.sh` registers each
argv through `evals/lib/hoststub.sh`; nothing here reaches a host, and an
unregistered argv fails the stub instead of fleetingly passing.

| File                 | `glab` argv                                           | Payload                                                      |
| -------------------- | ----------------------------------------------------- | ------------------------------------------------------------ |
| `MR.json`            | `mr view 7 -R acme/app -F json`                       | MR metadata: head `aaaa…`, base `bbb…`, `opened`             |
| `project-id.txt`     | `api projects/acme%2Fapp --jq .id`                    | the numeric project id `4242`                                |
| `diffs.json`         | `api projects/4242/merge_requests/7/diffs --paginate` | one changed file (`notes.txt`, +1/−1)                        |
| `full.diff`          | `mr diff 7 -R acme/app --raw`                         | the whole one-hunk diff                                      |
| `notes.json`         | `api …/merge_requests/7/notes?per_page=100&page=1`    | one MR comment and one `DiffNote`                            |
| `discussions.json`   | `api …/merge_requests/7/discussions?…&page=1`         | the same two notes as one unresolved and one resolved thread |
| `expected-hunks.tsv` | —                                                     | the `hunks.tsv` the fetch must index                         |
| `expected-argvs.txt` | —                                                     | the exact `glab` call sequence, in order, fetch then post    |

`MR.json` also answers `post`'s live-SHA read (`api
projects/acme%2Fapp/merge_requests/7`), so the dry run sees the same head it
fetched. The head and base SHAs are literals the eval pins; the base `bbb…`
is the commit the harness seeds through `git replace`, so the controls gate
resolves against the sandbox checkout.

Bodies are formatted JSON, not the one-line wire bytes `glab` may print: the
fixtures are graded by the parsed result, never by payload shape.
