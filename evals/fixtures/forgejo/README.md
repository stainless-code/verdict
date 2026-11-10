# Forgejo PR fixtures (hermetic: no network at all)

Forgejo has no CLI: `fetch --provider forgejo` shells `curl` with the API
token and the endpoint URLs built from `FORGEJO_HOST`. These payloads answer
those calls. `evals/cases/forgejo-fetch.sh` registers each argv through
`evals/lib/hoststub.sh`.

The host is `https://forgejo.invalid` on purpose — `.invalid` is reserved
(RFC 2606) and never resolves, so a call that escaped the stub could not
return the bundle the eval asserts. That is the zero-network proof for this
provider, not a promise.

| File                 | `curl` argv (after `-sf -H "Authorization: token …"`)   | Payload                                        |
| -------------------- | ------------------------------------------------------- | ---------------------------------------------- |
| `PR.json`            | `https://forgejo.invalid/api/v1/repos/acme/app/pulls/7` | PR metadata: head `aaaa…`, base `bbb…`, `open` |
| `full.diff`          | `…/pulls/7.diff`                                        | the whole one-hunk diff                        |
| `files.json`         | `…/pulls/7/files?limit=50&page=1`                       | one changed file (`notes.txt`, +1/−1)          |
| `reviews.json`       | `…/pulls/7/reviews?limit=50&page=1`                     | one review, empty body                         |
| `comments.json`      | `…/pulls/7/reviews/301/comments`                        | one inline comment on `notes.txt:2`            |
| `expected-hunks.tsv` | —                                                       | the `hunks.tsv` the fetch must index           |
| `expected-argvs.txt` | —                                                       | the exact `curl` call sequence, in order       |

The base `bbb…` is the commit the harness seeds through `git replace`, so the
controls gate resolves against the sandbox checkout. Bodies are formatted
JSON, not the wire bytes Forgejo prints: the fixtures are graded by the
parsed result, never by payload shape.
