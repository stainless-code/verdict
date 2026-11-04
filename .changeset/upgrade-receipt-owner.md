---
"@stainless-code/verdict": patch
---

`verdict upgrade` no longer treats an installer receipt as proof of ownership
when a package manager already owns the directory. A `.verdict-installer` file
beside a binary in an npm global root, a Homebrew Cellar, a project's
`node_modules`, or a skill copy used to flip the channel to `installer-managed`,
so `upgrade` planned a self-replace of a file the manager tracks and `uninstall`
would delete it. Those copies keep their own channel and delegate or refuse
with the owning command, as the channel table in the manual already promised.
An explicit `VERDICT_INSTALL` directory is unchanged: a receipt there still
means the installer owns the copy.
