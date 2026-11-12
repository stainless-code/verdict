---
"@stainless-code/verdict": patch
---

`verdict skill install` no longer half-installs when a target cannot be written.
On POSIX exactly the directories an install will write are probed before the
first byte, so a read-only skills dir anywhere in a `--detect` set refuses with
`skill-install-failed`, names that target, and leaves the earlier targets
absent instead of already installed. A target that changes between that probe
and the write still fails, at write time. Windows reports that failure at write
time only, because its writability probe is a stat. A receipt path that cannot
be replaced refuses with `skill-foreign-conflict` the same way, before the
payload lands. A single-target install refuses on the same probe, and a
repeated `--agent` dedupes to one install per target instead of refusing.
