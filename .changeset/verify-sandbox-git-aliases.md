---
"@stainless-code/verdict": patch
---

`verify --sandbox` closes more ways a reviewed checkout could reach its git
control files. Whether a git directory lies inside the reviewed root is now
decided by file identity, not spelling, so a `.git` pointer or `commondir`
that spells the root with another letter case or through the
`/System/Volumes/Data` firmlink is refused instead of escaping the deny rules.
A symlinked `commondir` or `.git` pointer is refused before it is read; links
are resolved one hop at a time; a git directory that is, or contains, the
reviewed root is refused; and a git control file with more than one hard link
(for example `cfg` hardlinked to `.git/config`) is refused at setup, while
the object payload `git clone --local` hardlinks (loose objects, packs, and
the commit-graph and pack-list caches, submodule git directories included)
stays allowed; `objects/info/alternates` is checked like any control file. A control file that is a symlink into the
reviewed root (for example `.git/config -> ../cfg`) is refused too; links
under `.git/hooks/` stay allowed, since hook code in the tree is your choice. The manual and
SECURITY.md now document that on macOS a sandboxed command can still read the
environment of other non-hardened processes you run via `kern.procargs2`.
