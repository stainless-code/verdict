# Untrusted expressions interpolated into shell steps

`${{ }}` is substituted into the `run:` script before the shell parses it. A
PR title, branch name, or comment body containing `$(...)`, backticks, or
`;` becomes shell code in the runner.

## When to flag

- `${{ github.event.pull_request.title }}`, `.body`, or `.head.ref` inside a
  `run:` block; the branch name is attacker-chosen and may contain shell
  metacharacters.
- `${{ github.head_ref }}`, `${{ github.event.issue.title }}`/`.body`,
  `${{ github.event.comment.body }}`, `${{ github.event.review.body }}`, or
  `${{ github.event.discussion.* }}` interpolated into a command.
- Commit messages, `github.event.head_commit.message`,
  `github.event.commits.*.message`, and `github.event.workflow_run.head_branch`
  interpolated into `run:`.
- `${{ github.event.inputs.* }}` and `${{ inputs.* }}` from
  `workflow_dispatch`/`workflow_call` spliced into a command without passing
  through `env:`.
- The same untrusted value interpolated into `actions/github-script`'s
  `script:` or a shell built through `eval`, where the value becomes code in
  another interpreter.

## Do NOT flag

- An expression in `if:`, `with:`, `env:`, or `permissions:`: those are
  evaluated by Actions, not by the shell. A comparison in `if:` against an
  event field is not shell injection.
- The `env:` indirection pattern: assign the expression under `env:` and
  reference the variable as `"$PR_TITLE"` in `run:`. That is the fix; do not
  flag it because the value still appears in the workflow.
- Trusted expressions: `github.sha` (40 hex), `github.repository`,
  `github.repository_owner`, `github.event_name`, `runner.os`,
  `github.run_id`, and matrix values the workflow itself defines.
- A value only passed to a quoted `printf` with a fixed format, not
  evaluated.
- `${{ secrets.X }}` in `run:` as a secret-exposure question: it is not
  injection. Report the leak separately only if it is printed or passed to
  an untrusted process.
- A shell script that receives the expression as an `env:` value and passes
  it through `shlex`/quoting before use, with the quoting visible.

## Evidence

Quote the `run:` line and the expression. Name the field an attacker
controls and the command their payload becomes.
