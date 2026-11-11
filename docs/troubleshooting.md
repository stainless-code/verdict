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

Cause: a completed post already exists on this bundle (an exact payload replay or a second completed post).

Fix: re-run with `--force` to override, or `fetch --base-sha <posted-head>` for the delta and review that instead — re-fetch if the earlier review was deleted.

## `already-retired`

Cause: the rule is already retired.

Fix: check the rule status with `learn --status`, then re-run (`learn --restore` brings it back with a `--reason`).

## `already-reviewed-patch`

Cause: this exact patch was already reviewed and posted.

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

## `bad-approve`

Cause: `--approve` is not a boolean.

Fix: pass `--approve=true|false`.

## `bad-attribution`

Cause: `--attribution` names an unknown tier.

Fix: re-run with one of `off|product|full`.

## `bad-base`

Cause: the checkout shares no merge-base with `--base`.

Fix: pass a `--base` reachable from this checkout, or run in the right repository.

## `bad-budget`

Cause: `REVIEW_BUDGET_USD` (or the typed `budget_usd` default) is not above 0, or the scaffolded config fails validation.

Fix: export a positive `REVIEW_BUDGET_USD` (or set a positive `budget_usd`), then re-run.

## `bad-category`

Cause: `report --category` names an unknown category.

Fix: re-run with one of the valid values named above.

## `bad-color`

Cause: `--color` is not a known mode.

Fix: pass `--color auto|always|never`.

## `bad-config`

Cause: the repository configuration (or a fragment of it) is malformed or semantically invalid — the message names the exact key.

Fix: fix the named key (see `docs/manual.md` for the shape), then re-run.

## `bad-config-credentials`

Cause: the configuration file carries a credential key (credentials never live there).

Fix: remove the credential key named above and export it as an environment credential instead, then re-run.

## `bad-flags`

Cause: the flag combination is not valid (e.g. `--range`/`--base`/`--staged`/`--stash` are mutually exclusive, or `learn` got no mode).

Fix: re-run with a valid combination — the message names the clash.

## `bad-from`

Cause: `adopt` got no (or a bad) `--from` local slug.

Fix: pass `--from <local-slug>` pointing at a `.verdict/state/local-*` directory carrying `findings.json`.

## `bad-group-validation`

Cause: `verify --group` is missing `--group` or `--command`, or the group id cannot name a confined receipt file.

Fix: pass both `--group <id>` and `--command <cmd>` with an id matching `[a-z0-9-]` (max 64 chars).

## `bad-id`

Cause: `--id` breaks the id shape.

Fix: pass an `--id` matching `^[a-z0-9][a-z0-9-]{0,64}$`, or omit `--id`.

## `bad-judgment`

Cause: the `calibrate --probe` judge file is missing or unreadable.

Fix: write one `{"sig": ..., "agent_verdict": ...}` object per line (or a JSON array of them), then re-run.

## `bad-max-bytes`

Cause: `REVIEW_MAX_BYTES` is not a positive byte count.

Fix: export a positive byte count and re-run.

## `bad-override`

Cause: the named overview/walkthrough override file is unreadable.

Fix: fix its permissions (or remove it), then re-run.

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

## `bad-requirement`

Cause: a requirement clause is invalid or unsatisfiable — the message names the key.

Fix: fix the clause named above and re-run.

## `bad-review-input`

Cause: the agent input is malformed (the message names the field).

Fix: fix the malformed agent input named above (`review.json` wants `{body, approved, comments[]}`; claims want string sigs), then re-run.

## `bad-review-schema`

Cause: `review.json` does not stamp `"schema_version": "1"`.

Fix: set `"schema_version": "1"` in `review.json`.

## `bad-rigor`

Cause: `--rigor` is not a known level.

Fix: re-run with one of `said|pointed|walked|ran|reproduced`.

## `bad-rule`

Cause: `learn --rule` got an empty rule.

Fix: pass the one-line rule text to `--rule`.

## `bad-scope`

Cause: `--scope` is empty, malformed, or belongs to a different mode (or the scope config key is invalid).

Fix: pass a comma-separated glob list (e.g. `src/*.ts`) together with `--rule`, or fix the named scope key.

## `bad-settings`

Cause: the settings file is malformed (or carries an unknown placeholder).

