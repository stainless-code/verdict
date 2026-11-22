---
"@stainless-code/verdict": patch
---

Hunk receipts accept the `OLD` row `hunks.tsv` prints for a modified hunk as a name for that same hunk. A review that copies every row of `hunks.tsv` into `receipts[]` no longer fails with a false `receipt-hunk-unknown`, its `OLD` and `NEW` receipts for one hunk count the hunk once toward coverage, and a bad quote on either row is still `receipt-mismatch`.
