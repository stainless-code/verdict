# Destructive commands whose path can resolve elsewhere

A path built from a variable is only safe when the variable is non-empty and
the path cannot climb out of its directory. An empty directory variable turns
`rm -rf "$DIR"/*` into a delete of `/*`.

## When to flag

- `rm -rf "$DIR"/*`, `rm -rf "$DIR"/`, or any delete whose directory comes
  from a variable with no `${DIR:?}`/`[ -n "$DIR" ]` guard; an empty `DIR`
  leaves `/` or `/*` as the target even when quoted.
- `cd "$dir"` with no `|| exit 1` (or `|| return 1`) in a script that then
  runs relative destructive commands, and no `set -e` that would abort.
- `rm -rf .` or `rm -rf *` after a `cd` that can fail.
- A destructively expanded `find ... -delete` or `rsync --delete` whose
  target comes from an unvalidated variable.
- A path that can contain `..` and is joined without a containment check
  before deletion.

## Do NOT flag

- `cd "$dir" || exit 1` (or `|| return 1`) in a function.
- `cd "$dir"` under an active `set -e`/`set -o errexit`, where a failed `cd`
  aborts the script; the shell's own control flow is the guard.
- A delete of a fixed literal path or a path under a fixed, non-empty root
  (`rm -rf /var/lib/myapp/cache/*`).
- `rm -rf -- "${DIR:?}"/*` with the required-variable guard: it aborts on an
  empty or unset value.
- A `mkdir -p`/copy that is not destructive, even with an unquoted path.
- A test fixture that deletes its own `mktemp -d` directory.

## Evidence

Quote the `cd`/variable and the destructive command. Name the empty or
traversal value that changes the target and what it deletes.
