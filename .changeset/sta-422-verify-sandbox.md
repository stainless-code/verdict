---
"@stainless-code/verdict": patch
---

`verify --sandbox` on macOS now denies reads and writes to every credential path in the fixed table under `HOME` (both the literal path and its realpath) and to the shared host temp roots (`/tmp`, `/private/tmp`, `/var/tmp`, `/private/var/tmp`, and the host `TMPDIR`), and gives each run a private `0700` `TMPDIR`/`TMP`/`TEMP` removed when it ends while the reviewed root stays writable. Quotes and backslashes are escaped into the Seatbelt profile; a path with a control character refuses `sandbox-unavailable`. This is Seatbelt policy, not a mount namespace: the rest of the host filesystem stays visible, and a Linux VM with `bwrap` remains stronger.

Linux hosts without bubblewrap can now opt into `verify --sandbox-network-only`, which probes and runs `unshare --user --map-root-user --net`. It isolates the network only — no mount, filesystem, or PID isolation — warns on every run, and records `sandboxMechanism: "unshare-net"` in `patch.json`, the deep-group receipt (`sandbox_mechanism`), the MCP tool argument (`sandboxNetworkOnly`), and `--json` data (`sandboxMechanism`). `--sandbox` still refuses `sandbox-unavailable` rather than downgrade, the new flag refuses on non-Linux hosts, and passing both refuses `bad-flags`.
