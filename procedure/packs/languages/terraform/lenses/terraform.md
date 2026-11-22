# Terraform lens

Dispatch only when the diff changes a stateful resource, a secret's storage,
or an access grant. Frame one falsifiable question — what does the next plan
replace, where does this secret land, how wide is this grant — and give it a
short hypothesis label.

Read the changed modules and their variables; the on-disk diff is
authoritative scope. Scope the question to at most ~12 files, cite NEW line
numbers, and name the resource address.
