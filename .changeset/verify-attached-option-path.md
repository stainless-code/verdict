---
"@stainless-code/verdict": patch
---

`verify` now refuses a path glued to the option that takes it: `tar -C/etc`,
`cp -t/etc/cron.d`, `sort -o/etc/x`, `grep -f/etc/passwd`, `gcc -I/usr/include`,
`git -C/etc`, and `go build -o/etc/x` exit before the command runs instead of
executing it. The pre-exec gate only recognized a path after a space or a
separator, so every spelling getopt accepts — a clustered `-czf/etc/x`, a
positional `-C` after `--`, a quoted or backslashed `-C/etc`, an uppercase
`-C/ETC` — reached the shell with the escape unreported. An attached value is
now judged as the path it is, and the refusal names the fragment it read.

One spelling to know: a relative value that starts with a letter and contains a
slash (`git -Ctools/cli`) has the same shape as `-C/etc`, so the gate refuses
it rather than guessing. Write `git -C tools/cli` — both spellings of a relative
value stay allowed once a space separates the option from its value.
