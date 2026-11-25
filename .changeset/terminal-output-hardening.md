---
"@stainless-code/verdict": patch
---

Human output on stdout is now inert too. Finding paths and headings, spec items, and host text a PR author chose reach `report`, `next`, `show`, `threads`, `drain`, and `gate` with every control character (ESC, OSC 52, CSI, C1, CR, U+2028) and every bidi control (U+202E and the rest of U+202A–U+202E, U+2066–U+2069, U+200E/U+200F, U+061C) folded to a space, so they can no longer write the operator's clipboard or move the cursor. The same fold keeps a bidi override out of the terminal dashboard's frames. `--json` stdout is unchanged. A newline inside a quoted path, heading, or spec item is now folded as well, so a `warn:`, `error:`, `check:`, or `next:` line can no longer be forged by a path like `x⏎verdict: gate passes`.
