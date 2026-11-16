---
"@stainless-code/verdict": patch
---

The verify pre-exec gate refuses a command word it cannot read: `E=rm; $E -rf
./target` and `E=curl; $E -o ./f http://host/x` passed every arm because the
tool word sat in the variable, and the resolved command then ran. `verify` now
refuses the shape as `verify-indirection`, quoting the spelling it saw and
naming the remedy (write the tool word plainly; `verify --sandbox` refuses it
too, because the gate runs before the sandbox wrapper). The braced (`${E}`), quoted (`"$E"`), `export`ed,
assignment-prefix, loop (`for E in curl; do $E ...`), concatenated (`$A$B`),
and own-line (`E=rm\n$E -rf ./target`) spellings close with it, and so do the
wrappers that run a later word (`env -u HOME $E`, `timeout -s KILL 5 $E`,
`timeout 5s $E`, `xargs -I {} $E`, `command`, `nice`, `time`, `nohup`,
`stdbuf`, `exec`, `then`, `do`). Variables in argument position still execute
(`echo $USER`, `PORT=9 curl http://127.0.0.1:9/$PORT/f`, `$BIN/tool --version`),
so the false-positive boundary is unchanged for a command written plainly.

The gate does not resolve variables — the value can come from the command's own
environment, or from a later or conditional assignment — so it refuses rather
than guessing, and a variable holding a whole command line (`sh "$V"`) stays a
documented residual of the tripwire. A wrapper's own option grammar decides
which later word bash runs, so after `builtin`/`command`/`env`/`exec`/`nice`/
`nohup`/`stdbuf`/`time`/`timeout`/`xargs` any variable in that command refuses:
that is the deliberate cost of not modelling each wrapper's flags, so
`timeout 300 make test` stays clean while `timeout 300 make test $TARGET`
refuses. A shell carrying an inline command (`sh -c "$E pwned"`, `bash -ec`, `sh -cu`)
has that command string judged as its own command position, so
`sh -c "echo $E"` stays clean; `eval` and `sudo` refuse earlier as
`verify-destructive`.
The three arms that read a command word — glob fragments, indirection and
command position — now read the segmented text, which splits at newlines, so a
later line is a command position of its own: `echo ok\nr? -rf x` and
`echo ok\n$(echo rm) -rf x` refuse instead of joining the first segment. (The
whole-string greps already saw every line.) A `\`-newline continuation is one
command and joins before segmentation, so `echo \` + newline + `"$USER"` stays
clean. Leading redirections (`</dev/null`, `2>&1`, `>> f`) are skipped to reach
the command word; a quoted word stays one word, so `LABEL="hello world" $E ok`
and `>"out file" $E` refuse rather than putting `world` in command position; a
substitution body (`$($E pwned)`, backticks, `cat <($E pwned)`) is judged as its
own command position — quote-aware, so `$($E "(")` closes where bash closes it —
while `$((…))` arithmetic and single-quoted literals (`awk '{print $($NF)}'`)
are left alone.

`SECURITY.md` now says the same thing
the README and the manual say: the refusals are a tripwire, `verify --sandbox`
is the boundary, and a spelling outside the gate's reach is a gate gap to arm,
not a vulnerability.
