Report a secret baked into an image layer: `ARG` or `ENV` carrying a token, key,
or password, `COPY` of a `.env` or credential file, or a `RUN` that leaves a
secret in the layer history even though a later layer deletes it.
