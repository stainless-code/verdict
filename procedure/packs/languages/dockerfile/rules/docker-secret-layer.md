# Secrets baked into image layers

Build arguments, environment variables, and copied files stay with the image.
Deleting a file in a later layer does not remove it from the earlier layer,
and a build argument is visible in history and provenance.

## When to flag

- An `ARG` carrying a token, password, private key, or connection string, or
  a build step that consumes such an `ARG`. Build arguments are recorded in
  image history and in provenance attestations.
- `ENV SECRET=...`/`ENV API_KEY=...`: `ENV` persists in the image config and
  is inherited by every container.
- `COPY .env`, `COPY id_rsa`, `COPY .npmrc`, or a credential file into the
  image, even if a later `RUN rm` deletes it. The earlier layer still holds
  the bytes.
- A `RUN` whose command line contains the secret (an `Authorization`
  header, a token argument), or a shell history or credential file written
  and deleted in a later layer.
- A multi-stage copy of a secret-bearing artifact from a builder stage into
  the final stage.

## Do NOT flag

- A `RUN --mount=type=secret,id=...`/`--mount=type=ssh` BuildKit mount: the
  secret is available to that command and does not enter a layer.
- An `ARG` used for non-secret build metadata (a version, a target
  architecture, a public base URL), and `ENV NODE_ENV=production` or another
  non-secret setting.
- A secret supplied at runtime through the orchestrator's secret store or an
  environment variable set outside the image.
- A public certificate, a public key, or a well-known placeholder in a
  `.env.example` copied for documentation (flag a real `.env` instead).
- A `.dockerignore` that excludes a credential file from the build context;
  that is the mitigation, not the leak.
- A `COPY` of a credential file into a builder stage that is never copied to
  the final stage, when the builder is not shipped as an image.

## Evidence

Quote the `ARG`/`ENV`/`COPY` and the secret it carries. Name the layer or
image metadata where the value survives, and who can read the image.
