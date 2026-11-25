---
"@stainless-code/verdict": minor
---

**Breaking:** `fetch` and `fetch-local` now always withhold built-in secret paths: `.env` and its variants, private keys (`*.pem`, `*.key`, `id_*`, `*.ppk`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`), credential files (`credentials.json`, `.netrc`, `.npmrc`, `.pypirc`, `.envrc`, `.pgpass`, `.htpasswd`, `kubeconfig`) and Terraform state. The rule is `default:secret` (reason `secret-builtin`). It runs before every other rule, checks both sides of a rename, and ignores case. No `.verdict/ignore` negation, `--ignore` or `--no-default-excludes` brings a path back, so a PR that changes only such files now reviews nothing for them. Templates (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`, `.env.defaults`) stay reviewable.

`fetch --preview` and `fetch-local --preview` print the exact reviewed files, every withheld section with its rule, the byte totals and the review budget tier, then stop without writing a bundle or starting an agent. `--json` carries the same as `data.preview`.
