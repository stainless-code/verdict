# verdict — vision

## What it is

`verdict` is local, on-demand PR review for any coding agent. You point it at a pull
request; it reads the complete diff, investigates with evidence-graded findings, and posts
exactly one GitHub review — then stops. The agent reviews; `gh` does GitHub I/O. No remote
servers, no external services, no daemons, no accounts. A local, read-only view is opt-in
(`verdict serve`), and nothing leaves the machine.

One sentence: **PR reviews with receipts.**

Its review core descends from Pullfrog's open-source action — the review prompt, not its
harness wrapping; its discipline borrows from clawpatch and pstack — credited, not hidden
(see [`NOTICE.md`](../NOTICE.md)).

## Public narrative

The pains, the answers, the refusals, the concessions, and the competition table
live on the docs site, which owns that wording:

- pains and answers → [`index.mdx`](../apps/docs/content/03-compare/index.mdx)
- refusals and concessions → [`index.mdx`](../apps/docs/content/03-compare/index.mdx)
- competition table, complements, lineage → [`index.mdx`](../apps/docs/content/03-compare/index.mdx)

This file keeps the essentials: what the thing is and where its craft comes from.
