---
"@stainless-code/verdict": patch
---

The TypeScript client's generated types render `anyOf` as a union instead of
collapsing it to `unknown`, and a null variant folds into a trailing `| null`
the same way a `"null"` entry in `type` does. `show`'s `data.fix.verify_hint`
now types as `VerifyHint | null` rather than `unknown`.
