# Planned replacement or destruction of stateful resources

Some attribute changes do not update a resource; the provider destroys it and
creates a new one. On a database, volume, or queue that is data loss, not a
deployment.

## When to flag

- A change to a ForceNew attribute on a stateful resource: `engine`,
  `identifier`, or `db_name` on `aws_db_instance`, an EBS volume's
  availability zone, a queue's name, or a bucket whose name is replaced.
  The Terraform plan shows `# forces replacement`; cite that or the
  provider schema, not a guess.
- `force_destroy = true` on a bucket, `skip_final_snapshot = true` or
  `delete_automated_backups = true` on a database, or `force_delete` on a
  repository: the destroy path deletes data that a rollback needs.
- Removing a `lifecycle { prevent_destroy = true }` block, or adding a
  resource that replaces one whose state the change does not migrate.
- Changing a `count`/`for_each` key on stateful instances so Terraform
  addresses a different instance (the old one is destroyed).
- A restored or renamed module/address without `moved`/`import` blocks, so
  the plan replaces instead of renaming.

## Do NOT flag

- A replace of a stateless resource: a security group, a Lambda function, an
  IAM role, a DNS record with a short TTL, a stateless container task.
- A stateful replace with a documented migration plan in the diff: a
  snapshot/restore or replication step, or a `moved` block that preserves
  state. `create_before_destroy` alone avoids an outage gap but does not
  migrate data.
- A change to a stateful resource's tags, description, or another updatable
  attribute.
- `force_destroy` on a bucket the same configuration creates as a temporary
  scratch bucket and destroys in the same lifecycle.
- A `replace_triggered_by` that intentionally recreates an ephemeral,
  stateless resource.
- Any replacement claim made from HCL alone when the provider schema marks
  the attribute updatable. Verify before flagging.

## Evidence

Quote the attribute and the resource address. Cite the plan line
(`# forces replacement`) or the provider's ForceNew contract, and name the
data or availability lost.
