"@stainless-code/verdict": minor
---

The shell is gone: the `verdict` binary is the only front door for all
eighteen verbs. Every eval case invokes the built binary via `$EVAL_CLI`
(`make evals` builds `bin/verdict` first; the `VERDICT_IMPL` switch and the
`missing-go-binary` refusal retire with the shell), the retired parity and
differential suites convert to checked-in Go golden files (record,
readparity, triage, scaffold, fetch cases plus the 626-command verify gate
battery, all regenerable with `-update`), and `scripts/review.sh` no longer
ships (the tarball contract fails on a stale manifest). Minor, not patch:
usage errors, re-enter lines, and refetch hints now name `verdict`, and
`install.sh`'s doctor gate runs the checkout binary.
