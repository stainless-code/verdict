# Dockerfile lens

Dispatch only when the diff changes the base image, what is baked into a
layer, or the user the container runs as. Frame one falsifiable question —
what is unpinned, what leaks into history, who runs this — and give it a
short hypothesis label.

Read the changed Dockerfile and the build context it copies from; the on-disk
diff is authoritative scope. Scope the question to at most ~12 files, cite
NEW line numbers, and name the layer.
