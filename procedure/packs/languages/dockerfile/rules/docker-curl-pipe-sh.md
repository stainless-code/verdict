# Remote install scripts without verification

`curl https://... | sh` runs whatever the server returns, with the build's
privileges and network access. A compromised or replaced response is code
execution in the image, and `ADD https://...` has the same trust problem.

## When to flag

- `RUN curl ... | sh`, `wget -O- ... | bash`, `curl ... | sudo bash`, or a
  pipeline that executes a downloaded script without a checksum or signature
  verification.
- `ADD https://...` of an archive or script with no `--checksum`, no
  signature check, and no digest-pinned source.
- A `curl` download without `--fail`/`-f` whose failure body is piped into a
  shell, so an HTTP error page becomes a script.
- A remote script fetched over plain `http://` or a URL whose TLS is not
  verified (`curl -k`, `--insecure`).
- A package installed by piping a remote installer into the package manager
  (`curl ... | apt-key add -`, `wget ... | tar xz` then executing) without
  a verified digest.

## Do NOT flag

- `ADD` of a local file or a file from the build context.
- A download followed by a pinned checksum or signature verification:
  `ADD --checksum=sha256:<digest> URL /dest`, or a `RUN` that fetches the
  archive and checks it against a checked-in digest file.
- A `curl` used only to probe a health endpoint or fetch data that is not
  executed.
- A package manager install from a signed repository whose keys and package
  hashes are verified (`apt-get install` and `apk add` against signed
  indexes), even though the network is involved.
- A remote artifact pinned by digest in an OCI reference
  (`docker://image@sha256:...`).
- A script fetched from a repository-owned, immutable artifact store and
  verified elsewhere in the same stage, with the verification visible.

## Evidence

Quote the download/execute command and its source. Name what the remote
server can change and the privileges the script runs with.
