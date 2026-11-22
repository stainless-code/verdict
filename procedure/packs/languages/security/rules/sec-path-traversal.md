# Path traversal and archive slip

A name from the request joined to a directory can climb out with `..`, an
absolute path, a Windows drive/UNC prefix, or an archive entry. The file
system then reads or writes outside the intended root.

## When to flag

- A path join of a fixed root and a request name (`filepath.Join`,
  `path.join`, or `Path(root) / name`) where the name is not checked to
  stay under `root`. `Join` cleans the path; it does not confine it.
- An absolute path, a `..` segment, a NUL byte, a Windows drive letter, or a
  UNC path accepted from input before an open/write.
- Archive extraction (zip/tar) that uses the entry name directly:
  `../../etc/cron.d/x`, an absolute entry, a symlink entry pointing outside,
  or a hardlink. Also cap the extracted size/count to avoid a zip bomb.
- `filepath.Base`/`basename` used as the only sanitizer when the caller
  expects a subdirectory, or a decoded/sanitized path re-joined later.
- A download endpoint that writes to a filename taken from a header or a
  multipart part.
- A `sendFile`/`serveStatic`-style call with a request-derived path and no
  root confinement.

## Do NOT flag

- A path built from a fixed root plus a validated identifier (a UUID, a
  numeric id mapped to a known filename).
- `filepath.Base` when the caller genuinely wants a flat filename and
  rejects `.`/`..`/empty.
- `filepath.IsLocal` (Go 1.20+) / `os.Root` (Go 1.24+) / Python
  `Path.resolve().is_relative_to(root)` / Java
  `normalize().startsWith(root)` / Node `path.resolve` plus a
  `root + path.sep` prefix check.
- Archive entries rejected for absolute paths and `..`, with symlinks
  refused and a size cap.
- A route parameter validated against a closed asset manifest.
- A test that asserts a traversal attempt returns 400.

## Evidence

Quote the path construction and the open/write/extract call. Name the file
outside the root that an attacker can read, write, or overwrite.
