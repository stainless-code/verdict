# verdict — vision

## What it is

`verdict` is local, on-demand PR review for any coding agent. You point it at a pull
request; it reads the complete diff, investigates with evidence-graded findings, and posts
exactly one GitHub review — then stops. The agent reviews; `gh` does GitHub I/O. No servers,
no external services, no daemons, no accounts.

One sentence: **PR reviews with receipts.**

Its review core descends from Pullfrog's open-source action; its discipline borrows
from clawpatch and pstack — credited, not hidden (see [`NOTICE.md`](../NOTICE.md)).

## Public narrative

The pains, the answers, the refusals, the concessions, and the competition table
live on the docs site, which owns that wording:

- pains and answers → [`why-verdict.mdx`](../apps/docs/content/why-verdict.mdx)
- refusals and concessions → [`why-verdict.mdx`](../apps/docs/content/why-verdict.mdx)
- competition table, complements, lineage → [`comparison.mdx`](../apps/docs/content/comparison.mdx)

This file keeps the essentials: what the thing is and where its craft comes from.
