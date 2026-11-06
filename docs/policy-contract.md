# Policy contract (evidence + outcomes)

Status: implemented. `fetch`/`fetch-local` persist the run bundle;
`post` and `report` enforce the stored policy and append observations;
`verdict config --explain` renders it and `--preview` resolves it.

## Terms

- **Evidence** is the fetch-time capture: effective values with origins,
  applicable clauses with `pending` status, the combined pending plan, the
  base commit identity, and the configuration hash. Evidence never rewrites.
- **Outcome** is one appended check execution: the check, the group, the
  dated verdict, and the detail. Outcomes never rewrite evidence.
- **Trusted** means the policy came from the base commit (or the reviewed
  worktree for local runs). A proposed file preview is never trusted, and
  preview writes no state, so a proposed config cannot self-approve.

## Rule table

| Rule                                                                                       | Enforced by                                      | Bypasses                                             | Failure                           |
| ------------------------------------------------------------------------------------------ | ------------------------------------------------ | ---------------------------------------------------- | --------------------------------- |
| Open finding on a clause path, in a clause category, at or above the threshold rank blocks | `post` compliance (full stored set)              | none: `--force` never consults                       | `requirement-blocked`             |
| Below-floor findings stay out of the payload                                               | `post` payload (unknown severities stay visible) | none (gates keep the full set)                       | —                                 |
| Binding approval needs `--bind` when any applicable set requires it                        | `post` bind gate                                 | none: `--force` never consults                       | `bind-required`                   |
| Approval needs zero open/deferred findings                                                 | `post` approve guard                             | `--force` (records `approve-blocked`)                | `approve-blocked`                 |
| Personal spend ceiling                                                                     | `fetch` personal gate                            | none: no consent path                                | `spend-capped`                    |
| Ordinary estimate guard                                                                    | `fetch` two-phase gate                           | consent receipt (`--consent-over-budget`)            | `budget-exceeded`                 |
| Unknown projection with a set budget                                                       | `fetch` gate                                     | consent receipt, unless `require_known_estimate`     | `spend-unknown`                   |
| Hard cap                                                                                   | `fetch` gate with `enforce_cap`                  | none: consent refused                                | `budget-exceeded`                 |
| Privacy exclusions                                                                         | `fetch` exports, chunk writer, prompt dump       | none: repository scopes and flags cannot narrow them | silent withhold + dump note       |
| Display floor                                                                              | `report` view (gates keep the full set)          | none                                                 | —                                 |
| `no-open-above-block` observed per group                                                   | `post`/`report` outcome ledger                   | append-on-change only                                | `requirement-blocked` / gate fail |
| `head-current` observed on completeness                                                    | `report` outcome ledger                          | append-on-change only                                | gate fail                         |
| `no-live-threads` observed on recheck                                                      | `post`/`report` outcome ledger                   | append-on-change only                                | downgrade / gate fail             |

## Degrade rules

- No run bundle: every verb degrades to the pre-policy behavior (legacy
  path, downgrade warning where one exists). Nothing refuses for a missing
  bundle except `config --explain`, which refuses `config-no-bundle`.
- Corrupt bundle: loud `bad-config` refusal, never silent degrade.
- Untrusted preview: resolves, never persists, never approves.
