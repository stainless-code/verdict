Report a literal secret in HCL or an attribute that stores one in plaintext
state: `password`, `secret`, `token`, `connection_string`, or a `sensitive =
true` output that still lands in `tfstate` and every state backend export.