Fix: keep only `output/layout/color/publish/review/spend/privacy` keys (see `docs/manual.md`), then re-run.

## `bad-settings-credentials`

Cause: the settings file carries a credential key.

Fix: remove the credential key from the settings file (credentials never live there), then re-run.

## `bad-sig`

Cause: the `--sig` prefix matched nothing actionable.

Fix: list open inline sigs with `report --status open --kind inline`, then re-run with the full prefix.

## `bad-slug`

Cause: the local bundle name is unusable (it sanitizes to nothing, looks like a PR number, or already carries the `local-` prefix).

Fix: pass a usable bundle name — `fetch-local` adds `local-` itself.

## `bad-stash`

Cause: the named `stash@{N}` does not exist.

Fix: pick one from `git stash list` and re-run with `--stash <N>`.

## `bad-target`

Cause: `adopt --from` points at a PR bundle instead of a local one.

Fix: point `--from` at a `.verdict/state/local-*` directory.

## `bad-timeout`

Cause: `--timeout` is not a positive number of seconds.

Fix: pass `--timeout <positive-seconds>` (default 300).

## `bad-triage-status`

Cause: `--status` is not a known triage state.

Fix: re-run with one of `open|addressed|wont-fix|false-positive|deferred`.

## `bad-usage`

Cause: the `VERDICT_USAGE` record does not match `schemas/usage.schema.json`.

Fix: fix the usage record named above to match the schema, then re-run.

## `bind-required`

Cause: the run policy sets `require_bind` and this binding approval lacks `--bind`.

Fix: pass `--bind` to bind this approval (binding downgrades never satisfy a required bind, and `--force` cannot bypass it).

## `budget-exceeded`

Cause: the projected run cost exceeds the budget.

Fix: re-run with `--consent-over-budget` once the spend is approved, or raise `REVIEW_BUDGET_USD`/the typed `budget_usd` default.

## `budget-unknown`

Cause: no usable cost projection exists and the operator settings require a known estimate.

Fix: re-run with `--consent-over-budget` to consent to the blind run (or relax `require_known_estimate`).

## `calibrate-failed`

Cause: `calibrate --probe` could not write its grades file.

Fix: make the bundle directory writable, then re-run.

## `canceled`

Cause: the run was canceled (`SIGINT`/`SIGTERM`) before the verb completed.

Fix: re-run the same command (a cancel exits 3, never 0).

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

Cause: the benchmark case manifest is invalid — the message names the file and the reason.

Fix: fix the manifest named above and re-run.

## `corrupt-store`

Cause: `findings.json` (or the knowledge-base store) is present but unreadable.

Fix: re-run `post`/`record` to rebuild the store, or restore `findings.json` from backup, then re-run.

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

## `degenerate-body`

Cause: the published body carries no verdict marker.

Fix: open with a verdict marker (see `prompts/review.md`), then re-run.

## `diff-failed`

Cause: `git diff` failed for the requested range.

Fix: fix the range so both refs resolve, then re-run `fetch-local`.

## `diff-too-large`

Cause: the fetched diff is over the cap.

Fix: split the range (`--range`/`--base`/`--base-sha`) or narrow with a paths filter, then re-fetch.

## `download-failed`

Cause: the release download failed (bad version, no download directory, or a fetch error — the message says which).

Fix: pass a concrete `X.Y.Z` (or `vX.Y.Z`) version with a writable download dir, then re-run `upgrade`.

## `duplicate-payload`

Cause: the exact payload (same body, comments, and SHA) was already posted.

Fix: re-run with `--force` to repost the identical payload — re-fetch if the earlier review was deleted.

## `fetch-failed`

Cause: the provider CLI call failed.

Fix: run `gh auth status`, confirm the repo/PR spelling, then re-run.

## `fix-ceiling`

Cause: the fix loop hit its pass ceiling without clearing the sig.

Fix: triage the sig or split the fix — the 3-pass ceiling is hard-coded.

## `fix-failed`

Cause: the fix ledger could not be written.

Fix: make the bundle directory writable, then re-run `fix`.

## `head-mismatch`

Cause: the bundle carries no reviewed head, or the checked-out head is not the reviewed head.

Fix: check out the reviewed commit (or re-fetch the bundle), then re-run validation.

## `id-taken`

Cause: `--id` is already taken.

