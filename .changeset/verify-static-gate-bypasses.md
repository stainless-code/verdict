---
"@stainless-code/verdict": patch
---

The verify pre-exec gate closes twelve static bypasses. `wget --remote-name`,
`wget -i`, `curl -K`/`--config`, `busybox wget`, `pip3 download <url>`,
`telnet`, curl's `telnet://` scheme, and `git archive --remote` now refuse as
`verify-networked`; a secret-shaped variable from the shared `envsecret`
classification in a networked command (`curl .../$AWS_KEY`,
`curl -H "X: $AWS_KEY"`) refuses as `verify-credential-read`; and
`git --exec-path` plus `tar -I` refuse as `verify-destructive`. Every refusal
keeps the quoted fragment and `try:` rewrite hint. The documented boundary is
unchanged: a plain `curl` GET, `/tmp` scratch writes, and non-secret variables
in URLs still execute, and the adversarial corpus rows that pinned each gap
move from `xfail` to `strict` with benign neighbours beside them.
