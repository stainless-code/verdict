# Policy contract (evidence + outcomes)

Status: implemented. `fetch`/`fetch-local` persist the run bundle; `post`
enforces the stored policy and appends one observation per scope and
reviewed head; `report` enforces the display floor and records no
observation; `verdict config --explain` renders the ledger and `--preview`
resolves it.

## Terms

- **Evidence** is the fetch-time capture: effective values with origins,
  applicable clauses with `pending` status, the combined pending plan, the
  base commit identity, and the configuration hash. Evidence never rewrites.
- **Outcome** is one appended check execution: the check, the stable group
  id, the fetched head it ran against, the dated verdict, and the detail.
  Outcomes never rewrite evidence. Only an outcome naming that exact scope,
  check, and head counts, so an empty group, an unknown group, or a record
  from another head or run never satisfies a requirement.
- **Trusted** means the policy came from the base commit (or the reviewed
  worktree for local runs). A proposed file preview is never trusted, and
  preview writes no state, so a proposed config cannot self-approve.

## Rule table

| Rule                                                                                               | Enforced by                                                 | Bypasses                                               | Failure                                 |
| -------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------ | --------------------------------------- |
| Open finding on a clause path, in a clause category, at or above the threshold rank blocks         | `post` compliance (full stored set)                         | none: `--force` never consults                         | `requirement-blocked`                   |
| Below-floor findings stay out of the payload                                                       | `post` payload (unknown severities stay visible)            | none (gates keep the full set)                         | —                                       |
| Binding approval needs `--bind` when any applicable set requires it                                | `post` bind gate                                            | none: `--force` never consults                         | `bind-required`                         |
| Approval needs zero open/deferred findings                                                         | `post` approve guard                                        | `--force` (records `approve-blocked`)                  | `approve-blocked`                       |
| Personal limit: exact sum of group projections against `spend.budget_usd`                          | `fetch` personal gate                                       | fresh per-invocation consent (`--consent-over-budget`) | `budget-exceeded`                       |
| Ordinary estimate guard, resolved per file group                                                   | `fetch` group guard                                         | fresh per-invocation consent (`--consent-over-budget`) | `budget-exceeded`                       |
| Unknown group projection makes the whole-run total unknown                                         | `fetch` gate with `require_known_estimate`                  | fresh per-invocation consent (`unknown-estimate`)      | `budget-unknown`                        |
| Hard cap (`spend.enforce_cap: true`)                                                               | `fetch` gate before budget/force/consent                    | none: force and consent refused                        | `unsupported-cap`                       |
| Privacy exclusions                                                                                 | `fetch` exports, chunk writer, prompt dump                  | none: repository scopes and flags cannot narrow them   | silent withhold + dump note             |
| Required review withheld by privacy exclusions                                                     | `report`/`ready`/`gate` compliance; `post` binding approval | none: no force or consent                              | `requirement-unmet` (conflicted)        |
| Display floor                                                                                      | `report` view (gates keep the full set)                     | none                                                   | —                                       |
| `no-open-above-block` evaluated per group over the full stored set                                 | `post` compliance gate + outcome ledger                     | none: `--force` never consults                         | `requirement-blocked`                   |
| `head-current` means the fetched head is the live host head                                        | `post` binding approval (drift read)                        | none: `--force` never consults                         | `requirement-unmet`                     |
| `no-live-threads` means zero unresolved live threads                                               | `post` binding approval (live read)                         | none: `--force` never consults                         | `requirement-unmet`                     |
| Outcome identity: scope id + check + reviewed head                                                 | outcome ledger                                              | append-on-change only                                  | unmet check reads pending               |
| Deep group needs a current tool validation receipt for this run/head/root                          | `post` binding approval                                     | none: `--force` and consent never consult              | `requirement-unmet`                     |
| Group dispatch (`panel`, `panel_from`, accounting `effort`) comes from that group's resolved files | shipped review dispatcher, read from `plan.txt`             | none: the root `panel` roster never overrides a group  | none (accounting only)                  |
| Cost lens evidence binds to the captured run (`schema_version` 2, `run`, `inputs_sha256`)          | `post` lens gate, `record`/`report` warnings                | none                                                   | `requirement-unmet` (unknown, not zero) |
| Display never certifies a check                                                                    | `report` view (records no outcome)                          | none                                                   | —                                       |

## Degrade rules

- No run bundle: every verb degrades to the pre-policy behavior (legacy
  path, downgrade warning where one exists). Nothing refuses for a missing
  bundle except `config --explain`, which refuses `config-no-bundle`.
- Corrupt bundle: loud `bad-config` refusal, never silent degrade.
- Untrusted preview: resolves, never persists, never approves.
