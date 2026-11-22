# Actions pinned to mutable refs

A tag or branch can be repointed. `uses: owner/action@v4` runs whatever `v4`
points at when the job starts, not the code reviewed in the pull request. A
full 40-character commit SHA is the only immutable pin.

## When to flag

- `uses: owner/action@v4`, `@v1`, `@main`, `@master`, `@latest`, or any tag
  or branch name that is not a 40-hex commit SHA, for a remote action or
  reusable workflow.
- A `docker://image:tag` action without a digest; use
  `docker://image@sha256:<digest>`.
- A reusable workflow call (`uses: owner/repo/.github/workflows/x.yml@v1`)
  pinned to a tag or branch.
- An action whose version is set by a variable or input the workflow does
  not pin (`@${{ inputs.version }}`).

## Do NOT flag

- A local action (`uses: ./.github/actions/setup`) or a composite action in
  the same repository: it is the reviewed checkout, not a remote mutable ref.
- A 40-character SHA, with or without a `# v4` comment, including a
  first-party `actions/*` action pinned that way.
- A `docker://` reference with a `@sha256:` digest.
- An action pinned through a Dependabot-maintained SHA bump, where the diff
  only changes the SHA and its comment.
- A workflow that runs on `push` only in a repository with no untrusted
  contributors, if the finding is only the tag; report the pin when the
  action runs with secrets or write permissions or on a PR trigger.

## Evidence

Quote the `uses:` line and the ref. Name what the upstream can change under
that ref and what the workflow grants it.
