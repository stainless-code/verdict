## Findings contract

An isolated finder follows the directive's output path; only the coordinator writes the aggregate `review.json` with version `2`.

Write `<bundle>/review.json` FIRST with `{"schema_version":"1","findings":[]}`, then update that same file as you work. Write no other file in the bundle.
Every suspected defect becomes one finding with `path`, `line`, `severity`, `claim`, `quote`, and `confidence`. `severity` is `critical|important|informational`; `confidence` is `high|medium|low`. `claim` names the defect and its impact; `quote` is the exact line at `path:line` — the characters that line carries, not a paraphrase. A quote spanning several lines sets `start_line` and carries one quoted line per range line. Report every suspicion with its confidence — filtering is verdict's job, not yours. Do not delete a finding because tests pass and do not write praise in `claim`.
A review whose findings are all below the approval tier must also show it read the change: `receipts[]` holds one `{"hunk":"<the row hunks.tsv prints for it>","quote":"<one line that hunk carries>"}` per changed hunk.
There is no `approved`, `body`, or `comments` field. The other optional keys are `spec_checks` and `summary` (one plain-text paragraph — verdict renders the review body). When you finish, set `summary`; a review with no findings must have a `summary`. Read the file before you finish: it must be valid JSON.
`verdict record --dry-run` names every quote, receipt and spec gap it finds; run it, fix what it names, and run it again.
