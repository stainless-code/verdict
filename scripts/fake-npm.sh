#!/bin/sh
# Dry-run helper for `make release-dry-run`: stands in for npm on PATH,
# prints the publish argv, and publishes nothing.
printf 'fake-npm: %s\n' "$*"
