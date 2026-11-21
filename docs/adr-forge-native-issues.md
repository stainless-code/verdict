# Forge-native linked issues only

**Status:** accepted (STA-466).

## Decision

Verdict's Go core reads a linked issue only through a forge-native provider it
already talks to: GitHub today (`closingIssuesReferences`, plus
`gh api repos/<owner>/<repo>/issues/<n>` for a keyword-only or explicit
`--issue` reference), and GitLab and Forgejo on their existing provider seams
when those joins are built. Third-party project-management trackers — Linear,
Jira, Shortcut, Notion, Asana, Azure Boards — are kept out of the core on
purpose. `--issue` accepts forge-native references only: `owner/repo#12`, a
GitHub issue URL, or a bare `#12` on GitHub. An external tracker's issue text
enters a review through `--spec <file>`.

## Why

- **One credential model.** Every forge read rides a CLI the operator already
  authenticated (`gh`, `glab`, `curl`) or a token this project already
  documents. A tracker API would add a second auth story per vendor —
  environment names, GraphQL schemas, pagination, rate limits, and the failures
  each of them produces — to the fetch path that must stay predictable.
- **No API sprawl, no binary bloat.** A core that reads linked issues natively
  holds one query per forge. Each tracker would ship its own request shape,
  response parser, and error mapping into the binary every user downloads, for
  a source only some users have.
- **The unbundled Lego core.** Verdict composes small, honest parts: a forge
  read, a spec source, a bundle. A tracker is a different system with a
  different contract, and it belongs on the outside of that seam, not inside
  `fetch`.
- **Agents and shells already solve it.** A coding agent, a CI step, or an
  operator can put any tracker's issue text on disk and pass `--spec`. That path
  is already capped, fenced as untrusted, and enumerated into the spec
  checklist, so external text arrives with the same guarantees as any other
  source and no new credential in the core. If demand shows up later, a generic
  external-resolver hook is the extension point to design — not a vendor SDK in
  `internal/`.

## Consequences

- `--issue` resolves forge-native references only; a tracker identifier or URL
  refuses `issue-invalid` with the accepted shapes in the message.
- A tracker's issue text reaches a review as a `--spec <file>` source, or as a
  file the agent writes and hands over. It counts toward the same caps and
  carries the same `trust: "untrusted"` mark.
- Automatic discovery stays GitHub's in this build; a non-GitHub PR with a
  closing keyword warns `issue-unavailable (unsupported)` and keeps its bundle.
- Adding a tracker read is a deliberate design decision, not a flag: it would
  need its own ADR, its own credential contract, and its own eval.
