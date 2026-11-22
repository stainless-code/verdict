Report an expression that becomes shell code: `${{ github.event.* }}`,
`github.head_ref`, or a PR title, body, or branch name interpolated directly
into `run:`. It must pass through `env:` and be quoted.
