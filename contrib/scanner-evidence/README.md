# Scanner evidence pilot

This external adapter is explicitly operator-invoked. It adds candidates to an
existing bundle; Verdict core does not import or discover them. Recipes and lens
packs do not execute this adapter.

Prerequisites: POSIX, Python 3.9+, Git, and an independently installed **Opengrep
2.0.0** executable on PATH. Provision and verify the executable before review.
The adapter refuses other versions. Opengrep is LGPL-2.1, separately installed,
and never bundled with Verdict. The original starter rules have the MIT license
in `rules/LICENSE`; no registry rules are vendored. ast-grep is not used.

Use a separate clean checkout at the bundle's exact head commit, with no
untracked files. Keep the bundle outside that checkout. Run the adapter from a
trusted base checkout so the reviewed change cannot replace its rules or code:

```sh
python3 contrib/scanner-evidence/scan.py /path/to/bundle /path/to/pinned-checkout FULL_HEAD_SHA
```

It writes owner-only `x-scanner.json` and `x-scanner.md`. Give the Markdown to the
review agent explicitly, or copy it outside the bundle and pass that file to a
subsequent `verdict fetch ... --context /path/to/scanner.md`. Context is untrusted
reference material. A rule match is a candidate, never “reproduced”; a clean scan
never means safe. The four starter patterns cover only a few Go/JS/TS hazards,
with false positives expected. Findings outside NEW changed hunks remain visible
with `changed_hunk: false`; OLD-only deleted code is not scanned.

Only the shipped absolute local rule file is used. Metrics and version checks
are disabled; no registry configuration is accepted. Run in an operator-provided
network-denied sandbox for OS-level network enforcement; flags suppress network
features but are not an OS firewall. Environment is scrubbed. Each scanner run
has a 90-second wall limit, 60-second CPU limit, 1 MiB per-stream file limit,
2 GiB address-space limit on Linux, and the engine's
1024 MiB memory / 10-second matching limits (also used on macOS, where Python
cannot set an address-space limit). Version and Git probes have five
seconds. Missing tools, wrong versions, dirty/mismatched trees, scanner errors,
unscanned changed files, malformed output, and limits produce `incomplete` and
exit 1. No supported scanned files also means incomplete. Exit 0 means only that
the bounded scanner invocation completed. Unsupported languages cannot count as
a clean scan. Inspect status even if findings are present.

JSON records the pinned commit, tool/version, rule-file SHA-256, rule ID,
locations, hunk association, fixed rule message/severity, and a withheld trace
marker. Source snippets, metavariables, scanner messages, stderr, and dataflow
content are deliberately excluded rather than attempting to recognize every
secret (the gitleaks-style principle of excluding secret-bearing material).
Paths use a conservative character allowlist; filenames can still be sensitive,
so inspect evidence before sharing. Output caps at 100 candidates; overflow is
incomplete. The checkout must remain immutable while scanning.

Run the offline shell eval with `make build && ./evals/one.sh scanner-evidence`.
It invokes the Python contract test with a stub Opengrep; it does not install or
validate the real engine. Real-engine rule compatibility remains pilot work.

For the S5 acceptance campaign, the orchestrator will run scanner-assisted and
unassisted arms with the same driver, model, input changes, and budgets, keeping
scanner-only results as a separate comparison. Record recall, false positives,
false greens, quote failures, latency, tokens, unsupported coverage, and failures
on held-out defective and clean changes. Include defects these starter patterns
cannot express. This adapter claims no measured improvement and runs no campaign.
