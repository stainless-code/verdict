Report an unquoted expansion that decides which files or arguments a command
receives: `$var`, `$@`, or a `$(...)` result in `rm`, `mv`, `cp`, `chmod`, or a
loop head. Word splitting and globbing change the target set, and an empty value
can widen it.
