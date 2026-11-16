---
"@stainless-code/verdict": minor
---

**What's new in 0.7.0**

Verdict 0.7.0 is a breadth release. It adds a local code index, a read-only `serve` surface with a browser
UI, and a backtest harness that mines past bug fixes from your own history, replays them against your
agent, and scores the result. Review output gains an auditable provenance receipt, diff-coverage
attestation, advisory risk scoring, and an optional validation pass that floors each finding on the
evidence behind it, while the CI gate now checks coverage by default. Posting grows a secret guard, honest
per-host capability reporting, and a summary-comment option, GitLab and Forgejo join GitHub as full publish
targets, and lens packs, declared recipes, and reported verification make the review pipeline itself data.
Thirteen pre-1.0 changes alter behavior — each names the migration step below — and the known limits close
the notes.
