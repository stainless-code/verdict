---
"@stainless-code/verdict": minor
---

**Serve and the browser UI**

- **`verdict serve`** — a read-only HTTP surface over the current repository, loopback by default, with a session-cookie exchange, strict CSP, Host and Origin gates, and per-response source traceability. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s1.md -->
- **Root discovery** — `serve --root` finds nested repositories under a bounded traversal backed by a local 24-hour registry, and reports cache, truncation, and skipped patterns. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s2-discovery.md -->
- **Cost and precision buckets** — `/v1/cost` adds repository, ISO-week, and model buckets and `/v1/precision` adds category and tier counts, keeping unmeasured prices null and marking corrupt aggregates partial. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s3-cost-precision.md -->
- **Browser UI** — `serve` opens an embedded UI with inbox, review, threads, cost, precision, and gate views, dark mode, live refresh, and static `data.json` reading. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: serve-ui.md -->
- **Live invalidation** — bundles invalidate as they change, with `--interval`, `--sweep`, and `--no-watch` to control it. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: serve-live-updates.md -->
- **Vanished thread feeds** — a thread feed removed mid-read now reads as absent instead of corrupt. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: vanished-threads.md -->
- **Cache content budget** — serve cache hashing is bounded to 16 MiB per bundle lookup and bypasses the cache when the budget is exceeded. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: cache-content-budget.md -->
- **Small-file rewrites** — artifacts up to 64 KiB are hashed, so a rewrite that preserves size and mtime is still detected. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: cache-small-content.md -->
- **Inventory accounting** — serve inventory accounting stays inside its byte budget even for oversized artifacts. [Serve](docs/manual.md#serve-local-http-surface) <!-- changesets: inventory-budget-exact.md -->
