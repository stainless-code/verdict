---
"@stainless-code/verdict": minor
---

**Breaking (pre-1.0): `gate` is now the only gating verb.** `report` is the advisory read and no longer takes any flag that fails its exit. `--check`, `--completeness`, `--max-risk`, and `--live` are unknown flags on `report` (usage exit 2, no envelope), `report --json` no longer carries `data.gate` or `data.completeness` (the `envelope` schema drops both properties and the `reportCompleteness` definition, and the TS client drops `ReportData.gate`, `ReportData.completeness`, and the `ReportCompleteness` type; `gate` keeps `reportGate` as its data profile), the Go SDK's `ReportRequest` drops `Check`, `Completeness`, `Live`, and `MaxRisk` (and `ReportResponse` drops `CheckLines` and `GateFailed`), and the MCP `report` tool no longer accepts `check`, `completeness`, `maxRisk`, or `live`. `gate` gains the two report behaviors it lacked — `--max-risk low|medium|high|critical` (a `risk` dimension) and `--category <cat>`, which narrows `--max-severity` to one finding kind — and its `head` dimension now carries the typed cause (`missing-post`, `corrupt-post`, `stale-head`) and the re-fetch command that `report --completeness` printed. The Go SDK's `GateRequest` takes `MaxRisk` and `Category`; the `risk` queue's usage error and the served compare view's next command name the new spellings.

Migration:

| Before                                      | Now                                           |
| ------------------------------------------- | --------------------------------------------- |
| `report --check <tier>`                     | `gate --max-severity <tier>`                  |
| `report --check <tier> --category <cat>`    | `gate --max-severity <tier> --category <cat>` |
| `report --completeness`                     | `gate --require-head`                         |
| `report --max-risk <level>`                 | `gate --max-risk <level>`                     |
| `report --live <auto\|strict\|skip>`        | `gate --live <auto\|strict\|skip>`            |
| `report --json` → `data.gate.passed`        | `gate --json` → `data.passed`                 |
| `report --json` → `data.gate.dimensions[]`  | `gate --json` → `data.dimensions[]`           |
| `report --json` → `data.completeness`       | `gate --json` → the `head` dimension          |
| Go SDK `ReportRequest{Check, MaxRisk, ...}` | `GateRequest{MaxSeverity, MaxRisk, ...}`      |

`gate` always grades the stored coverage receipt (`--coverage require`), which `report` never did, so a review that has not attested coverage fails `gate` where `report --check` passed; add `--coverage warn` to keep that shortfall advisory until it attests. A `report --max-risk` run made no live-thread read, so its `gate` spelling adds `--live skip`. Output differs too: `gate` prints one `gate: <dimension>` line each (on stdout, or stderr under `--json`) where `report` printed `check:` lines on stderr. `verdict init --ci` already scaffolds `gate`, so a freshly written workflow needs no change. The manual's `gate` section is the canonical gating doc and carries the same table.
