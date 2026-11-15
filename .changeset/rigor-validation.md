---
"@stainless-code/verdict": minor
---

Gate a review on evidence rather than on its author's confidence. A trusted
`prompts/validate.md` pass judges each candidate finding — hedge,
reproducibility, anchor, evidence rung, severity — and writes
`.review/<bundle>/validation.json`: one `CONFIRMED`, `REJECTED`, or
`DOWNGRADED` result per sig, with a reason of 10-500 characters. The pass runs
in the invoker, so `verdict` still spawns no model; `post` and `record` read
the file, hash its exact bytes into `validation_hash`, and record
`validation_counts`, `validation_mode`, and `rigor_floors` beside it.

`rigor_policy` in the trusted-base `.verdict/config.json` floors each severity
tier (`mode: warn|gate`, `critical`/`important`/`informational` at
`said|pointed|walked|ran|reproduced`), and `post --require-rigor
<severity>=<rung>` narrows it per invocation, implying gate mode for the named
tiers. Advisory is the default: a rejection warns with the validator's reason
and the finding still posts. In gate mode a rejected finding is withheld
through the D2 receipt (`withheld.source: "validation"`, status still `open`)
rather than deleted, a finding below its floor refuses `rigor-below-floor`, and
`--force` publishes it while recording the reason and the offending sigs in
`post.json.forced[]`. A critical that still clears the important floor is
downgraded to `important` with a `validation` history event instead of
refusing, and no verdict ever upgrades a severity. The validator's prose is
stored and never quoted as a finding's evidence.

`validation.json` is validated strictly: a stale head refuses
`validation-stale`, and an unknown key, unpublished verdict, short reason, a
downgrade that skips a rank, a duplicate sig, or a sig that resolves to no
single finding refuses `bad-validation`; a malformed base policy refuses
`bad-rigor-policy`. `prompts/validate.md` joins the hashed prompt set, so
`prompts_hash` moves once for this change.
