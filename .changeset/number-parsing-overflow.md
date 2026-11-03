---
"@stainless-code/verdict": patch
---

Refuse integer overflow instead of wrapping it. A review anchor, KB counter,
override threshold, adopt anchor, verify `--timeout`, or jq-read number that
does not fit in an int now takes the existing typed refusal or non-numeric
path — a 20-digit line keeps with the "non-numeric anchor" warning — instead
of becoming a nonsense negative span or count. Every ordinary digit parse now
goes through the standard library.
