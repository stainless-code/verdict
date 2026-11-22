# Terraform lens

Dispatch only when the diff changes a stateful resource, a secret's storage,
an access grant, a network exposure, or an encryption setting. Frame one
falsifiable question — what does the next plan replace, where does this
secret land, how wide is this grant, who can reach this port — and give it a
short hypothesis label.

Read the changed modules, their variables, and the provider version; the
on-disk diff is authoritative scope. Do not claim a replacement from HCL
alone when the provider's schema decides it: cite the plan's `forces
replacement` marker or the provider's ForceNew attribute. Scope the question
to at most ~12 files, cite NEW line numbers, and name the resource address.
