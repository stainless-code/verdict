---
"@stainless-code/verdict": patch
---

`docs/troubleshooting.md` lists every refusal code the binary can emit, each
with its cause and its fix. A drift test fails the build when a code ships
without an entry (or an entry names a retired code), and the manual's
refusal section links the page.
