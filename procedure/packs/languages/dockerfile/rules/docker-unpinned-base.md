# Unpinned or floating base images

`FROM node` is `node:latest`; `:latest` and a major-only tag move under the
build. The base is the largest dependency the review never sees, and a
rebuild can produce different code from the same Dockerfile.

## When to flag

- `FROM image` with no tag (implicit `latest`), or an explicit
  `FROM image:latest`.
- A floating major tag (`node:20`, `python:3`, `ubuntu:22`) in a production
  image, where the tag can move to a new minor/patch without a diff.
- A base image selected by a variable or build arg with no default pin
  (`FROM ${BASE_IMAGE}`), when the value is not pinned in the build.
- A manifest-list tag whose platform can change the digest without a
  Dockerfile change, when the image is used for reproducible builds.
- An unpinned base in a stage whose output is the shipped image, even if the
  builder stages are pinned.

## Do NOT flag

- `FROM scratch`: there is no upstream image to pin.
- `FROM builder`/`FROM base` referencing an earlier stage by name; it is not
  a registry reference.
- A digest-pinned base (`FROM node@sha256:...`) or a digest with a tag
  (`node:20@sha256:...`), which is immutable.
- An exact version tag (`node:20.11.1-alpine`) under a repository policy that
  pins by version rather than digest. Note that the tag is still mutable;
  prefer a digest, but do not call it floating.
- A local or internal base image built by the same pipeline and pinned by
  digest elsewhere in the diff.
- A test/CI-only Dockerfile that is rebuilt from the default branch and
  never shipped.

## Evidence

Quote the `FROM` line. Name the tag or digest and what upstream can change
under it, plus whether the stage is the shipped image.
