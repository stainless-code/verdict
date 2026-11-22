# Predictable temporary paths

A fixed name under `/tmp` is shared by every user and process on the host.
Another process can pre-create it, point it at a symlink, or read the
contents before the script finishes.

## When to flag

- A hardcoded temporary path (`/tmp/build.log`, `/tmp/app.lock`,
  `/tmp/upload.tar`) written or read by a script that can run concurrently.
- A PID-based or timestamp-based name (`/tmp/out.$$`, `/tmp/run-$(date +%s)`)
  used as a security boundary; `$$` is predictable and reusable.
- A temporary directory created with `mkdir /tmp/foo` instead of
  `mktemp -d`, with no mode restriction.
- A script that trusts an existing file under `/tmp` without checking
  ownership/permissions, or writes secrets there.
- A trap that does not clean up the temporary file, leaving a symlink target
  or sensitive content behind.

## Do NOT flag

- `mktemp` / `mktemp -d`, which creates a unique path with restrictive
  permissions; use `trap 'rm -rf "$tmp"' EXIT` for cleanup.
- A fixed path inside a private, mode-0700 directory created by the script
  (or a container's isolated scratch volume).
- A temporary path under `$TMPDIR`/`$XDG_RUNTIME_DIR` whose value the
  environment owns, when the script creates a unique child with `mktemp`.
- A test fixture's `t.TempDir()`/`mktemp` directory.
- A lock file in `/var/lock` or a runtime directory owned by the service,
  with the correct permissions.
- A fixed cache path that the application owns and that contains no secret.

## Evidence

Quote the path and how it is created. Name the pre-creation, symlink, or
read opportunity and the data exposed.
