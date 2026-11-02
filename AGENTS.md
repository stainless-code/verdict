# House rules for the verdict repository

The shipped prompts stay repo-agnostic so reviews of other projects carry no
verdict conventions. Repo-specific rules live here, where any agent working in
this checkout reads them.

## Git and commits

- Never push to protected branches (`main`, `master`, `production`, `develop`,
  `staging`); work on `agent/<issue>-<kebab>`.
- Never add `Co-authored-by` trailers.
- One commit per change; leave unrelated files untouched.

## Formatting

- Before commit or push, run `bun fmt --check` on changed files when Bun is
  available; fix only non-example hunks.
- Never reflow `prompts/` examples — see `README.md` § Formatting.
- `gofmt -l .` must be empty, and shell files gate on `shellcheck -S warning`
  (see `hooks/pre-commit`).

## Prompt style

- Use emdashes for sentence breaks.
- Use backticks liberally for inline code, even in headers.
