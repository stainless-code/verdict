---
"@stainless-code/verdict": patch
---

Bind every credential-bearing provider read to the configured origin.
`fetch` refuses a Bitbucket pagination `next`, a Forgejo metadata `diff_url`,
or an Azure metadata `remoteUrl` whose scheme and host differ from the API
base, instead of sending the token or PAT to that host. Bitbucket paging also
stops at a page cap or a repeated cursor and warns that the bundle may be
incomplete.
