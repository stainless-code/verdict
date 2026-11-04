---
"@stainless-code/verdict": patch
---

Every release now publishes `commit.txt` — the commit SHA the tag points at and
the certificate's workflow-SHA claim — and `install.sh`, `install.ps1`, and
`verdict upgrade` pin `cosign verify-blob` to it with
`--certificate-github-workflow-sha`; the `setup-verdict` action inherits the pin
through `install.sh`. The identity and issuer alone accept any run of the
Release workflow, so a bundle from a different build of the same workflow used
to satisfy the check; the pin makes that a refusal. A release that publishes no
`commit.txt`, or a `commit.txt` that is not a full 40-character SHA, is refused
with `verify-failed` instead of being installed unpinned.
