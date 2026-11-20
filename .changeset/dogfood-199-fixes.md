---
"@stainless-code/verdict": patch
---

Drain commands now serialize writes to the same bundle. Replanning keeps prior posting receipts, and retries clear failures when the thread is resolved or the reply draft is obsolete. An unresolved withdrawal still requires resolution.

MCP servers can shut down even when closing their input would leave a read blocked. The CLI still joins its protocol reader during shutdown.

`report --validation --json` uses the saved validation tally when a receipt exists and omits warnings specific to the human table.
