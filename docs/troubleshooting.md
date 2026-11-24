# Refusal codes

Every refusal the binary can emit carries a machine `error.code` and a human
`try:` hint. This page lists each code with its cause and its fix. The entries
are kept in sync with `internal/core/refusalcodes.go` by
`TestTroubleshootingCoversRefusalCodes`: a code with no entry here fails the
build, and an entry naming a retired code fails it too.

## `adopt-failed`

Cause: merging the local bundle into the fetched PR bundle failed (the message names the write or the step that errored).

Fix: address the error named above and re-run `verdict adopt`.

## `agent-failed`

Cause: the launched agent exited non-zero.

Fix: read the recorded tail in `.verdict/state/<bundle>/launcher.json`, fix the invocation (the agent's own flags go after `--`), then re-run `verdict run`.

## `agent-mismatch`

Cause: the binary resolved for a row with a binary-name clash failed its identity probe (non-zero exit, timeout, or missing match text).

Fix: define the agent in settings.json `agents` with an absolute `binary` path, or re-run `verdict run` with `--agent` for the one you meant.

## `agent-missing`

Cause: the named agent binary is not on `PATH`.

Fix: install the agent CLI (or put it on `PATH`), or re-run `verdict run --agent <name>` with one you have.

## `agent-record-failed`

Cause: the launcher could not write its provenance record.

Fix: make the bundle directory writable (or re-fetch the bundle), then re-run `verdict run`.

## `agent-spawn-failed`

Cause: the agent binary resolved but could not be started.

Fix: check the binary runs from your shell (permissions, shebang), then re-run `verdict run`.

## `agent-timeout`

Cause: the watchdog killed the agent after `--timeout` seconds.

Fix: re-run `verdict run` with a larger `--timeout`, or pass the agent's faster flags after `--`.

## `already-adopted`

Cause: the target bundle already carries `findings.json` — `adopt` merges once into a fresh fetch.

Fix: `post` this bundle, or re-fetch for a fresh bundle and `adopt` into that.

## `already-learned`

Cause: the rule text is already learned.

Fix: cite the existing rule id instead of learning it twice.

## `already-posted`

Cause: an older version used a completed local receipt to refuse a second post. Current `post` compares live remote content and uses `duplicate-payload` only when nothing is missing.

Fix: re-run with `--force` to override, or `fetch --base-sha <posted-head>` for the delta and review that instead — re-fetch if the earlier review was deleted.

## `already-retired`

Cause: the rule is already retired.

Fix: check the rule status with `learn --status`, then re-run (`learn --restore` brings it back with a `--reason`).

## `already-reviewed-patch`

Cause: an older version refused publication based on a same-patch local receipt. Current `post` records a new-head re-arm and compares live remote content instead.

Fix: re-run with `--force` to post the same change again, or fetch the delta/base for a new review.

## `ambiguous-patch-match`

Cause: `--from` matched more than one local bundle.

Fix: pass the exact `--from <local-slug>` named above.

## `ambiguous-sig`

Cause: the sig prefix matched more than one finding.

Fix: copy the full sig prefix from `report` output.

## `approve-blocked`

Cause: a binding `APPROVE` is refused while findings stand open (triage has not cleared them).

Fix: triage the findings or re-run with `--force`.

## `auth-missing`

Cause: the credential the host call needs is not exported.

Fix: export the credential named above, then re-run.

## `backtest-failed`

Cause: a git read failed while mining (git is missing, or the checkout is unreadable).

Fix: confirm `git --version` works in the checkout, then re-run.

## `backtest-row-stale`

Cause: a scored run's `prs/<NNN>/replay.json` is unreadable, names another run, repeats a candidate another row already scored, or its bundle's `findings.json` is missing or unreadable.

Fix: delete the named `prs/<NNN>/` directory, then replay the run so it records a fresh row from the same candidate.

## `backtest-run-stale`

Cause: the run's `mine.json` predates the seed `signal` field, is unreadable, or no longer carries a PR a recorded review names.

Fix: re-mine the run in place with the same `--run` and window, then replay and score it again. Deleting the run directory is never required.

## `bad-adjudication`

Cause: an adjudication record is not a versioned per-run file the report can apply: unreadable or multi-document JSON, a schema_version other than `"1"`, an unknown key or verdict, a missing case/sample/sig/adjudicator, a rationale under 30 characters, a verdict that raises a figure without quoting the code in backticks, a `matches-ground-truth` rationale that does not quote the ground-truth note, a bug index the case does not have, a bug the strict run already matched, a finding outside the bug's window, or one finding or bug adjudicated twice.

Fix: correct the named entry in the record and re-run `bench/runner -render <report> -adjudication <record>`. The record is separate from `case.json`, which adjudication never edits.

## `bad-approve`

Cause: `--approve` is not a boolean.

Fix: pass `--approve=true|false`.

## `bad-at`

Cause: `--at` names a revision the checkout cannot resolve to a tree or commit, or (for `learn --incident`) a timestamp that is not RFC 3339.

Fix: pass a ref or tree SHA that exists in the source checkout, then re-run. An incident attach takes an RFC 3339 timestamp (`2026-11-01T09:00:00Z`).

## `bad-attribution`

Cause: `--attribution` names an unknown tier.

Fix: re-run with one of `off|product|full`.

## `bad-base`

Cause: the checkout shares no merge-base with `--base`.

Fix: pass a `--base` reachable from this checkout, or run in the right repository.

## `bad-baseline`

Cause: `GET /v1/roots/{root}/bundles/{bundle}/compare` received no `baseline` query parameter, a second query parameter beside it, or a baseline that cannot name a bundle inside the route's own served root — an absolute path, a `..` segment, a path separator, or a path that resolves outside the root.

Fix: pass exactly one `baseline` query parameter naming another bundle in the same root, as a bare bundle name (`?baseline=local-previous`, `?baseline=1234`). A name this root does not hold refuses `missing-compare-bundle` instead.

## `bad-budget`

Cause: `REVIEW_BUDGET_USD` (or the typed `budget_usd` default) is not above 0, or the scaffolded config fails validation.

Fix: export a positive `REVIEW_BUDGET_USD` (or set a positive `budget_usd`), then re-run.

## `bad-by`

Cause: a `serve` cost or precision route received an unsupported `by` dimension.

Fix: use `repo|week|model` for cost or `category|tier` for precision.

## `bad-category`

Cause: `report --category` names an unknown category.

Fix: re-run with one of the valid values named above.

## `bad-color`

Cause: `--color` is not a known mode.

Fix: pass `--color auto|always|never`.

## `bad-compare`

Cause: `report --compare` names the bundle being reported, a baseline from a different repository or review host, an explicitly empty value, or a value that is neither a bundle directory nor a valid bundle name.

Fix: pass a different baseline — a bundle directory, or a PR or `local-<name>` under the same `.verdict/state` — with `--compare <bundle-dir|pr|slug>`.

## `bad-config`

Cause: the repository configuration (or a fragment of it) is malformed or semantically invalid — the message names the exact key.

Fix: fix the named key (see `docs/manual.md` for the shape), then re-run.

## `bad-config-credentials`

Cause: the configuration file carries a credential key (credentials never live there).

Fix: remove the credential key named above and export it as an environment credential instead, then re-run.

## `bad-flags`

Cause: the flag combination is not valid (e.g. `--range`/`--base`/`--staged`/`--stash` are mutually exclusive, `fix` got `--sig` with `--all` or a batch flag (`--min-severity`, `--verify-cmd`, `--paths`) without `--all`, or `learn` got no mode), or a bundle positional is not one path element (`../other`, `a/b`, `a\b`, `.`, `..`, and an empty name all refuse this way, since a bundle name is a PR number or a fetch-local slug, never a path). `run --stage` joins this list: it renders one stage only, so it wants both `--print` and `--recipe`, an index inside the plan's stages, and no pass-through `--model`/`-m` when that stage declares a hint.

Fix: re-run with a valid combination — the message names the clash.

## `bad-from`

Cause: `adopt` got no (or a bad) `--from` local slug, or `ingest --from` named a source format this build cannot read.

Fix: for `adopt`, pass `--from <local-slug>` pointing at a `.verdict/state/local-*` directory carrying `findings.json`. For `ingest`, pass `--from sarif <file|->`; that is the one format it reads.

## `bad-glob`

Cause: a bundle-listing pattern is not a portable repository-relative glob — it is absolute, escapes with `..`, carries an empty segment, or carries a backslash.

Fix: pass a portable `/`-separated glob (for example `local-*`), or omit the pattern to list every bundle.

## `bad-group-validation`

Cause: `verify --group` is missing `--group` or `--command`, or the group id cannot name a confined receipt file.

Fix: pass both `--group <id>` and `--command <cmd>` with an id matching `[a-z0-9-]` (max 64 chars).

## `bad-id`

Cause: `--id` breaks the id shape.

Fix: pass an `--id` matching `^[a-z0-9][a-z0-9-]{0,64}$`, or omit `--id`.

## `bad-ignore`

Cause: the trusted exclusion control is unreadable or malformed.

Fix: keep `.verdict/ignore` UTF-8, under 64 KiB and 1,000 patterns. Use one shell glob per line, `#` comments and `!` to re-include, then re-fetch.

## `bad-judgment`

Cause: the `calibrate --probe` judge file is missing or unreadable.

Fix: write one `{"sig": ..., "agent_verdict": ...}` object per line (or a JSON array of them), then re-run.

## `bad-lock-source`

Cause: a `.verdict/verdict.lock` row's `source` is neither a clean `dir:` path nor one of the three allowlisted git remotes pinned to a 40-hex sha. A hand-written row with a one-slash scheme, an upper-case scheme, a `%`-escaped password, or https userinfo refuses here; the message prints the fixed `<invalid source>` placeholder, never the row.

Fix: re-run `verdict pack add <dir|git-url>` from the pack's source so the lock records a valid source, then commit the lock.

## `bad-max-bytes`

Cause: `REVIEW_MAX_BYTES` is not a positive byte count.

Fix: export a positive byte count and re-run.

## `bad-min-severity`

Cause: the post severity floor is not a supported value.

Fix: pass `--min-severity critical|important|informational|off`, then re-run.

## `bad-override`

Cause: the named overview/walkthrough override file is unreadable.

Fix: fix its permissions (or remove it), then re-run.

## `bad-pack`

Cause: a lens pack manifest is malformed, a key is spelled in any case but the published lowercase form, two packs in one layer share an id, or a named lens/rule body is missing, empty, or stem-mismatched.

Fix: fix `pack.json` against `schemas/pack.schema.json` (exact lowercase keys; every lens id equals `lenses/<id>.md` and every rule id `rules/<id>.md`), then re-run.

## `bad-pack-source`

Cause: `verdict pack add` named a remote-shaped source the git allowlist rejects. Only `https://host/path` with no userinfo, `ssh://user@host/path`, and `user@host:path` are git sources; a one-slash scheme, `http://`, `git+https://`, a `%`-escaped password, a token-only https user, a second `@`, a query or fragment (`?private_token=…`, `#…`), or userinfo on a bracketed IPv6 host refuses before any git call. The message prints `<invalid source>`, never the bytes supplied.

Fix: use one of the three forms with a bare ssh user and a public https URL, and put no token in the URL, ref, or path — the source is recorded in the committed lock. Use an ssh key instead. Then re-run.

## `bad-panel`

Cause: `verification/verdicts.json` has malformed results, duplicate `(sig, model)` pairs, unknown or ambiguous sigs, or evidence outside 10–500 characters.

Fix: use schema version `1`, unique full signatures, named models, and verdicts `confirmed`, `refuted`, or `insufficient` with evidence. Correct `verification/verdicts.json` and retry.

## `bad-path`

Cause: `pack check` got a path that is not a repository-relative slash path (absolute, `..`, a backslash, a drive-letter spelling, or a control character such as a stray NUL).

Fix: pass the path as git spells it, for example `verdict pack check internal/core/pack/pack.go`.

## `bad-preset`

Cause: `--preset` is not a known preset.

Fix: pass `--preset chill|standard|strict`.

## `bad-provider`

Cause: `--provider` names an unknown host.

Fix: re-run with one of `github|gitlab|gitcafe|forgejo|bitbucket|azure` (default `github`).

## `bad-range`

Cause: the `--range` base ref cannot be resolved (or has no merge-base).

Fix: pass a resolvable base ref, then re-run.

## `bad-reason`

Cause: `--retire`/`--restore` lacks a human veto on the record.

Fix: add `--reason` with 30+ chars.

## `bad-recipe`

Cause: the base `.verdict/recipes.json` document or the selected entry is invalid — an unknown key, a duplicate recipe key, a bad slug, semver, model hint, description length, or cap, a non-positive `budget_usd`, a duplicate lens id, empty `lenses`, or a missing `synthesize` (the message names the JSON path and the key).

Fix: fix the key named above in `.verdict/recipes.json`, commit it at the base, then re-fetch.

## `bad-requirement`

Cause: a requirement clause is invalid or unsatisfiable — the message names the key.

Fix: fix the clause named above and re-run.

## `bad-review-input`

Cause: the agent input is malformed (the message names the field).

Fix: fix the malformed agent input named above; v2 `review.json` wants `{schema_version: "1", findings: [...]}`.

## `bad-review-schema`

Cause: `review.json` does not stamp `"schema_version": "1"`.

Fix: set `"schema_version": "1"` in `review.json`.

## `bad-rigor`

Cause: `--rigor` is not a known level.

Fix: re-run with one of `said|pointed|walked|ran|reproduced`.

## `bad-rigor-policy`

Cause: `--require-rigor` is not `<critical|important|informational>=<rung>`, or `rigor_policy` in the trusted-base `.verdict/config.json` names a mode or rung outside the typed vocabulary.

Fix: pass `--require-rigor critical=walked` (repeatable, one tier per flag), or fix `rigor_policy` and re-fetch so the base snapshot carries it.

## `bad-root`

Cause: `serve --root` names a path that is missing, not a directory, or unreadable.

Fix: pass `--root <existing-directory>`, or start `verdict serve` inside a checkout with no `--root`.

## `bad-rule`

Cause: `learn --rule` got an empty rule.

Fix: pass the one-line rule text to `--rule`.

## `bad-sarif`

Cause: the document is not valid UTF-8 JSON conforming to SARIF 2.1.0 — a non-object root, trailing data, a `version` other than `"2.1.0"`, a missing or non-array `runs`, a run without `tool.driver.name`, a result without a message, or a `ruleIndex` outside the driver's rules. A single result with an unsafe URI does not refuse the file: that result is skipped (`path-escapes-repo`) instead.

Fix: supply valid SARIF 2.1.0 JSON (`"version": "2.1.0"`, a `runs[]` array, one driver name and message per run/result), then re-run `verdict ingest --from sarif <file|->`. Read `data.skipped[]` to see which results the pinned diff could not anchor and why.

## `bad-scan`

Cause: a `fetch-local --scan <path>` operand is not reviewable: it does not exist, it resolves outside the repository root (`../` or an absolute path), it is a symlink or a link sits between the root and the operand (a scan never follows a link, whatever it points at, and components are validated before any `..` collapses them), it names a managed `.git` or `.verdict` entry at any depth (or a case variant that aliases one on a case-insensitive volume), a file could not be read, or the scanned tree would synthesize more diff bytes than a bundle read can load.

Fix: pass a file or directory inside the repository (a relative path resolves from the current directory) and name the real path rather than a link. Narrow `--scan` to a subdirectory when the tree is too large for one bundle, and re-run on a quiet tree if a file changed while it was being read.

## `bad-scope`

Cause: `--scope` is empty, malformed, or belongs to a different mode (or the scope config key is invalid). Learning proposals also refuse unsafe cited paths or comment identifiers.

Fix: pass a comma-separated glob list (e.g. `src/*.ts`) together with `--rule`, or fix the named scope key. For learnings, use a clean repo-relative cited path of at most 4096 bytes — no control runes or line breaks (U+2028/U+2029 included), parent components, absolute paths, backslashes, colons, commas or glob syntax. Comment IDs allow 1–256 ASCII letters, digits, underscores, hyphens or equals signs. Unsafe metadata is refused before proposing or accepting.

## `bad-settings`

Cause: the settings file is malformed, carries an unknown placeholder, or names an invalid operator-defined agent under `agents` — an unknown key, a blank or relative binary, a name outside `[a-z0-9][a-z0-9-]*`, or a collision with a shipped agent.

Fix: keep only the ten accepted top-level keys (including `$schema`): `$schema/output/layout/color/publish/review/spend/privacy/packs/agents`. Fix the named `$.agents.*` field (or choose a non-colliding name), then re-run. Agent definitions belong only in the prefs file, never in `.verdict/config.json` (see `docs/manual.md`).

## `bad-settings-credentials`

Cause: the settings file carries a credential key.

Fix: remove the credential key from the settings file (credentials never live there), then re-run.

## `bad-sig`

Cause: the `--sig` prefix matched nothing actionable.

Fix: list open inline sigs with `report --status open --kind inline`, then re-run with the full prefix.

## `bad-since`

Cause: `backtest mine --since` is missing, is a bare month (`18m`), does not resolve, or names a commit that is not an ancestor of `--until`.

Fix: pass `--since <90d|12w|18mo|2y|ref>` with a unit of `d`, `w`, `mo`, or `y`, or a ref that precedes `--until`.

## `bad-slug`

Cause: the local bundle name is unusable (it sanitizes to nothing, looks like a PR number, or already carries the `local-` prefix).

Fix: pass a usable bundle name — `fetch-local` adds `local-` itself.

## `bad-stash`

Cause: the named `stash@{N}` does not exist.

Fix: pick one from `git stash list` and re-run with `--stash <N>`.

## `bad-suppression`

Cause: a suppression has an invalid source or locator, no matching exclusion receipt, or an agent reason shorter than 30 characters.

Fix: use source `agent` with a 30-character reason, or `exclusion` with the path and rule id from `excluded.json`, then re-run.

## `bad-target`

Cause: `adopt --from` points at a PR bundle instead of a local one.

Fix: point `--from` at a `.verdict/state/local-*` directory.

## `bad-timeout`

Cause: `--timeout` is not a positive number of seconds.

Fix: pass `--timeout <positive-seconds>` (default 300).

## `bad-triage-status`

Cause: `--status` is not a known triage state.

Fix: re-run with one of `open|addressed|wont-fix|false-positive|deferred`.

## `bad-until`

Cause: `backtest mine --until` does not resolve, or names a commit that is not an ancestor of `HEAD`.

Fix: pass a ref in `HEAD`'s history (the default is `HEAD`), then re-run.

## `bad-usage`

Cause: the `VERDICT_USAGE` record does not match `schemas/usage.schema.json`.

Fix: fix the usage record named above to match the schema, then re-run.

## `bad-validation`

Cause: `validation.json` is malformed: an unknown key, trailing data after the object, a verdict outside `CONFIRMED|REJECTED|DOWNGRADED`, a reason outside 10-500 characters, a `DOWNGRADED` pair that is not exactly one rank or that names a `from_severity` the finding does not carry, a duplicate sig, or a sig that matches no single finding.

Fix: rewrite the artifact to match `schemas/validation.schema.json`, or delete it to post without a validation pass.

## `bind-required`

Cause: the run policy sets `require_bind` and this binding approval lacks `--bind`.

Fix: pass `--bind` to bind this approval (binding downgrades never satisfy a required bind, and `--force` cannot bypass it).

## `budget-exceeded`

Cause: the projected run cost exceeds the budget.

Fix: re-run with `--consent-over-budget` once the spend is approved, or raise `REVIEW_BUDGET_USD`/the typed `budget_usd` default.

## `budget-unknown`

Cause: no usable cost projection exists and the operator settings require a known estimate.

Fix: re-run with `--consent-over-budget` to consent to the blind run (or relax `require_known_estimate`).

## `bundle-ambiguous`

Cause: a served bundle name resolves to a different directory than the discovered one — the bare name shadows an existing `local-<name>` bundle with no `PR.json`.

Fix: address the local bundle explicitly as `local-<name>`, or restore the PR bundle's `PR.json` so the bare name resolves to it.

## `calibrate-failed`

Cause: `calibrate --probe` could not write its grades file.

Fix: make the bundle directory writable, then re-run.

## `canceled`

Cause: the run was canceled (`SIGINT`/`SIGTERM`) before the verb completed.

Fix: re-run the same command (a cancel exits 3, never 0).

## `cannot-resume`

Cause: `run --resume` found no interrupted two-finder execution to continue: the bundle has no `launcher.json`, its record predates the resume fingerprint, its `run.json` lost the captured execution reference, or the resolved shape is one finder.

Fix: re-run `verdict run` without `--resume` and pass `--finders 2` explicitly — a fresh two-finder execution retires the interrupted one to `x-superseded/` — then pass the same `--finders 2` when you `--resume` that execution.

## `chunk-write-failed`

Cause: a chunk diff went missing while slicing `full.diff`.

Fix: re-run the fetch.

## `config-no-bundle`

Cause: `config --explain` found no run bundle to render (a never-fetched run has nothing to explain).

Fix: fetch the run first, then explain it (`config --preview` renders the no-policy layers without a bundle).

## `config-too-large`

Cause: the configuration (or a control file) is over its parser byte cap.

Fix: trim the file named above under its cap, then re-run.

## `conflicted-tree`

Cause: the checkout carries unresolved merge conflicts.

Fix: resolve the conflicts (or abort the merge), then re-run.

## `context-invalid`

Cause: a `fetch` or `fetch-local` `--context` INPUT could not be read or was
refused: the path is missing, a symlink or special file, too deeply nested, or
past the 64-file or 1 MiB limit, or the named file is not Markdown, JSON or
`.txt`. Bundle write failures never carry this code. The message names the
operand as typed and, for a relative path, its absolute resolution.

Fix: check each `--context` path — relative paths resolve against the current
directory — then hand over regular supported files under the caps and re-run.

## `controls-stale`

Cause: the review controls changed under the bundle.

Fix: re-fetch the bundle (controls changed under it), then re-run.

## `controls-too-large`

Cause: the named control file is over its byte cap.

Fix: trim the named control file under its cap, commit it at base, then re-fetch.

## `controls-unavailable`

Cause: the checkout lacks the base commit the controls are read from (shallow clone).

Fix: check out the PR base (`actions/checkout` with `fetch-depth: 0`, or `git fetch --depth=1 origin <base>`), then re-fetch; `fetch --allow-head-controls` is the explicit opt-out.

## `corpus-invalid`

Cause: the benchmark case manifest is invalid, or a real bundle's reviewed `full.diff` is missing, empty, unreadable, or not a unified diff (no `@@` hunk header and no `---`/`+++` pair) — the message names the file and the reason.

Fix: for a manifest, fix the file named above and re-run. For a `full.diff` that cannot grade, rebuild the bundle (`fetch-local` or `fetch` for a fetched bundle, `backtest replay` for a replay bundle, which the message names), then re-run the verb. An empty or prose diff is a failed capture, not a valid review of no changes.

## `corrupt-compare-bundle`

Cause: the `--compare` baseline's `findings.json` is present but unreadable, is not a findings array, or carries an invalid finding entry.

Fix: re-run `record` or `post` on the baseline bundle, or restore its `findings.json` from backup, then re-run `report --compare`.

## `corrupt-store`

Cause: `findings.json` (or the knowledge-base store, or the incident ledger `.verdict/incidents.jsonl`) is present but unreadable, or a bundle home (`.verdict/state`) is not a directory so a listing cannot read it.

Fix: re-run `post`/`record` to rebuild the store, or restore `findings.json` (or the incident ledger) from backup, then re-run.

## `cost-entry-contradicts-panel`

Cause: a cost entry's name or order contradicts the panel that ran.

Fix: pass the panel in the order it ran (synthesizer first), or fix the entry's name, then re-run.

## `cost-entry-unnamed`

Cause: a cost entry names no model from the panel.

Fix: pass every model that ran: `--cost-names=<lead>,<lens-1>,<lens-2>`.

## `cost-models-not-array`

Cause: the cost record's `models` is not an array of named entries.

Fix: rewrite `models` as an array of named entries, then re-run.

## `cost-panel-unresolved`

Cause: the panel that ran cannot be resolved from the cost record.

Fix: pass the panel that ran, in order: `--cost-names=<lead>,<lens-1>,<lens-2>`.

## `cost-unreadable`

Cause: the cost record cannot be read.

Fix: fix or delete the cost record named above, then re-run.

## `coverage-incomplete`

Cause: `review.json` does not attest coverage for every changed file.

Fix: stamp `review.json` `"coverage": [{"file": "...", "ranges": "1-240"}]` for every changed file, or re-run `next --coverage` to see the gaps, then post again.

## `coverage-stale`

Cause: the tracked working tree changed after `fetch-local`, or the dirty bundle
has no verifiable tracked-tree fingerprint.

Fix: re-run `fetch-local` to refresh `.verdict/state/local-<name>/`, review the
new bundle, then record again. Coverage from the previous snapshot cannot
certify the current tree.

## `diff-failed`

Cause: `git diff` failed for the requested range.

Fix: fix the range so both refs resolve, then re-run `fetch-local`.

## `diff-too-large`

Cause: the fetched diff is over the cap, measured after exclusions.

Fix: split the range (`--range`/`--base`/`--base-sha`) or exclude files with `--ignore <glob>`, then re-fetch.
The `next:` line names the two exclusions that fit the review: `--ignore <glob>`
(repeatable on `fetch` and `fetch-local`), or a `.verdict/ignore` pattern
committed on the base revision — the ignore file is read from the base snapshot,
so a rule added on the head never applies to its own review. Paths after `--` on
`fetch-local` are git pathspecs: they narrow the review to the matching files,
they never exclude.

## `download-failed`

Cause: the release download failed (bad version, no download directory, or a fetch error — the message says which).

Fix: pass a concrete `X.Y.Z` (or `vX.Y.Z`) version with a writable download dir, then re-run `upgrade`.

## `drafts-invalid`

Cause: `drain.replies.json` names an unknown thread or comment, repeats a thread, carries a bad disposition or body, or has an unknown key; the message names the site.

Fix: edit the named site to match the plan, then re-run `drain --agent <name>`. With no drafts file at all, run the agent first.

## `drafts-stale`

Cause: the drafts `plan_digest` differs from the plan's; the feed or the pending list moved after drafting.

Fix: re-run `drain` for a fresh plan, then draft replies against its digest.

## `drain-failed`

Cause: the bundle, `threads.json`, the login, or the reply ledger could not be read, or the refreshed feed is truncated.

Fix: refresh threads and confirm host auth, then re-run `drain`.

## `drain-incomplete`

Cause: at least one `drain --post` host write failed; `drain.json` names the threads in `failed[]`. Replies that landed are already in `replies.json`.

Fix: re-run the same `drain --post` command. The ledger skips replies already posted and only the failed threads are retried; check the host's own error in `failed[]` first.

## `duplicate-payload`

Cause: every rendered summary part and comment is present in the live remote lists for this reviewed head.

Fix: re-fetch to refresh remote evidence, or use `--force` to deliberately republish. Retry a partially published review directly; `post` refreshes live remote evidence and writes only missing content.

## `fetch-failed`

Cause: the provider CLI call failed, or verdict could not write a bundle
context artifact.

Fix: run `gh auth status` and confirm the repo/PR spelling for provider failures.
For a context write failure, make the bundle directory writable and re-run.

## `filtered-out`

Cause: `ready --filter` found a trigger predicate in `.verdict/config.json` at the PR base that the PR does not match; the refusal names the failing predicate.

Fix: review the PR without `--filter`, or change the trigger at the base branch and re-fetch. The decision is recorded in `run.json`.

## `fix-ceiling`

Cause: the fix loop hit its pass ceiling without clearing the sig.

Fix: triage the sig or split the fix — the 3-pass ceiling is hard-coded.

## `fix-failed`

Cause: the fix ledger or the patch output could not be written.

Fix: make the bundle directory writable, then re-run `fix`.

## `fix-patch-secret`

Cause: the worktree diff `fix` was about to write to `fix.patch` carries a
secret-shaped value — a private key, an AWS key id, a GitHub, Slack, Stripe,
Google, Anthropic, or OpenAI token. The bundle keeps that patch, and every later
agent run reads it.

Fix: remove the value from the worktree and rotate it if it was real, then
re-run `fix`. The pass was not recorded and `fix.patch` was not written, so no
pass was spent.

## `fixtures-failed`

Cause: `backtest fixtures` could not create or atomically replace
`.verdict/backtest/fixtures/report.json`, usually because the state directory
is not writable.

Fix: make `.verdict/` writable, then re-run `backtest fixtures`. The scores
are recomputed; nothing is sent to a host.

## `fixtures-invalid`

Cause: `backtest fixtures` could not read `bench/quality/fixtures.json`, or the
catalog is malformed — a missing id, a broken base/head or context pin, an
empty rule, or a pattern that does not compile.

Fix: check the `--fixture` id against the catalog, or repair
`bench/quality/fixtures.json`, then re-run `backtest fixtures`. The recordings
were not scored.

## `fixtures-recording-invalid`

Cause: a recording under `bench/quality/<fixture>/recordings/` or an explicit
`--recordings` root has no readable `meta.json`, names another fixture or an
unknown source file, or names a fixture the catalog does not carry.

Fix: repair or remove that recording directory, then re-run `backtest fixtures`.
A recording with `gate_passed: null` is valid: it is counted as an unknown gate
outcome, not a pass.

## `head-mismatch`

Cause: the bundle carries no reviewed head, or the checked-out head is not the reviewed head.

Fix: check out the reviewed commit (or re-fetch the bundle), then re-run validation.

## `html-not-export`

Cause: the HTML export destination is a file or a non-empty directory without
Verdict's export file set and static-page marker.

Fix: choose a new or empty directory, or a previous HTML export directory.

## `html-symlink`

Cause: the HTML export destination is a symlink.

Fix: choose a directory path that is not a symlink. Verdict does not follow the
export destination link.

## `id-taken`

Cause: `--id` is already taken.

Fix: pass a different `--id`.

## `ignore-too-large`

Cause: the trusted `.verdict/ignore` exceeds 64 KiB or 1,000 patterns.

Fix: reduce the file below both limits, then re-fetch.

## `index-capped`

Cause: the tree or shard store crossed the index caps (20,000 files, a 96 MiB
tree, or a 256 MiB shard cache); the message names the measured number.

Fix: narrow `--from`/`--at` to a smaller tree, or raise the caps deliberately.

## `index-corrupt`

Cause: a stored tree or shard is truncated or invalid.

Fix: delete `.verdict/index` and rebuild with `verdict index build`.

## `index-failed`

Cause: an index store read or write failed, or git could not be run.

Fix: confirm git works in the checkout, make `.verdict/index` writable, then
re-run.

## `ingest-failed`

Cause: reading the SARIF input or writing the updated `findings.json` failed (for example, a symlinked or unreadable SARIF file, or a non-writable bundle directory).

Fix: confirm the `--from sarif <file|->` path is a readable regular file (not a symlink) or valid stdin stream and that `.verdict/state/<bundle>` is writable, then re-run `verdict ingest`.

## `init-failed`

Cause: `init` could not create `.verdict` or write a control file (storage not writable).

Fix: make the directory writable, then re-run `init`.

## `internal`

Cause: an unexpected internal error — the message carries the detail.

Fix: re-run the same command; if it repeats, report it with the full output.

## `issue-invalid`

Cause: a `fetch` `--issue` operand is not one of the three accepted shapes — a
GitHub `owner/repo#12` or issue URL, or a bare `#12`. The message names the
operand and what was wrong: whitespace, an unrecognised reference, a missing or
zero number, or a bare `#12` on a non-GitHub provider (there is no repository to
join the number to there).

Fix: pass `--issue owner/repo#12`, `--issue '#12'` (GitHub only), or
`--issue https://github.com/owner/repo/issues/12`, then re-run. A bare number on
another host refuses because the reference cannot name a repo; spell it out as
`owner/repo#12`, which reads `gh` on any provider. An issue in another tracker
(Linear, Jira, ...) is not an issue reference: write that tracker's text to a
file and pass it as `--spec <file>`.

## `learn-failed`

Cause: `learn` could not write the knowledge-base store (or create `.verdict`).

Fix: make `.verdict` writable (or restore the knowledge-base store from backup), then re-run.

## `ledger-too-large`

Cause: the attach would have pushed `.verdict/incidents.jsonl` past the 8 MiB the reader opens, so it wrote nothing: a ledger that size can never be read again, which would block every later attach and `trend --incidents`.

Fix: trim or archive rows from `.verdict/incidents.jsonl` (it is a plain JSONL file you own), then re-run the attach.

## `manager-failed`

Cause: the package manager's update command failed — the message names its exit or error.

Fix: fix the manager invocation named above (the manager owns the install), then re-run `upgrade`.

## `manager-missing`

Cause: the channel's package manager is not on `PATH`.

Fix: install it, then re-run the printed update command.

## `mcp-config-backup-failed`

Cause: the existing MCP config could not be backed up.

Fix: check the config path and permissions named above, then re-run.

## `mcp-config-foreign`

Cause: the file already configures the MCP entry differently.

Fix: remove the existing MCP entry by hand, then re-run.

## `mcp-config-no-home`

Cause: neither `HOME` (nor `CODEX_HOME` for codex) is set.

Fix: set `HOME` (or `CODEX_HOME`), then re-run.

## `mcp-config-read-failed`

Cause: the current directory cannot be resolved, or the config path cannot be inspected or read.

Fix: check the config path and permissions named above, then re-run.

## `mcp-config-unknown-agent`

Cause: `mcp config` got no (or an unknown) `--agent`.

Fix: pass `--agent` with one of `claude|cursor|codex|gemini|qwen|windsurf|opencode|goose|copilot`.

## `mcp-config-unmergeable`

Cause: the host is print-only, or the file's TOML shape cannot be merged (the message names the line).

Fix: copy the printed snippet by hand (print-only hosts), or fix the TOML quoting/shape named above and re-run.

## `mcp-config-unparseable`

Cause: the config file's JSON/TOML does not parse.

Fix: fix the JSON/TOML syntax (or move the file aside), then re-run.

## `mcp-config-write-failed`

Cause: the config directory cannot be created, the path is a symlink, or the file cannot be written.

Fix: check the config path and permissions named above, then re-run.

## `missing-bundle`

Cause: the bundle is absent — nothing fetched it yet. For `backtest score`, the run has no `mine.json` (never mined) or no `prs/` records (never replayed).

Fix: run `fetch` first (`verdict fetch <owner>/<repo> <pr>`, or `verdict fetch-local` for a local bundle). For a backtest run, mine it (`verdict backtest mine --since … --run <id>`) and replay it (`verdict backtest replay --run <id> --agent <name>`) before scoring.

## `missing-chunk-coverage`

Cause: the bundle lacks chunk coverage.

Fix: re-fetch the bundle with `--chunk`, then re-synthesize across all chunks and re-run.

## `missing-chunk-snapshot`

Cause: the bundle lacks the chunk hunk snapshot.

Fix: re-fetch the bundle with `--chunk`, then re-synthesize across all chunks and re-run.

## `missing-compare-bundle`

Cause: `report --compare` names no bundle: the directory does not exist, or a PR/local slug has no directory under `.verdict/state`.

Fix: pass an existing bundle directory, or the PR number or `local-<name>` the prior run wrote under `.verdict/state`, then re-run.

## `missing-diff`

Cause: `full.diff` is absent.

Fix: fetch the PR first, then re-run.

## `missing-findings`

Cause: `findings.json` is absent.

Fix: run `post` or `record` first, then re-run.

## `missing-index`

Cause: no tree file exists for the queried tree.

Fix: run `verdict index build` at the revision the refusal names (`--at <ref>`,
or `--from <dir>` for a directory hand-off). The message pairs the ref you
typed with the tree it resolved to, so `no index for HEAD (tree <tree>)` says
which tree is unbuilt.

## `missing-patch`

Cause: `patch.json` is absent — nothing claimed to verify.

Fix: produce the patch claim first, then re-run `verify`.

## `missing-post`

Cause: `post.json` is absent (or carries no review id/head) — nothing posted to calibrate.

Fix: `post` for real first, then re-run `calibrate`.

## `missing-review-file`

Cause: the review file is absent.

Fix: have the agent write `{schema_version: "1", findings: [...]}` to the review file first, then re-run.

## `missing-sarif`

Cause: `ingest` was invoked without a source — no `--from sarif <file|->` (or legacy `--sarif <file|->`) — or with an empty path or stdin stream.

Fix: pass `--from sarif <file>`, or `--from sarif -` (or `--sarif -`) to read stdin, then re-run `verdict ingest`.

## `missing-selection`

Cause: `fix --selected` found no `selected.json` in the bundle — nothing recorded
which sigs a human reacted to.

Fix: re-run the bundle's fetch. Only a GitHub fetch reads reactions, so a
GitLab or Forgejo bundle never carries the file; use `--all` or `--sig` there.

## `missing-sig`

Cause: `show` got no `--sig`.

Fix: re-run with `--sig <sig>` (a short prefix works).

## `missing-source`

Cause: a required record source is absent (the message names it).

Fix: run the fetch/record step that produces it first, then re-run.

## `missing-tool`

Cause: a required external tool is not installed.

Fix: install the tool named above (or put it on `PATH`), then re-run.

## `needs-confirm`

Cause: `init` would overwrite existing control files without confirmation.

Fix: re-run with `--yes` after reviewing the preview.

## `no-open-findings`

Cause: `handoff` projects the open inline findings a `fix` run would take, and
the bundle holds none at or above the severity floor (and under the `--paths`
globs, when given).

Fix: lower `--min-severity` (or pass `off`), widen `--paths`, or read the whole
bundle with `report --status open`.

## `no-patch-match`

Cause: no local bundle matched the patch operation.

Fix: pass `--from <local-slug>`, or re-fetch the target bundle so `identity.json` exists.

## `no-pr-target`

Cause: the verb needs a PR bundle (local bundles never post).

Fix: `fetch` the PR first; local bundles use `record` and `report`.

## `no-reproduction`

Cause: `fix --test` needs a reproduced finding's retained artifact, and the sig carries no reproduction receipt or the retained file cannot be read.

Fix: re-run `record` with a reproduction block so the bundle retains the artifact, then re-run `verdict fix <owner/repo> <local-slug|pr> --test <sig>`.

## `no-review-artifact`

Cause: the agent exited `0` but wrote no `review.json` (or a non-file).

Fix: run the printed command yourself (`--print`) to see why no `review.json` appeared, then re-run.

## `no-run-bundle`

Cause: the bundle has no verified `run.json`.

Fix: re-fetch it before deep validation.

## `no-selected-findings`

Cause: `selected.json` names sigs, and none of them is an open inline finding in
the store — they were addressed, deferred, or re-sigged since the fetch. An
empty `sigs` list lands here too: it selects nothing, so it is never read as
"every finding".

Fix: re-fetch the PR to re-record the current reactions, or triage the sigs
first. `--min-severity` and `--paths` also narrow a selection to nothing, and a
hand-edited bundle whose `sigs` is `[]` needs a re-fetch to name any.

## `no-threads`

Cause: `calibrate` joins live threads — nothing to settle on a local bundle.

Fix: `adopt` and `post` first, then calibrate the PR.

## `no-verify-cmd`

Cause: `patch.json` carries no `verifyCmd` to execute.

Fix: add a `verifyCmd` to the patch claim, then re-run `verify`.

## `not-a-repo`

Cause: the command needs a git worktree and this directory is not one.

Fix: run inside a git worktree, then re-run.

## `not-found`

Cause: the MCP dashboard query named a bundle this served root does not hold.

Fix: list bundles with `query-bundles` first, then re-issue the call with the right bundle name.

## `not-local`

Cause: the verb got the wrong bundle kind (`record` on a GitHub PR, or `fix` on a PR bundle
without `--selected`).

Fix: `fetch-local` for local work first; GitHub PRs use `post`, and local bundles use `record` and `report`.
`fix --selected` is the one batch that reads a PR bundle, because that is where
`selected.json` comes from.

## `not-retired`

Cause: the rule is already active.

Fix: check the rule status with `learn --status`, then re-run.

## `not-verdict-authored`

Cause: the thread is authored by someone (or something) else — only verdict-authored threads resolve.

Fix: resolve only verdict-authored threads (pass the ids from `threads.json`).

## `pack-conflict`

Cause: a higher-layer pack declares an id a lower-layer pack already declares, without naming that pack in `replace`; or `replace` names a pack that is not loaded or is not below the declaring pack.

Fix: add `"replace": ["<lower pack name>"]` to the higher pack's manifest (replacement is downward only), or rename the id. Then re-run.

## `pack-git-failed`

Cause: the hermetic `git clone`, the HEAD resolution, the detached checkout `pack add` runs, the pinned `refs/tags/<tag>` or commit `pack sync` fetches, or the `git merge-base` the trusted-base read runs on a default branch that resolves, could not complete: the remote or the ref was not found, the credential was refused, the network is down, the five-minute clone budget ran out, or the default branch shares no merge base with `HEAD` (a shallow or partial clone). git's own error text is never printed, because it can carry the source URL, its query, or the ref.

Fix: check the source spelling, the `@ref`, your access (an ssh key for an ssh source), and the network, then re-run. For a private remote, use ssh with a key rather than a token in the URL. A merge-base refusal names the branch: fetch the default branch and its history (`git fetch origin <branch>`) so `HEAD` and the branch meet, then re-run.

## `pack-lock-drift`

Cause: `.verdict/verdict.lock` no longer matches the vendored repo pack: its tree hash differs, its pack directory is gone, or its `sig` does not match the entry's name, source, version, and hash (plus `org_path` and `org_ref` on a row `pack sync` wrote). When a base-owned `org_pack` pin governs, the vendored central pack was edited (or deleted) after `pack sync`.

Fix: re-add the pack with `verdict pack add <dir|git-url>[@ref]` so the lock records the current tree, or restore the pack the lock pins, then re-run. For a central pack, re-sync it with `verdict pack sync` instead. A bundle keeps the lock controls captured at fetch, so repair the trusted base and fetch a fresh bundle before repeating `post` or `record`.

## `pack-lock-missing`

Cause: a repo pack under `.verdict/packs/` has no `.verdict/verdict.lock` entry, or the lock file is absent while repo packs exist. When a base-owned `org_pack` pin governs, no row synced from the pinned repository exists yet, or the row that exists was synced at another `ref` — a bumped tag such as `v1.0.0` to `v1.1.0` repins the pack, and the vendored tree is still the old ref's.

Fix: run `verdict pack add <dir|git-url>[@ref]` to vendor the pack and write its lock entry, commit both, then re-run. For a central pack, run `verdict pack sync` to vendor the pinned `repo@ref` and lock it. A bundle keeps the lock controls captured at fetch, so repair the trusted base and fetch a fresh bundle before repeating `post` or `record`.

## `pack-staging-stale`

Cause: a crashed re-add left a dot-prefixed `.verdict/packs/.<slug>.tmp-*` staging tree or `.old-*` backup that the next add of that pack cannot remove (usually its permissions), so the add refuses rather than stage beside it.

Fix: delete the path the message names by hand (`rm -rf .verdict/packs/.<slug>.tmp-* .verdict/packs/.<slug>.old-*`), then re-run. The loader already ignores those siblings, so a listed pack is not affected.

## `pack-test-cases`

Cause: the lens pack has no eval cases under `evals/`.

Fix: add `evals/<case>/diff.patch`, `findings.json`, and `expect.json` to the pack, then re-run `verdict pack test`.

## `pack-too-large`

Cause: a lens pack exceeds the per-file (64 KiB), per-pack (1 MiB), file-count (256), or lens-count (64) cap.

Fix: trim the pack under those caps, then re-run.

## `pack-unsafe-path`

Cause: a lens pack root, file, or symlink escapes the pack directory, the pack root itself is a symlink, or a FIFO/device entry sits where a regular pack file belongs.

Fix: keep the pack root a real directory, every body a regular file, and every path relative with no symlink components, then re-run.

## `panel-empty`

Cause: the bundle has no pinned `panel` roster, fewer than two members, or no known head.

Fix: configure `defaults.panel` with the synthesizer first, re-fetch, then run `verdict panel`.

## `panel-stale`

Cause: `verification/verdicts.json` names a different head from the current bundle.

Fix: dispatch `verification/dispatch.md` against the current head and rewrite `verification/verdicts.json`; then retry `record` or `post`.

## `panel-too-large`

Cause: more than 100 inline findings or 4 verifiers would exceed the 400-dispatch limit.

Fix: narrow the review or pinned roster, re-fetch if the roster changed, then run `verdict panel`.

## `partial-clone`

Cause: `backtest mine` ran in a partial clone (`git clone --filter=blob:none`). Blame fetches each missing blob from the promisor remote on its own round-trip, so the run stalls on the network instead of finishing.

Fix: re-clone without `--filter=blob:none`, or unset the remote's `promisor` and `partialclonefilter` settings and run `git fetch --refetch`, then re-run.

## `post-failed`

Cause: the post submission failed (anchor drift or body size — the message says which).

Fix: narrow anchor drift or body size (see above), then re-run.

## `post-secret-shaped`

Cause: a review draft carries a secret-shaped value — `review.json` for `post`, `drain.replies.json` for `drain --post`.

Fix: remove the value from the draft and re-run. `post` can pass `--allow-secret-values` to publish it anyway; `drain --post` has no override, and `drain --post --dry-run` refuses the same way before any write.

## `pr-not-open`

Cause: the PR is not open (the bundle is stale).

Fix: re-fetch the PR, then re-run.

## `pr-read-failed`

Cause: the PR could not be read from the host, or a host read kept disagreeing
with itself (a GraphQL head that is not yet the REST head, a torn view
document) for the whole bounded wait.

Fix: run `gh auth status`, confirm the repo/PR spelling, then re-run. A bounded
`pr-wait` retries an inconsistent observation on its next interval and names the
retried count in this refusal, so a wait right after a push usually recovers by
itself; rerun once the push settles. `--once` never retries.

## `privacy-unavailable`

Cause: privacy exclusions are configured but the diff carries no file sections, so applied exclusions cannot be shown.

Fix: re-fetch a bundle with file sections (or drop the exclusions), then re-run.

## `prompt-dump-failed`

Cause: an embedded review Procedure file is missing from the binary. The
message names the path (`PROCEDURE.md`, `prompts/system.md`,
`prompts/review.md`, or `prompts/diagrams.md`); the working directory cannot
supply a replacement, so no partial Procedure is written or printed.

Fix: reinstall verdict from a signed release, then re-run.

## `provider-unimplemented`

Cause: the provider documents no path for this verb.

Fix: use the verb named above for this bundle kind (`post` reaches GitHub, GitLab, Forgejo, Bitbucket, and Azure DevOps; the other hosts use `record` and `report`).

## `provider-unknown`

Cause: origin names an unknown or ambiguously configured host. Supported public hosts are `github.com`, `gitlab.com`, `gitcafe.com`, `codeberg.org`, `bitbucket.org`, `dev.azure.com`, and `ssh.dev.azure.com`.

Fix: pass `--provider github|gitlab|gitcafe|forgejo|bitbucket|azure`, or configure `GITLAB_HOST` / `FORGEJO_HOST` for a self-hosted installation. Remote URLs are withheld to protect embedded credentials.

## `rationale-required`

Cause: the veto needs a human rationale on the record.

Fix: add `--rationale` with 30+ chars, or `--force`.

## `read-only-tool`

Cause: an MCP call names a tool hidden by `--read-only` because it writes local state.

Fix: restart the server without `--read-only`; operator-only tools also require `--operator`.

## `record-failed`

Cause: `record` could not write its capture (the message names the file and the error).

Fix: make the bundle directory writable (or re-fetch the bundle), then re-run.

## `record-stale`

Cause: the bundle's `record.json` no longer matches the review post is publishing — `review.json`, `full.diff`, or the bundle head moved after `record` wrote its decision, so the stored approval receipt describes a different review.

Fix: run `verdict record <repo> <slug>` again on the current review (then `post`), so the published body and approval are the decision verdict stored.

## `repair-exhausted`

Cause: `run --rounds N` spent every round without a valid `review.json` (or a round repeated the prior bytes, so there was no progress to spend). The default is two rounds, so this is a review that stayed invalid across one repair attempt.

Fix: fix every error named in `error.problems`, then re-run (when the cap was hit, raise `--rounds` up to 5; for no progress check the round files).

## `report-failed`

Cause: `report` could not read the bundle's review input.

Fix: run the fetch/record step named above first, then re-run.

## `requirement-blocked`

Cause: a finding violates an applicable block clause.

Fix: address it, narrow the clause, or re-fetch without the policy.

## `requirement-unmet`

Cause: a required typed check has no passing evidence for the reviewed head.

Fix: record the evidence named above (`verify --group <id> --command <cmd>`), then re-run — `--force` cannot satisfy a trusted requirement.

## `resume-drift`

Cause: `run --resume` proved the bundle no longer matches the inputs the interrupted execution recorded (`full.diff`, `run.json` and the controls and config it seals, `prompts_hash`, `context.txt`, the reviewed head, the agent and its launch definition, the finder topology, the resolved finder source or review depth), or a bound finder or cross artifact moved or stopped passing its checks, or a phase path is no longer a plain directory.

Fix: re-run `verdict run` without `--resume` to retire the interrupted execution and review the bundle as it stands; `--resume` only ever continues unchanged inputs with intact bound outputs.

## `review-insufficient`

Cause: the review is not backed by evidence the bundle can check — a missing,
stale, or unreadable evidence receipt for the decided head, a gating finding
whose quote is unverified (including a block clause naming one), a missing or
mismatched changed-hunk receipt, or an unknown, uncovered, or non-code
specification check. `record` writes the receipt without approval; `run`
refuses once its repair rounds finish; `report --check` and `gate` fail the
`evidence` dimension; `report` withholds `data.approval`; and `post` refuses a
binding approval with it, even under `--force`.

Fix: run `record --dry-run` and read what it names — the finding quote at `path:line` that `full.diff` does not carry, the `hunks.tsv` row with no receipt, the spec item with no check or no code evidence — rewrite `review.json`, then re-run `record` or `run --rounds`. A quote gets one repair round; a receipt or spec gap re-prompts on every round.

## `rigor-below-floor`

Cause: a posting finding's evidence rung sits below the floor its severity demands, in gate mode.

Fix: deepen the evidence and re-review, or re-run with `--force` to publish and record the bypass in `post.json`.

## `rounds-path-unsafe`

Cause: the bundle's `.run.rounds` entry is a symlink or another non-directory, so round evidence would land outside the bundle (or a sweep would delete files there).

Fix: remove the named path from the bundle, then re-run.

## `run-mismatch`

Cause: `.verdict/backtest/<run>/run.json` exists with a different mode or window than this invocation, so replay would grade one resolution against another.

Fix: pick another `--run <slug>`, or delete the existing run directory and mine the window again.

## `sandbox-unavailable`

Cause: the sandbox cannot be enforced — on Linux, no `bubblewrap` or no unprivileged user namespaces; on macOS, no working `sandbox-exec`; on any other host, no enforced mechanism at all. The same code covers the inputs the sandbox must resolve before it can hide anything:

- `HOME is unset in the child environment` — the credential paths under `HOME` cannot be named. Keep `HOME` in the child environment (it is on the default allowlist; a `--env-allowlist` or `.verdict/verify-env` setup must not drop it).
- `HOME "<value>" in the child environment is not an absolute path` — a relative `HOME` names no fixed location to hide. Export an absolute `HOME`.
- `the reviewed root <root> is inside the credential path <path>` — hiding the path would hide the code under review, and allowing the root would expose the credential. Verify from a checkout outside your credential stores.
- `the reviewed root <root> has no .git` (Linux) — `bwrap` can make `<root>/.git` read-only only by mounting over it, and creating the missing mount point would write an empty `.git` into your tree, so without one the command could create git hooks or config that run later. Verify from the top of a git checkout, or run the whole review in a throwaway VM or container.
- `could not inspect <root>/.git (<error>)` (Linux) — `.git` exists but cannot be read (permissions, `ENOTDIR`, an I/O error); the error is quoted as reported. Fix the path or its permissions.
- `<path> names the git directory <dir>, which does not exist as a directory` / `<path> has no readable git pointer line` / `<root>/.git is neither a directory nor a gitdir: file` / `the git location <dir> does not resolve (<error>)` / `the git location <dir> is the reviewed root itself` / `the git location <dir> passes through the symlink <link> inside the reviewed root` (Linux) — a `.git` file or a `commondir` points somewhere `bwrap` cannot make read-only, so the command could create or swap the git directory git later runs hooks from. Fix or remove the pointer (`git worktree repair`, or point `gitdir:` at the real directory), or verify a fresh clone.
- `the reviewed root's <root>/.git is a symlink` (Linux) — `bwrap` can protect only the link's target, so the link could be replaced. Replace the link with the directory or a `gitdir:` file, or verify a fresh clone.

Fix: install bubblewrap on Linux and allow unprivileged user namespaces (or, accepting network isolation alone, pass `--sandbox-network-only`, which needs `unshare`), or fix `sandbox-exec` on macOS (`/usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' true` must exit 0), or re-run without `--sandbox` on a command you trust.

`--sandbox` is the enforced half of the split: the static gate is a
tripwire (see `verify-networked`), and this flag is the boundary. A host
that cannot enforce it refuses here instead of running the command
unwrapped.

## `sarif-too-large`

Cause: the SARIF input — file or stdin — exceeds the 8 MiB size cap.

Fix: split or filter the SARIF output per tool or target under 8 MiB, then re-run `verdict ingest --from sarif <file|->`. Narrowing the analyzer's rules is usually cheaper than splitting the document.

## `scope-escape`

Cause: the fix touched files outside the anchor path.

Fix: revert or stash files outside the anchor path, then re-run `fix`.

## `serve-listen-failed`

Cause: the listener cannot bind — the address is in use, unavailable, or not a local interface.

Fix: pass `--port 0` for a kernel-assigned port (the default), free the named address, or choose a bind address this machine owns.

## `skill-bad-dir`

Cause: `--dir` is not absolute.

Fix: pass an absolute `--dir`.

## `skill-dir-foreign`

Cause: the target is a non-verdict skill dir (no `.verdict-managed` receipt); `skill install` and `skill uninstall` both raise it.

Fix: for install, re-run with `--force` to write only verdict's payload files, or pick an empty dir; for uninstall, re-run with `--force` to remove only verdict's payload files and the receipt.

## `skill-foreign-conflict`

Cause: a payload path or the `.verdict-managed` receipt collides with a file or
directory in the way.

Fix: remove the colliding path by hand, then re-run.

## `skill-install-failed`

Cause: the skill payload could not be inspected, created, or written — including a
target or payload dir that is not writable. On POSIX `skill install` probes every
directory it will write before the first byte, so an unwritable target or payload
dir refuses there instead; a target that changes between that probe and the write
still fails here, at write time. On Windows the probe only stats the directory, so
an ACL-denied dir or a read-only payload file also fails here, after the files and
targets ahead of it landed.

Fix: check the target path and permissions named above, then re-run.

## `skill-no-detected-agent`

Cause: no agent could be detected for `skill install`.

Fix: name the agent explicitly with `verdict skill install --agent <name>`.

## `skill-no-home`

Cause: neither `HOME` (nor `CLAUDE_CONFIG_DIR`/`CODEX_HOME`) resolves the agent skills dir.

Fix: set `HOME`, or pass `--dir` instead.

## `skill-not-installed`

Cause: the skill is not installed at the target.

Fix: install first with `verdict skill install --dir <skills-dir>`, or check the target.

## `skill-path-not-inventory`

Cause: the path is not in the embedded inventory.

Fix: run `verdict skill files` to list every embedded path.

## `skill-read-failed`

Cause: the embedded skill payload could not be read.

Fix: reinstall the binary (the payload ships inside it) and re-run.

## `skill-target-file`

Cause: the target exists but is not a plain directory — it is a file, or a symlink to a directory; `skill install` and `skill uninstall` both refuse it.

Fix: point `--dir` or the agent config at the real skills directory instead of the symlink, or remove the file, then re-run.

## `skill-unknown-agent`

Cause: `--agent` names an unknown agent.

Fix: re-run with one of the known agent names listed above.

## `spec-too-large`

Cause: the combined `--spec` checklist sources enumerate more than 150 specification items; `fetch` and `fetch-local` refuse before writing any bundle files.

Fix: narrow the `--spec` file under 150 checklist items or split the PR, then re-fetch.

## `spend-capped`

Cause: the projected run cost exceeds the personal spend ceiling.

Fix: re-run with `--consent-over-budget` once the spend is approved, or raise the personal spend ceiling (`spend.budget_usd`) in the operator settings, then re-run.

## `stale-review-artifact`

Cause: `review.json` predates the launch (an earlier run's).

Fix: delete the stale `review.json` (or let the agent overwrite it), then re-run.

## `store-busy`

Cause: another write held the store's lock — `.verdict/incidents.jsonl.lock` for `--incident`, `.verdict/kb.jsonl.lock` for `--rule`, `--pack`, `--retire`, and `--restore`, the same `kb.jsonl.lock` for the credit rewrite a `record` or `triage` flip performs, and `.verdict/reverts.jsonl.lock` for `backtest reverts` — for the whole five-second wait, so this write did nothing. The lock is the operating system's and its holder is alive, so nothing is stale to clear.

Fix: re-run the write once the other one finishes; a holder that crashed or was killed already released the lock, so a retry succeeds at once. Deleting the lock file never helps — it carries no state, and the next write creates it again.

## `test-conflict`

Cause: `fix --test` would overwrite a promoted target at `.verdict/tests/` that already holds different bytes (or is not a regular file), or `.verdict` / `.verdict/tests` is a symlink — the refusal names the target.

Fix: resolve the named target or symlink by hand (promote the retained bytes, or remove the divergent file or link), then re-run `verdict fix <owner/repo> <local-slug|pr> --test <sig>`.

## `threads-failed`

Cause: the thread list could not be read from the host.

Fix: run `gh auth status`, confirm the repo/PR spelling, then re-run.

## `triage-failed`

Cause: `triage` could not write `findings.json`.

Fix: make the bundle directory writable, then re-run.

## `trigger-failed`

Cause: the trigger could not read its trusted-base policy or record the decision in `run.json`.

Fix: re-fetch the bundle so its base snapshot is complete, then re-run `ready --filter`.

## `uninstall-foreign`

Cause: the binary is not installer-managed.

Fix: remove it with the printed manager command.

## `uninstall-no-receipt`

Cause: no installer receipt sits in the install dir (not installed by `install.sh`).

Fix: reinstall with the printed installer command.

## `unknown-agent`

Cause: the agent name is not in the agent table.

Fix: re-run with one of the known agent names listed above.

## `unknown-group`

Cause: the run has no plan group with that id.

Fix: pass a group id from the list named above.

## `unknown-judgment`

Cause: a judge entry's sig matches no finding in `findings.json`.

Fix: copy the full sig prefix from `report` output.

## `unknown-recipe`

Cause: `run --recipe <slug>` names no entry in the base `.verdict/recipes.json` and no built-in recipe.

Fix: pass `--recipe` one of the names listed above; recipes live in `.verdict/recipes.json` at the PR base, so commit the entry there and re-fetch.

## `unknown-rule`

Cause: no rule carries that id.

Fix: list rules with `learn --status`, then re-run.

## `unknown-sig`

Cause: the sig prefix matched nothing known.

Fix: copy the full sig prefix from `report` output.

## `unknown-thread`

Cause: no thread with that id sits in `threads.json`.

Fix: refresh threads first, then re-run with an id from the list.

## `unrecorded-compare-bundle`

Cause: the `--compare` baseline has no `findings.json`, so it was never recorded or posted and has nothing to compare.

Fix: `record` or `post` the baseline bundle first, then re-run `report --compare`.

## `unsafe-compare-bundle`

Cause: the `--compare` baseline or one of its inputs (the directory — with or without a trailing separator — `findings.json`, or a chunk diff) is a symlink, a file, or a device; the comparison reads regular files only.

Fix: pass the bundle directory itself — not a symlink, a file, or a device — then re-run.

## `unsupported-cap`

Cause: `spend.enforce_cap` requests a hard-cap model this build does not implement.

Fix: remove it from the operator settings — budget, force, and consent cannot authorize an unsupported cap.

## `unsupported-platform`

Cause: no release asset exists for this `GOOS`/`GOARCH`.

Fix: run on a supported platform (darwin/linux/windows, amd64/arm64).

## `upgrade-bad-method`

Cause: `VERDICT_INSTALL_METHOD` names an unknown channel.

Fix: set it to one of `installer-managed|npm|pnpm|bun|yarn|homebrew|project-local|skill|unknown`.

## `upgrade-bad-version`

Cause: `--version` is not a releasable version.

Fix: pass `--version X.Y.Z` (or `vX.Y.Z`).

## `upgrade-failed`

Cause: the upgrade could not resolve, stage, or install the release — the message says which step.

Fix: address the error named above and re-run `upgrade`.

## `upgrade-needs-privilege`

Cause: `upgrade` runs as root/admin, or the install dir is not writable.

Fix: run the installer as the owning user — reinstall without sudo using the printed command.

## `upgrade-project-local`

Cause: the binary is a project-local install — the upgrade belongs in the project manifest/lockfile.

Fix: run `npm install @stainless-code/verdict@latest` in the project root.

## `upgrade-skill-copy`

Cause: the binary is a skill copy.

Fix: update the skill with `npx skills update`, or bootstrap with `./scripts/verdict bootstrap`.

## `upgrade-unidentified`

Cause: the running binary (or its install channel) could not be identified.

Fix: reinstall with the printed installer command.

## `upgrade-version-unsupported`

Cause: the Homebrew formula owns version selection.

Fix: run `brew upgrade` on the formula instead of pinning `--version`.

## `upstream-diff-too-large`

Cause: the upstream diff is over the cap.

Fix: re-fetch narrowed to a range via `--base-sha`/`--since`, or narrow with a paths filter, then re-run.

## `validation-stale`

Cause: `validation.json` was written for a head that is not the bundle head, usually after a push that moved it.

Fix: re-run the validation pass against the current head, or delete the artifact to post unreviewed.

## `verify-credential-read`

Cause: `verifyCmd` reads credential paths (or pipes env into the network).

Fix: rewrite `verifyCmd` without reading credential paths or piping env into the network, then re-run.

## `verify-destructive`

Cause: `verifyCmd` is destructive (a `rm`/`mkfs`/`dd`-class command).

Fix: rewrite `verifyCmd` to stay in-repo and non-destructive (run the test runner, not `rm`/`mkfs`/`dd`), then re-run.

## `verify-failed`

Cause: the verification command (or the receipt/head read around it) failed — the message says which.

Fix: fix the command or the evidence named above, then re-run `verify`.

## `verify-indirection`

Cause: `verifyCmd` invokes a command word the gate cannot read — a plain
variable (`E=curl; $E -o ./f http://host/x`), the braced (`${E}`) or quoted
(`"$E"`) spelling, an `export`, an assignment prefix, a loop variable, the
same spelling on its own line, after a leading redirection (`</dev/null $E`,
`2>&1 $E`, `>> f $E`), or inside a substitution (`echo $($E pwned)`, backticks,
`cat <($E pwned)`), whose body is judged as its own command — quotes included,
so `$($E "(")` is read the way bash reads it. The value may
come from the command's own environment, so the gate refuses the shape instead
of guessing what the variable holds.

A wrapper's own option grammar decides which later word bash runs, so after
`builtin`, `command`, `env`, `exec`, `nice`, `nohup`, `stdbuf`, `time`,
`timeout`, or `xargs` any variable in that command refuses: `timeout 300 make
test` stays clean, `timeout 300 make test $TARGET` does not. A shell counts
the same way when it carries an inline command — any short-flag cluster with a
`c` in it (`sh -c`, `bash -c`, `bash -ec`, `sh -cu`): the inline command string
is judged as its own command, so `sh -c "$E pwned"` refuses while
`sh -c "echo $E"` stays clean. `eval` and `sudo` refuse earlier, as
`verify-destructive`, whatever their arguments. A quoted word stays one word:
`LABEL="hello world" $E ok` and `>"out file" $E` refuse, because quote removal
would otherwise put `world` in command position.

The gate is a tripwire, not a boundary, and it runs before the sandbox
wrapper: `verify --sandbox` refuses an indirection the same way, so the remedy
is to write the command plainly. A variable holding a whole command line
(`sh "$V"`, a file written and then run) is a shape the gate has not learned.

Fix: write the tool word plainly (`E=curl; $E -o ./f http://host/x` becomes
`curl -o ./f http://host/x`, or the test runner directly) and keep variables
for arguments of a command written plainly, then re-run.

## `verify-networked`

Cause: `verifyCmd` fetches the network or pipes to a shell.

The gate is a tripwire, not a boundary: it refuses the spellings it knows,
and a shape it has not learned executes. For a boundary, run the step
under `verify --sandbox`: `bubblewrap` on Linux removes the route out, and
`sandbox-exec` on macOS denies the network and every Mach service; a host
that can enforce neither refuses `sandbox-unavailable`.

Fix: rewrite `verifyCmd` without network fetches or pipe-to-shell (fetch fixtures first, run offline), then re-run.

## `verify-repo-escape`

Cause: `verifyCmd` escapes the repo root.

Fix: rewrite `verifyCmd` with repo-relative paths (`/tmp` for scratch), then re-run.

## `window-too-large`

Cause: the `--since..--until` commit list is larger than one 16 MiB capture.

Fix: narrow `--since` to a shorter span or a later ref, then re-run.

## `worktree-failed`

Cause: the isolated worktree checkout failed — the message carries the git error.

Fix: fix the git state named above and re-run `fetch-local`.

### Learning proposal lifecycle

`bad-flags` means the lifecycle modes were combined, the bundle operands were
missing, or `--dry-run` was used outside propose/accept/reject. Choose one mode.
`bad-reason` requires at least 30 characters for rejection. `bad-id` requires a
KB slug. `bad-rule` refuses acceptance of a rejected learning or rejection of an
already accepted rule; use `learn --retire` for the latter. `unknown-rule` means
there is no matching one-rule proposal pack. `learn-failed` can mean a missing or
invalid normalized `threads.json`, or a failed local write. Fetch the bundle
first and check local permissions. Pack validation refusals apply to edited
proposal packs exactly as they do to `learn --pack`. `corrupt-store` refuses
mining or rejection when the KB cannot be read safely; repair the store first.

## `wait-timeout` (data outcome)

Cause: a bounded `pr-wait` or `ready --wait-for build` call exhausted its budget.
This is a result in `data`, not an envelope refusal code. `pr-wait` exits 1
with `error: null`; `ready` reports not-ready and a `wait-timeout` blocker,
exiting 0 unless `--check` requests exit 1.

Fix: inspect the last snapshot and CI state, then make another bounded call if
needed. Use MCP `pr_wait` instead of an agent sleep loop. A missing or unreadable
check rollup never counts as complete. GitLab and Forgejo waits refuse
`provider-unimplemented`; GitHub hard read failures, and a state still
inconsistent when the budget expires, refuse `pr-read-failed`. CLI timeouts
accept 1s–30m; MCP timeouts cannot exceed 50s.

## `condition-unmet` (data outcome)

Cause: `pr-wait --once` returned its single observation before the requested
condition held. Exit 1 carries data with `error: null`.

Fix: inspect the snapshot, or remove `--once` and supply a bounded timeout.
For `review-changed`, each invocation starts a new baseline.

## When `ingest` skips a result

Cause: this is not a refusal. A SARIF result the bundle's pinned diff cannot
anchor is published in `data.skipped[]` with `reason`, `tool`, `rule_id`,
`path`, and `line`, and nothing is written for it: `no-location` (no physical
location or artifact URI), `invalid-line` (absent, zero, or negative line),
`path-escapes-repo` (absolute path, drive path, a colon, `..` traversal, control
byte, invalid UTF-8, a foreign scheme, or an authority-bearing `file://`
reference such as `file://localhost/etc/passwd`), `out-of-diff` (a path or line
the changed hunks do not carry),
`ambiguous-path` (the diff names the path twice), or `snippet-mismatch` (the
reported `region.snippet.text` disagrees with the diff line at `path:line`).

Fix: run the analyzer against the reviewed head and keep its paths
repository-relative POSIX (`src/a.go`, not `C:/repo/src/a.go`). A result pointing
outside the diff means the scanner saw a different checkout, so re-run it on the
reviewed head rather than widening the diff.
