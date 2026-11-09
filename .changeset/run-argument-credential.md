---
"@stainless-code/verdict": patch
---

Keep an argument credential out of the launch record. `verdict run --agent`
redacted a secret-shaped flag's operand in `launcher.json`'s `argv`, but the same
credential still reached the bundle twice: the recorded `output_tail` was scrubbed
only against secret-named environment values, so a child that echoed its own argv
— or printed it in a stray debug line — left the operand in plaintext, and the
raw child log `.run.out` was written `0644`.

The output scrub now hides the operand the argv redactor already identifies, from
one walk that feeds both the record's `argv` and its tail, so the two cannot
disagree about what a credential is. `.run.out` stays the child's raw stream for
debugging and is written `0600`. The environment scrub, the tail bound, `--print`,
and `launcher.json`'s shape are unchanged.
