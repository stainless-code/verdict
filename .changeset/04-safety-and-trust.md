---
"@stainless-code/verdict": patch
---

**Safety and trust**

- **Tokens off the argv** — Forgejo and Bitbucket reads hand the credential to curl on stdin or a 0600 file, so it is gone from `ps` and `/proc/<pid>/cmdline`. [Providers](docs/manual.md#providers-non-github-hosts) <!-- changesets: curl-token-argv.md -->
- **Secret guard** — `post` refuses a secret-shaped value before the first host call, naming the shape and never the value, with `--allow-secret-values` as the explicit exception. [Guarded submission](docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-secret-guard.md -->
- **Mentions neutralized** — `record` escapes `@`-mentions once, where the body is built, so recorded, posted, dry-run, and digested bytes cannot diverge. [Mentions](docs/manual.md#mentions-in-the-published-text) <!-- changesets: mention-neutralization.md -->
- **Punctuated mentions** — `(@alice)`, `**@alice**`, and `_@alice_` are escaped while real addresses stay byte-identical. [Mentions](docs/manual.md#mentions-in-the-published-text) <!-- changesets: punctuated-mentions.md -->
- **Strikethrough mentions** — `~~@alice~~` is escaped too, while a lone tilde in prose stays as written. [Mentions](docs/manual.md#mentions-in-the-published-text) <!-- changesets: strikethrough-mentions.md -->
- **Learning evidence** — cited paths and comment identifiers must stay inside quoted learning evidence, and unsafe ones refuse before a learning is proposed or accepted. [Learnings](docs/manual.md#learnings-durable-repo-memory) <!-- changesets: learnings-evidence.md -->
- **Unicode line breaks** — U+2028 and U+2029 now refuse a serve URL, refuse a learning path, and flatten in a rendered `pack test` line. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: line-break-guards.md -->
- **Store locking** — every `.verdict/kb.jsonl` write takes one advisory lock; a second writer refuses `store-busy` instead of dropping a row. [Findings store](docs/manual.md#findings-store--triage-authority) <!-- changesets: store-lock-and-listing.md -->
- **Enforced sandbox** — `verify --sandbox` runs the verify command under bubblewrap on Linux with no network, a private PID namespace, empty `~/.ssh`/`~/.aws`/`~/.docker`, and no silent fallback. [Enforced sandbox](docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox.md -->
- **macOS sandbox** — `verify --sandbox` now denies every `bind()` and `connect()` on macOS through the built-in `sandbox-exec`, loopback and unix sockets included, so filesystem isolation stays Linux-only. [Enforced sandbox](docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox-macos.md -->
- **Variable command words refuse** — `E=rm; $E -rf ./target` and the braced, quoted, `export`ed, loop, concatenated, and own-line spellings now refuse `verify-indirection`, including after `env`/`timeout`/`xargs` wrappers and inside `sh -c`. [Known residuals](docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-indirection.md -->
- **`.verify.out` is owner-only** — the verify stream is written `0600` on Unix, like `.run.out`, because a command that prints a credential leaves the value in it. [Enforced sandbox](docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-out-owner-only.md -->
- **Launch-record redaction** — a secret-shaped `run --agent` operand no longer reaches the recorded `output_tail` or the 0644 child log. [What `run` asserts](docs/manual.md#what-run-asserts) <!-- changesets: run-argument-credential.md -->
- **setup-verdict hardening** — the action passes its inputs through environment variables and verifies a cached archive before reuse, failing closed on a tampered one. [The runner is your CI](docs/manual.md#the-runner-is-your-ci) <!-- changesets: setup-verdict-hardening.md -->
- **Install preflight** — `skill install` probes exactly the directories it will write before the first byte, so a read-only target leaves nothing half-installed. [Install routes](docs/manual.md#install-routes-and-where-commands-run) <!-- changesets: skill-install-preflight.md -->
