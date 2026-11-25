---
"@stainless-code/verdict": minor
---

**Breaking:** `gate` is the only gating verb. `report` is the advisory read: `--check`, `--completeness`, `--max-risk` and `--live` are unknown flags on it (usage exit 2), and `report --json` drops `data.gate` and `data.completeness`. The Go SDK's `ReportRequest`, the MCP `report` tool and the TypeScript client drop the same fields. `gate` gains `--max-risk low|medium|high|critical` and `--category <cat>`, and its `head` dimension names the cause (`missing-post`, `corrupt-post`, `stale-head`) and the re-fetch command.

| Before                                      | Now                                           |
| ------------------------------------------- | --------------------------------------------- |
| `report --check <tier>`                     | `gate --max-severity <tier>`                  |
| `report --check <tier> --category <cat>`    | `gate --max-severity <tier> --category <cat>` |
| `report --completeness`                     | `gate --require-head`                         |
| `report --max-risk <level>`                 | `gate --max-risk <level> --live skip`         |
| `report --live <auto\|strict\|skip>`        | `gate --live <auto\|strict\|skip>`            |
| `report --json` → `data.gate.passed`        | `gate --json` → `data.passed`                 |
| `report --json` → `data.gate.dimensions[]`  | `gate --json` → `data.dimensions[]`           |
| `report --json` → `data.completeness`       | `gate --json` → the `head` dimension          |
| Go SDK `ReportRequest{Check, MaxRisk, ...}` | `GateRequest{MaxSeverity, MaxRisk, ...}`      |

`gate` always grades the stored coverage receipt, which `report` never did, so a review without attested coverage fails `gate` where `report --check` passed: add `--coverage warn` until it attests. `gate` prints one `gate: <dimension>` line each instead of `check:` lines. Workflows from `verdict init --ci` already use `gate`.
