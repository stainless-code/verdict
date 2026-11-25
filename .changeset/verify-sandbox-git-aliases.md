---
"@stainless-code/verdict": patch
---

`verify --sandbox` closes more ways a reviewed checkout could reach its git control files. Whether a git directory lies inside the reviewed root is now decided by file identity, not spelling, so a `.git` pointer or `commondir` that spells the root in another case or through the `/System/Volumes/Data` firmlink is refused. A symlinked pointer or `commondir` is refused before it is read, links resolve one hop at a time, and a git directory that is or contains the reviewed root is refused. A control file with extra hard links, or that is a symlink into the root, is refused at setup; the object files `git clone --local` hardlinks and links under `.git/hooks/` stay allowed. The manual and SECURITY.md now note that on macOS a sandboxed command can still read the environment of other non-hardened processes you run.
