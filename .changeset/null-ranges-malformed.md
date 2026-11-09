---
"@stainless-code/verdict": patch
---

A `ranges: null` coverage claim is malformed, not whole-file. The attestation
reader treated a present-but-null `ranges` as "no ranges given", which is the
same shape as an omitted key, so it counted the claim as the file's whole hunk
set: `record --dry-run --require-coverage` reported the diff 100% attested and
the gate passed on a claim that carried no ranges at all. A `ranges` key that is
present and is not a string — `null` included — now warns once and counts as
unattested, exactly like a reversed or unparsable range. A claim still widens
only by saying `"all"` or by omitting the key, so a malformed claim can never
attest more than it says.
