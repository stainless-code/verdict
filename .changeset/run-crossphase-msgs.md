---
"@stainless-code/verdict": patch
---

Two-finder `run` now repairs a cross check instead of failing it blind: the cross directive names the accepted quote sides (`RIGHT` for the new side of the diff, `LEFT` for a deleted line), and a repair round carries the checker's real problems — JSONPath, code, and fix — instead of one generic message, so an agent that wrote `side: "new"` fixes it on the next round. An execution left incomplete names each failed phase and the `--resume` that continues it; `gate` no longer reports "no open findings" while the incomplete execution leaves in-tier findings unchecked (the `findings` dimension reads `unknown` with their count, and the recovery is `run --resume`, then `record`); a `review-insufficient` refusal that lists no evidence problems no longer points at gaps "named above"; and `run`'s `re-enter:` line keeps the arguments after `--`, quoted as `--print` quotes argv, with credential-shaped values redacted.
