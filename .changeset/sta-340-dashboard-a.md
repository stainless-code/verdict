---
"@stainless-code/verdict": minor
---

New `verdict dashboard`: a read-only terminal view of your reviews, over the same query layer `serve` uses. **Overview** (`1`) shows portfolio totals, the served roots and every bundle that could not be read; **Queue** (`2`) shows one row per bundle with its counts, model and head. A withheld total shows `—`, never a smaller number. The live view needs a terminal (else it refuses `dashboard-no-tty`, or `dashboard-no-vt` on a Windows console without VT support). `q` or `Esc` quits, Ctrl-C exits 3, and every handled exit restores the screen; after a SIGKILL, run `reset`. `--once` prints one plain-text frame, and `--root` reads every repository below a directory. `NO_COLOR` and `--color never` drop color, and `TERM=dumb` switches to ASCII. Text from a PR has its control characters folded to spaces, so it cannot move the cursor or write the clipboard.
