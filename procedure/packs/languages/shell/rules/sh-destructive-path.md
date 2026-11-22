Report a destructive command whose target can resolve somewhere unintended: `rm
-rf "$DIR"/`, a `cd` before `rm -rf .`, or an expanded path with no `--` and no
existence or emptiness check.
