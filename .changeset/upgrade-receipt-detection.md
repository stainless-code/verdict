---
"@stainless-code/verdict": patch
---

`verdict upgrade` recognizes an install made with `VERDICT_INSTALL` even after
the variable is gone, by reading the installer receipt beside the running
binary. A receipt is a hint, never proof of ownership: when npm, Homebrew, a
project's `node_modules`, or a skill copy already owns the directory, `upgrade`
and `uninstall` keep that channel and delegate or refuse with the owning command
instead of replacing or deleting a file the manager tracks.
