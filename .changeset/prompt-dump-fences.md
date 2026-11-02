"@stainless-code/verdict": minor
---

Nonce-fenced prompt dumps. `--show-prompts` (and `data.prompt_dump`) now wraps every untrusted bundle section in a per-run nonce fence (`<<<UNTRUSTED nonce=<16 hex> source=<file>>>` … `<<<END UNTRUSTED nonce=<same>>>`) named in a trusted trust-boundary prologue; only fences carrying that nonce delimit untrusted data, so forged closes smuggled in payloads stay contained as evidence. Thread files stay excluded by design (the dump is the initial-review reading list; threads are read live on the incremental path). Section content bytes are otherwise untouched.
