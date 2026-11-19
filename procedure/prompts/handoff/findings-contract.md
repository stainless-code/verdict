## Findings contract

Write `<bundle>/review.json` FIRST with `{"schema_version":"1","findings":[]}`, then update that same file as you work. Write no other file in the bundle.
Every suspected defect becomes one finding with `path`, `line`, `severity`, `claim`, `quote`, and `confidence`. `severity` is `critical|important|informational`; `confidence` is `high|medium|low`. `claim` names the defect and its impact; `quote` is the exact code at `path:line`. Report every suspicion with its confidence — filtering is verdict's job, not yours. Do not delete a finding because tests pass and do not write praise in `claim`.
There is no `approved`, `body`, or `comments` field. Optional keys are `spec_checks`, `receipts`, and `summary`. Read the file before you finish: it must be valid JSON.
