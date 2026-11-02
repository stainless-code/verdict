---
"@stainless-code/verdict": minor
---

Verified binary installers. `install.sh` (macOS/Linux) and `install.ps1`
(Windows) install a pinned `verdict` into `~/.verdict/bin` after verifying the
cosign sigstore bundle and the archive SHA-256, then atomically replace the
target; missing cosign warns and verifies checksums only, and
`VERDICT_REQUIRE_SIGNATURE=1` fails closed. PATH is edited only by appending
one marked line (with `VERDICT_NO_MODIFY_PATH=1` / `--no-modify-path` opt-outs),
and offline/local-file installs and uninstall are supported. **Breaking for
0.3.x users:** the old checkout-only skill linker moved to `install-skill.sh`;
update `curl ... | sh` invocations of the old script and any docs links.
