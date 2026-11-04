---
"@stainless-code/verdict": minor
---

`verdict` is one static binary — no checkout, no `jq`, no shell script. Install
it with the verified `install.sh` (macOS/Linux) or `install.ps1` (Windows) into
`~/.verdict/bin`, the Homebrew tap, or the npm wrapper; both installers verify
the cosign signature and the archive SHA-256, smoke-test the installed binary
from its final path, and add `PATH` with one marked line
(`VERDICT_NO_MODIFY_PATH=1` opts out). In CI the new `setup-verdict` action
installs a pinned, verified copy on Ubuntu, macOS, and Windows runners without
touching shell rc files. `verdict upgrade` updates whichever channel installed
it — a verified self-replace for installer installs, the owning package
manager's global update for npm and Homebrew — and `verdict uninstall` removes
only installer-owned files; both refuse, naming the exact command, when the
install belongs to something else. `verdict skill` prints or installs the
payload embedded in the binary (`SKILL.md`, `prompts/`, `schemas/`, and the
`scripts/verdict` shim), so the served skill can never skew from the binary.

**Upgrading from 0.3.x:** the old script install is replaced, not patched.
`scripts/review.sh` is gone — every verb is `verdict <verb>` with the same
flags and `--json` envelopes. The old symlink-only `install.sh` became
`install-skill.sh` (checkout-only), and the copy-based `install` verb is gone,
so reinstall the skill with `verdict skill install` or `npx skills add`. Docs
and scripts that call the old script path must be updated. Start at the
rewritten quickstart, the new glossary of review terms, the CI recipe in
`docs/ci-recipe.md`, and `verdict --help`, which now names each verb's
provider scope.
