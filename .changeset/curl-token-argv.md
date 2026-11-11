---
"@stainless-code/verdict": patch
---

Host tokens no longer appear in curl's command line. Forgejo and Bitbucket
reads hand the credential to curl as a config document on stdin, and a Forgejo
write stages it in a 0600 file under a private temp dir because its body owns
stdin; the file is removed after the call. The token, password, and
`Authorization` header are gone from the argv a local `ps` or
`/proc/<pid>/cmdline` can read. A credential carrying a newline refuses before
any process spawns.
