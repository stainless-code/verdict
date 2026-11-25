---
"@stainless-code/verdict": patch
---

`verify --sandbox` now protects nested git repositories. Before the command runs it finds every nested `.git` in the reviewed root (a submodule with its own git directory, a nested clone) and, on Linux, every submodule path in the index, and makes each read-only, so a sandboxed command can no longer plant a hook in `sub/.git/hooks`. A nested `gitdir:` file is followed and its git directory protected on both platforms. A reviewed root reached through a symlink is resolved first. **Behavior change:** on Linux, a missing submodule path or an unreadable index (a split index, or over 64 MiB), and on either platform a tree of more than 200,000 directories, now refuses `sandbox-unavailable`.