Fix: pass a different `--id`.

## `init-failed`

Cause: `init` could not create `.verdict` or write a control file (storage not writable).

Fix: make the directory writable, then re-run `init`.

## `internal`

Cause: an unexpected internal error — the message carries the detail.

Fix: re-run the same command; if it repeats, report it with the full output.

## `learn-failed`

Cause: `learn` could not write the knowledge-base store (or create `.verdict`).

Fix: make `.verdict` writable (or restore the knowledge-base store from backup), then re-run.

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

Cause: the bundle is absent — nothing fetched it yet.

Fix: run `fetch` first (`verdict fetch <owner>/<repo> <pr>`, or `verdict fetch-local` for a local bundle).

## `missing-chunk-coverage`

Cause: the bundle lacks chunk coverage.

Fix: re-fetch the bundle with `--chunk`, then re-synthesize across all chunks and re-run.

## `missing-chunk-snapshot`

Cause: the bundle lacks the chunk hunk snapshot.

Fix: re-fetch the bundle with `--chunk`, then re-synthesize across all chunks and re-run.

## `missing-diff`

Cause: `full.diff` is absent.

Fix: fetch the PR first, then re-run.

## `missing-findings`

Cause: `findings.json` is absent.

Fix: run `post` or `record` first, then re-run.

## `missing-patch`

Cause: `patch.json` is absent — nothing claimed to verify.

Fix: produce the patch claim first, then re-run `verify`.

## `missing-post`

Cause: `post.json` is absent (or carries no review id/head) — nothing posted to calibrate.

Fix: `post` for real first, then re-run `calibrate`.

## `missing-review-file`

Cause: the review file is absent.

Fix: have the agent write `{body, approved, comments[]}` to the review file first, then re-run.

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

## `no-patch-match`

Cause: no local bundle matched the patch operation.

Fix: pass `--from <local-slug>`, or re-fetch the target bundle so `identity.json` exists.

## `no-pr-target`

Cause: the verb needs a PR bundle (local bundles never post).

Fix: `fetch` the PR first; local bundles use `record` and `report`.

## `no-review-artifact`

Cause: the agent exited `0` but wrote no `review.json` (or a non-file).

Fix: run the printed command yourself (`--print`) to see why no `review.json` appeared, then re-run.

## `no-run-bundle`

Cause: the bundle has no verified `run.json`.

Fix: re-fetch it before deep validation.

## `no-threads`

Cause: `calibrate` joins live threads — nothing to settle on a local bundle.

Fix: `adopt` and `post` first, then calibrate the PR.

## `no-verify-cmd`

Cause: `patch.json` carries no `verifyCmd` to execute.

Fix: add a `verifyCmd` to the patch claim, then re-run `verify`.

## `not-a-repo`

Cause: the command needs a git worktree and this directory is not one.

Fix: run inside a git worktree, then re-run.

## `not-local`

Cause: the verb got the wrong bundle kind (`record` on a GitHub PR, or `fix` on a PR bundle).

Fix: `fetch-local` for local work first; GitHub PRs use `post`, and local bundles use `record` and `report`.

## `not-retired`

Cause: the rule is already active.

Fix: check the rule status with `learn --status`, then re-run.

## `not-verdict-authored`

Cause: the thread is authored by someone (or something) else — only verdict-authored threads resolve.

Fix: resolve only verdict-authored threads (pass the ids from `threads.json`).

## `post-failed`

Cause: the post submission failed (anchor drift or body size — the message says which).

Fix: narrow anchor drift or body size (see above), then re-run.

## `post-secret-shaped`

Cause: `review.json` carries a secret-shaped value.

Fix: remove the value from `review.json` and re-run, or pass `--allow-secret-values` to publish it anyway.

## `pr-not-open`

Cause: the PR is not open (the bundle is stale).

Fix: re-fetch the PR, then re-run.

## `pr-read-failed`

Cause: the PR could not be read from the host.

Fix: run `gh auth status`, confirm the repo/PR spelling, then re-run.

## `privacy-unavailable`

Cause: privacy exclusions are configured but the diff carries no file sections, so applied exclusions cannot be shown.

Fix: re-fetch a bundle with file sections (or drop the exclusions), then re-run.

## `prompt-dump-failed`

Cause: the prompt dump needs the skill and prompts sources.

