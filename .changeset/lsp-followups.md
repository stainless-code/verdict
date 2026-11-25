---
"@stainless-code/verdict": patch
---

`verdict lsp` resolves workspace roots concurrently, so a hung volume delays `initialize` by one 250 ms budget instead of one per root, and its slow-walk log lines name the real cause. `verdict serve` and `verdict lsp` no longer miss a bundle written just as the watcher starts. A session that ends during a stuck walk no longer waits it out, and a slow watcher no longer stalls `didChange` or `$/cancelRequest`.
