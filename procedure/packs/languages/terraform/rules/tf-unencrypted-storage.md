# Storage or state without encryption

Data at rest is encrypted by the backend default in some services and not in
others. An explicit disable, or a Terraform state bucket that stores secrets
in plaintext, is a durable exposure independent of the application.

## When to flag

- `storage_encrypted = false` on `aws_db_instance`, `encrypted = false` on an
  EBS volume or snapshot, `at_rest_encryption_enabled = false` on
  ElastiCache/Redshift, or `server_side_encryption { enabled = false }` on a
  DynamoDB table.
- A Terraform S3 backend with `encrypt = false`, or a state bucket/table
  whose only copy is unencrypted and holds secrets.
- `aws_s3_bucket_server_side_encryption_configuration` explicitly set to no
  algorithm, or a bucket policy that denies encrypted transport but permits
  plaintext object writes where the diff controls it.
- A volume or snapshot copied/restored with encryption explicitly disabled.

## Do NOT flag

- An S3 bucket with no `server_side_encryption_configuration` block: AWS has
  applied SSE-S3 by default to new objects since January 2023. Report a
  missing customer-managed key as a key-control question only when the data
  classification requires it; do not claim the bucket is unencrypted.
- `sse_algorithm = "AES256"`: SSE-S3 is encryption. It is not a KMS CMK, but
  it is not plaintext.
- A state backend with `encrypt = true` (the S3 backend default) and no
  `kms_key_id`; the object is still encrypted with SSE.
- An ephemeral scratch volume or test fixture with no sensitive data, where
  the diff shows the lifecycle.
- A managed service whose encryption is enabled by default and whose schema
  has no explicit disable in the diff.
- A KMS key policy change that only scopes key administration.

## Evidence

Quote the setting and the resource. Name the data at rest, the backend that
stores it in plaintext, and who can read it.
