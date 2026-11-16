---
"@stainless-code/verdict": patch
---

The verify pre-exec gate refuses a command word it cannot read: `E=rm; $E -rf
./target` and `E=curl; $E -o ./f http://host/x` passed every arm because the
tool word sat in the variable, and the resolved command then ran. `verify` now
refuses the shape as `verify-indirection`, quoting the spelling it saw and
naming the remedy (write the tool word plainly, or run the step under
`verify --sandbox`). The braced (`${E}`), quoted (`"$E"`), `export`ed,
assignment-prefix, loop (`for E in curl; do $E ...`), and concatenated
(`$A$B`) spellings close with it, and so do the wrappers that run the word
after their flags (`env`, `timeout`, `nice`, `xargs`, `command`, `then`, `do`).
Variables in argument position still execute (`echo $USER`, `PORT=9 curl
http://127.0.0.1:9/$PORT/f`, `$BIN/tool --version`), so the false-positive
boundary is unchanged.

The gate does not resolve variables — the value can come from the command's own
environment, or from a later or conditional assignment — so it refuses rather
than guessing, and a variable holding a whole command line (`sh -c "$V"`) stays
a documented residual of the tripwire. `SECURITY.md` now says the same thing
the README and the manual say: the refusals are a tripwire, `verify --sandbox`
is the boundary, and a spelling outside the gate's reach is a gate gap to arm,
not a vulnerability.
