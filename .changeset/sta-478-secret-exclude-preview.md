---
"@stainless-code/verdict": minor
---

`fetch` and `fetch-local` now withhold built-in secret paths unconditionally: `.env`
and its variants, private keys (`*.pem`, `*.key`, `id_rsa` and the other `id_*`
names, `*.ppk`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`), credential documents
(`credentials.json`, `.netrc`, `.npmrc`, `.pypirc`, `.envrc`, `.pgpass`,
`.htpasswd`, `kubeconfig`), and Terraform state (`*.tfstate`, `*.tfstate.*`). The
set is `default:secret` with reason
`secret-builtin`. No `.verdict/ignore` negation, no `--ignore`, and no
`--no-default-excludes` can put one back: the exclusion is applied before every
other rule and probes both sides of a rename, so renaming `.env` to `notes.txt`
cannot bypass it. Matching folds ASCII case and cleans the path, so `Server.PEM`
and `.Env.local` are withheld like their canonical spellings, while the
`.env.example`, `.env.sample`, `.env.template`, `.env.dist`, and `.env.defaults`
templates ship key names rather than values and stay reviewable.
`excluded.json` always lists `default:secret` in `defaults`, including under
`--no-default-excludes`.

`fetch --preview` and `fetch-local --preview` print the exact reviewed file list,
every withheld section with its rule and reason, the included and excluded byte
totals, and the review budget tier the deterministic classifier resolves, then
stop. A preview writes no bundle directory, starts no agent, and reuses the same
exclusion rules and `budget.Classify` decision the run itself uses, and measure
the filtered diff rather than the PR's file list, so a withheld path is reported
as withheld and never as reviewed. `--json` carries the same payload under
`data.preview`, typed by `previewData` in `schemas/envelope.schema.json` and the
generated TypeScript types.
