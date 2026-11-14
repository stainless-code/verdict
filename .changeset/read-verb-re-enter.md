---
"@stainless-code/verdict": patch
---

Give `report`, `next`, and `show` refusals a `re-enter:` line. A missing bundle
printed a `try:` hint with no command to re-run, so the way forward was a retype;
the refusal now names the invocation the operator typed, shell-quoted and with
the flags they passed (`re-enter: verdict report acme/app 5101 --json`). This
covers `next`'s store-error path, which renders its own envelope. Refusal codes,
exit codes, and the envelope keep their shape; only stderr gains a line.
