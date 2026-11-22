# Secrets in configuration, state, or unsanitized outputs

Terraform state holds every attribute a provider returns, and a `.tf` literal
is committed to the repository. Marking an output `sensitive` hides it from
CLI output; it does not remove it from state.

## When to flag

- A literal password, token, API key, private key, or connection string in a
  `.tf` or `.tfvars` file, or in a `default` value of a variable that carries
  one.
- A secret assigned to a resource attribute that the provider persists
  (`password`, `secret_string`, `token`, `connection_string`), with no
  secrets-manager reference and no encryption story for the state backend.
- An output that exposes a secret without `sensitive = true`. The output is
  rendered in plan/apply output, CI logs, and any `terraform output` caller.
- A `local_file`/`templatefile` that writes a secret into a file or remote
  object.
- A state backend that disables encryption (`encrypt = false`) for a bucket
  or table holding secrets.

## Do NOT flag

- `sensitive = true` on an output: that is the minimum masking fix. It is not
  sufficient to remove the value from state; report the state exposure as a
  separate concern only when the diff also shows a plaintext backend or a
  literal, not as a contradiction of the masking.
- A variable marked `sensitive = true` fed from `TF_VAR_*`/CI secrets with no
  literal in the repository.
- A reference to a secrets manager data source or an ephemeral/write-only
  argument (Terraform 1.10+) where the value is not persisted in state.
- A non-secret placeholder in `.tfvars.example`, a test fixture, or a
  documented dummy value.
- A provider-generated value that is already sensitive by schema, unless the
  diff writes it into an unsanitized output or a file.
- A public identifier (a key ID, ARN, or bucket name) that is not itself a
  credential.

## Evidence

Quote the literal or output and name the state backend or log that exposes
it. Say whether the finding is the repository literal, the state
persistence, or the unsanitized output.
