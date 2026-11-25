---
"@stainless-code/verdict": minor
---

New in the repository (not the binary): `contrib/scanner-evidence/`, a pilot adapter you run yourself to add Opengrep candidates to a bundle. `python3 contrib/scanner-evidence/scan.py <bundle> <checkout> <head-sha>` scans a clean checkout at the bundle's head with the shipped starter rules only (Opengrep 2.0.0, no registry rules, metrics and version checks off), and writes `x-scanner.json` and `x-scanner.md` into the bundle. Hand the Markdown to the review, or pass it as `--context`. A match is a candidate, never `reproduced`, and a clean scan never means safe. A missing or wrong-version tool, a dirty tree, a scanner error, a limit hit or an unscanned changed file writes `status: incomplete` and exits 1. Evidence records rule ids, locations and hunk association, never source text. Opengrep is LGPL-2.1, installed separately and never bundled.
