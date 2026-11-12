---
"@stainless-code/verdict": patch
---

`scripts/release.sh` no longer reports a shipped release asset as missing. The
completeness check piped the asset list into `grep -q`, which exits at the first
match; the writer then took SIGPIPE, `pipefail` failed the pipeline, and the
asset landed in the missing list — so a complete release could refuse its own
body ("release v0.6.1 is not complete (missing: …)"). The race lost only under
load, which is why it showed up in a parallel eval run. The membership test is
now a bash whole-line match with no producer that can break, and the
`release-body` eval pins it with an asset list over 64 KiB whose first entry
must still be found.
