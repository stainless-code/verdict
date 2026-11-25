---
"@stainless-code/verdict": patch
---

`verdict lsp` resolves a session's workspace roots concurrently within the existing walk bounds, so a hung volume delays `initialize` by one 250 ms budget instead of one per root. A walk skipped because all eight walk slots were held now logs that cause rather than "took longer than the budget", slow-walk lines go out as soon as the walk misses, and the server forgets a slow path once it resolves. `verdict serve` and `verdict lsp` no longer miss a bundle written just as the file watcher starts. A session that ends while a walk is stuck no longer waits out the walk, and a slow watcher baseline no longer stalls `didChange` or `$/cancelRequest`.
