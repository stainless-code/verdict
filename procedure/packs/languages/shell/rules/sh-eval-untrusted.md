# eval on untrusted data

`eval` reparses its argument as shell syntax. A value from an environment
variable, a file, a command-line argument, or a downloaded response becomes
code with the script's privileges.

## When to flag

- `eval "$var"` or `eval "$(cmd)"` where the value or the command's output
  comes from outside the script: a request field, a CI input, a config file
  another process can write, or a network response.
- `bash -c "$var"` / `sh -c "$cmd"` with an interpolated untrusted value.
- `eval` used to build a command from argument names or values
  (`eval "rm -rf $target"`), even when the author intended only variable
  indirection.
- `source`/`.` of a path supplied by input, or a script downloaded and then
  sourced.
- A shell function that receives a command string and evals it, called with
  user input anywhere in the diff.

## Do NOT flag

- `eval` of a fixed string built only from module constants and quoted
  variables that were validated against an allowlist.
- `bash -c 'fixed script'` with no interpolation.
- `source ./lib/common.sh` of a repository-owned file with a fixed relative
  path.
- An `eval` in a test harness that evaluates a fixture the test itself wrote,
  with no external input path.
- An indirect expansion (`${!name}`) or an array lookup used instead of
  `eval`; that is data, not reparsed code.
- A script that constructs a command in an array and runs it
  (`cmd=(rm -rf -- "$target"); "${cmd[@]}"`); no reparsing happens.

## Evidence

Quote the `eval`/`-c` and the input path. Name the string an attacker
controls and the command it becomes.
