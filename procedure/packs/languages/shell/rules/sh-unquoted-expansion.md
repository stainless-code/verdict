# Unquoted expansions in commands

An unquoted expansion is split on IFS and glob-expanded before the command
runs. In a destructive command that changes which paths or arguments it
receives; an empty value can widen the target set.

## When to flag

- `$var`, `$@`, `$*`, or `$(...)` unquoted as an argument to `rm`, `mv`,
  `cp`, `chmod`, `chown`, `chgrp`, `find ... -delete`, or `rsync --delete`.
- An unquoted expansion in a `for` head (`for f in $files`) where the list
  is built from input and the body deletes or moves the results.
- An unquoted command substitution used as a path, a hostname, or a git ref.
- A glob built from an unquoted variable (`rm -rf $DIR/*`) where an empty
  `DIR` leaves `/*`.

## Do NOT flag

- Quoted expansions: `"$var"`, `"$@"`, `"$(cmd)"`, including `${var:?}`.
- An assignment, including an assignment argument to a declaration builtin
  (`x=$1`, `local x=$1`, `export x=$1`, `readonly x=$(cmd)`): bash treats
  those as assignment contexts and does not split or glob the right-hand
  side. `cmd $1` is the splitting form.
- Expansions inside `[[ ... ]]` or a `case` word where the shell does not
  perform field splitting on the tested string.
- Arithmetic contexts (`$(( ... ))`) and `$(())` values.
- A deliberate word-splitting loop over a controlled, documented list, such
  as `for f in $DEFAULT_FLAGS`, where the value is a module constant and not
  input.
- An unquoted `$?`/`$$` in a numeric context.

## Evidence

Quote the command and the expansion. Name the split or glob that changes the
target, and say whether an empty value widens it.