Fix: run from a checkout carrying `SKILL.md`/`prompts`, then re-run.

## `provider-unimplemented`

Cause: the provider documents no path for this verb.

Fix: use the verb named above for this bundle kind (`post` reaches GitHub, GitLab, and Forgejo; the other hosts use `record` and `report`).

## `rationale-required`

Cause: the veto needs a human rationale on the record.

Fix: add `--rationale` with 30+ chars, or `--force`.

## `record-failed`

Cause: `record` could not write its capture (the message names the file and the error).

Fix: make the bundle directory writable (or re-fetch the bundle), then re-run.

## `repair-exhausted`

Cause: `run --rounds N` spent every round without a valid `review.json` (or a round repeated the prior bytes, so there was no progress to spend).

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

## `rounds-path-unsafe`

Cause: the bundle's `.run.rounds` entry is a symlink or another non-directory, so round evidence would land outside the bundle (or a sweep would delete files there).

Fix: remove the named path from the bundle, then re-run.

## `sandbox-unavailable`

Cause: the sandbox cannot be enforced (no bubblewrap, or no unprivileged user namespaces).

Fix: install bubblewrap on Linux and allow unprivileged user namespaces, or re-run without `--sandbox` on a command you trust.

## `scope-escape`

Cause: the fix touched files outside the anchor path.

Fix: revert or stash files outside the anchor path, then re-run `fix`.

## `skill-bad-dir`

Cause: `--dir` is not absolute.

Fix: pass an absolute `--dir`.

## `skill-dir-foreign`

Cause: the target is a non-verdict skill dir (no `.verdict-managed` receipt); `skill install` and `skill uninstall` both raise it.

Fix: for install, re-run with `--force` to write only verdict's payload files, or pick an empty dir; for uninstall, re-run with `--force` to remove only verdict's payload files and the receipt.

## `skill-foreign-conflict`

Cause: a payload path collides with a foreign file or directory.

Fix: remove the colliding path by hand, then re-run.

## `skill-install-failed`

Cause: the skill payload could not be inspected, created, or written.

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

## `spend-capped`

Cause: the projected run cost exceeds the personal spend ceiling.

Fix: re-run with `--consent-over-budget` once the spend is approved, or raise the personal spend ceiling (`spend.budget_usd`) in the operator settings, then re-run.

## `stale-review-artifact`

Cause: `review.json` predates the launch (an earlier run's).

Fix: delete the stale `review.json` (or let the agent overwrite it), then re-run.

## `threads-failed`

Cause: the thread list could not be read from the host.

Fix: run `gh auth status`, confirm the repo/PR spelling, then re-run.

## `triage-failed`

Cause: `triage` could not write `findings.json`.

Fix: make the bundle directory writable, then re-run.

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

## `unknown-rule`

Cause: no rule carries that id.

Fix: list rules with `learn --status`, then re-run.

## `unknown-sig`

Cause: the sig prefix matched nothing known.

Fix: copy the full sig prefix from `report` output.

## `unknown-thread`

Cause: no thread with that id sits in `threads.json`.

Fix: refresh threads first, then re-run with an id from the list.

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

## `verify-credential-read`

Cause: `verifyCmd` reads credential paths (or pipes env into the network).

Fix: rewrite `verifyCmd` without reading credential paths or piping env into the network, then re-run.

## `verify-destructive`

Cause: `verifyCmd` is destructive (a `rm`/`mkfs`/`dd`-class command).

Fix: rewrite `verifyCmd` to stay in-repo and non-destructive (run the test runner, not `rm`/`mkfs`/`dd`), then re-run.

## `verify-failed`

Cause: the verification command (or the receipt/head read around it) failed — the message says which.

Fix: fix the command or the evidence named above, then re-run `verify`.

## `verify-networked`

Cause: `verifyCmd` fetches the network or pipes to a shell.

Fix: rewrite `verifyCmd` without network fetches or pipe-to-shell (fetch fixtures first, run offline), then re-run.

## `verify-repo-escape`

Cause: `verifyCmd` escapes the repo root.

Fix: rewrite `verifyCmd` with repo-relative paths (`/tmp` for scratch), then re-run.

## `worktree-failed`

Cause: the isolated worktree checkout failed — the message carries the git error.

Fix: fix the git state named above and re-run `fetch-local`.
