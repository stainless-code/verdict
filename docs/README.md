# docs/ — the long-form pages

One line per page, so a page cannot sit in this folder and be missing from the
index. `apps/docs/scripts/check-docs.ts` fails the docs build when a page here
has no line below, when a line names a file that is gone, when a page's surface
and the built site disagree (`apps/docs/navigation.ts` is the site's one ordered
list), and when a link in this folder or the root `README.md` points at a file
or heading that no longer exists.

The two sections are the two surfaces a page has. Text lives here either way:
publishing a page adds a route in `apps/docs/sources/`, not a second copy.

## Published on the site

- [Refusal codes](troubleshooting.md): every refusal code the binary can emit, with its cause and its fix, kept in sync with the registry by the archtest guard.

## Repository-only

- [Manual](manual.md): the operator contract: the --json envelope, exit codes, store files, refusal codes, control files, model wiring, and every provider.

- [Adding a verb](adding-a-verb.md): the five surfaces one command reaches, and the two allowlists that decide where it may stop.
- [Benchmark corpus](benchmark.md): how the measured review rates are built and graded offline, and what the comparison page may cite.
- [CI and test rules](ci-and-tests.md): the skip-token ban on commit messages, and the rules that keep the suite deterministic.
- [CI recipe](ci-recipe.md): the copy-paste workflow that runs fetch, review, gate and post on your own runner.
- [Typed configuration](config.md): strict parsing, cascading defaults, scoped plans, captured provenance, and configuration explain/preview.
- [Glossary](glossary.md): the vocabulary the help, prompts and reports use, grouped by where you meet it.
- [Go KB index](go-kb/README.md): what the knowledge base is, and where the `go-*` skills embed a copy of it.
- [Go knowledge base](go-kb/knowledge-base.md): the 131 cards the `go-*` skills are built from: Rule, Why, Enforce, Since, Sources.
- [Go rules](go-rules.md): the adopted rules, one per line with their `GO-<TOPIC>-<NN>` IDs and enforcement markers.
- [Landing claims](landing-claims.md): every factual claim on the home page, and the manual line that checks it.
- [Policy contract](policy-contract.md): requirement gates, evidence, consent, privacy constraints, and publication outcomes.
- [Reactions and rule trends](reactions-trend.md): `calibrate --reactions` proposals from votes on posted comments, and `trend --rules` per-rule acceptance and false-hit rates.
- [Vision](vision.md): what Verdict is, what it refuses to be, and where its craft comes from.
