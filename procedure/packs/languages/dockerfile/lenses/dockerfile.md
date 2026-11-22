# Dockerfile lens

Dispatch only when the diff changes the base image, what is baked into a
layer, the user the final container runs as, or a remote install script.
Frame one falsifiable question — what is unpinned, what leaks into history,
who runs this, what remote code executes — and give it a short hypothesis
label.

Read the changed Dockerfile and the build context it copies from; the on-disk
diff is authoritative scope. A multi-stage build's builder stages may run as
root and carry build tools; only the final stage's `USER` is the runtime
identity. Scope the question to at most ~12 files, cite NEW line numbers, and
name the stage and layer.
