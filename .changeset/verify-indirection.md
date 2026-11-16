---
"@stainless-code/verdict": patch
---

The verify pre-exec gate refuses a command word it cannot read: `E=rm; $E -rf
./target` and `E=curl; $E -o ./f http://host/x` passed every arm because the
tool word sat in the variable, and the resolved command then ran. `verify` now
refuses the shape as `verify-indirection`, quoting the spelling it saw and
naming the remedy (write the tool word plainly, or run the step under
`verify --sandbox`). The braced (`${E}`), quoted (`"$E"`), `export`ed,
assignment-prefix, loop (`for E in curl; do $E ...`), concatenated (`$A$B`),
and own-line (`E=rm\n$E -rf ./target`) spellings close with it, and so do the
wrappers that run a later word (`env -u HOME $E`, `timeout -s KILL 5 $E`,
`timeout 5s $E`, `xargs -I {} $E`, `command`, `nice`, `time`, `nohup`,
`stdbuf`, `exec`, `then`, `do`). Variables in argument position still execute
(`echo $USER`, `PORT=9 curl http://127.0.0.1:9/$PORT/f`, `$BIN/tool --version`),
so the false-positive boundary is unchanged for a command written plainly.

The gate does not resolve variables — the value can come from the command's own
environment, or from a later or conditional assignment — so it refuses rather
than guessing, and a variable holding a whole command line (`sh -c "$V"`) stays
a documented residual of the tripwire. A wrapper's own option grammar decides
which later word bash runs, so after `builtin`/`command`/`env`/`exec`/`nice`/
`nohup`/`stdbuf`/`time`/`timeout`/`xargs` any variable in that command refuses:
that is the deliberate cost of not modelling each wrapper's flags, so
`timeout 300 make test` stays clean while `timeout 300 make test $TARGET`
refuses. A shell carrying an inline command (`sh -c "$E pwned"`, `bash -ec`, `sh -cu`)
counts the same way; `eval` and `sudo` refuse earlier as `verify-destructive`.
The segmentation the gate reads now also splits at newlines for every arm that
reads a command word, so a glob or tool word on a later line (`echo ok\nr? -rf
x`) refuses instead of joining the first command's segment. Leading
redirections (`</dev/null`, `2>&1`, `>> f`) are skipped to reach the command
word, and a substitution body (`$($E pwned)`, backticks, `cat <($E pwned)`) is
judged as its own command position, read quote-aware (`$($E "(")` closes where
bash closes it) with `$((…))` arithmetic left alone.

`SECURITY.md` now says the same thing
the README and the manual say: the refusals are a tripwire, `verify --sandbox`
is the boundary, and a spelling outside the gate's reach is a gate gap to arm,
not a vulnerability.
