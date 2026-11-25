---
"@stainless-code/verdict": patch
---

`verify --sandbox` now protects nested git repositories. Before the command
runs, it finds every nested `.git` in the reviewed root (a submodule checked
out with its own git directory, a nested clone) and, on Linux, every submodule
path in the root's index. On Linux each is bound read-only, with the
directories above it pinned so none can be moved aside; before, only the
root's own `.git` was, so a sandboxed command could plant a hook in
`sub/.git/hooks` that git ran on the next command in that submodule. A nested
`gitdir:` file is followed and its git directory protected on both platforms,
including on macOS for a root without a `.git` of its own. Patch mode now
resolves a reviewed root reached through a symlink before building the
sandbox, as group mode already did. On Linux, a submodule path that is
missing, an index that cannot be read (a split index, over 64 MiB), or a tree
of more than 200,000 directories (on either platform) refuses
`sandbox-unavailable`. The git-directory scan is also linear in the number of
submodule git directories instead of quadratic.
