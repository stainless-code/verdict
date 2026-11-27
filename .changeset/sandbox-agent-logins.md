---
"@stainless-code/verdict": patch
---

Sandboxed `opencode` reviews now sign in with your real opencode login. Current opencode keeps its logins in its database rather than `auth.json`, so verdict reads the database's credential table read-only, without SQLite, and seeds each provider's login into the launch's throwaway data home. `OPENCODE_DB` now points at that throwaway copy. The review sandbox also hides opencode's database (`opencode.db` with its `-wal` and `-shm` files, and wherever `OPENCODE_DB` moves it) from every agent, because it holds the same tokens as `auth.json`. `agy` now declares `~/.gemini` as read-only state. On macOS, a sandboxed `agy` review warns before launch that its keychain sign-in stays closed and that `GEMINI_API_KEY` signs it in instead. The keychain stays closed because agy keeps a shell, and with the keychain open that shell could read every token stored there, `gh`'s included.
