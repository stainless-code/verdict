---
"@stainless-code/verdict": patch
---

`verdict mcp` now joins its stdin reader when the shutdown signal arrives, so no goroutine outlives `Serve`. `verdict serve` now ends open event streams when the HTTP listener fails, not only on a clean shutdown. The CLI now reads its environment only through the injected source, so host variables no longer leak into a run that was given its own.
