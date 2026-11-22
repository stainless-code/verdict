Report a final stage with no `USER`: the container runs as root, so every file
it writes is root-owned on a mounted volume and any container escape starts
privileged.
