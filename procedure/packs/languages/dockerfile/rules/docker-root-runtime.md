# Final stage runs as root

A container with no `USER` in its final stage runs as UID 0. Every file it
writes on a mounted volume is root-owned on the host, and a process escape
starts with the container's full privileges.

## When to flag

- A final stage with no `USER` instruction, when the base image does not
  already declare a non-root user.
- An explicit `USER root`/`USER 0` in the final stage, or a switch back to
  root after a non-root step, when the entrypoint runs as root.
- A `USER` that is set in an earlier stage only, so the final stage inherits
  root from its base.
- An entrypoint script that starts as root and drops privileges only after
  writing files that the runtime later needs, or that never drops them.
- A final stage on `scratch` with a static binary and no `USER`: there is no
  passwd file, but the process still has UID 0.

## Do NOT flag

- A builder or intermediate stage that runs as root: compilers and package
  managers commonly need it, and it is not the runtime image.
- A final stage whose base image already configures a non-root `USER` and
  which does not override it (a distroless `:nonroot` base, a base with
  `USER app` in its config). Verify the base's config rather than the
  Dockerfile alone.
- A final stage with `USER app`/`USER 10001:10001` before the entrypoint,
  even if an earlier build step in that stage ran as root and switched back.
- A development-only Dockerfile marked as such and never deployed, when the
  diff shows the scope (still report if it is the file under review for a
  production image).
- A container that intentionally runs a root-only system daemon with a
  documented privilege set and no host mounts; state the reason before
  exempting.

## Evidence

Quote the final `FROM`/`USER` sequence and name the base image's configured
user. State what the root process can write or reach.
