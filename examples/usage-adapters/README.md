# Usage adapters (optional, community-extendable)

verdict never opens harness-private files. The budget gate only reads usage
records the operator explicitly hands over via `VERDICT_USAGE=<file|dir>`,
in the contract shape published as `schemas/usage.schema.json`:

```json
{
  "schema_version": "1",
  "source": "claude-code",
  "model": "claude-sonnet-4-5",
  "tokens": { "input": 3000, "output": 170, "cache_read": 800 },
  "usd": 2.5,
  "started_at": "2026-09-01T10:00:00Z",
  "ended_at": "2026-09-01T10:02:00Z"
}
```

Only `schema_version`, `source`, and the `started_at`/`ended_at` window are
required; `model`, `tokens`, and `usd` ride along when the measurer knows
them. A record without `usd` never becomes a measured claim — the tool
never prices tokens itself. One file may hold a single record, an array of
records, or (`.jsonl`) one record per line; a directory hands over every
`*.json`/`*.jsonl` entry. A named path that is missing or malformed refuses
typed (`bad-usage`) instead of silently degrading.

## The two shipped converters

Both are standalone POSIX shell + `jq` (`jq >= 1.7` for the opencode
epoch-millisecond windows) with no other dependencies, so they run
wherever the evals run. They validate their own output shape and exit
nonzero when the harness file carries nothing convertible.

| Harness                             | Converter                                                                | Hook / export                                      |
| ----------------------------------- | ------------------------------------------------------------------------ | -------------------------------------------------- |
| Claude Code transcripts (`*.jsonl`) | `claude-code/claude-to-usage.sh [--source NAME] <transcript> <out.json>` | `claude-code/on-stop.sh` + `claude-code/hook.json` |
| `opencode export <session>` JSON    | `opencode/opencode-to-usage.sh [--source NAME] <export.json> <out.json>` | run `opencode export` yourself, then convert       |

Claude Code pricing: the last cost-state `totalCostUSD` wins; without one,
deduplicated per-request `costUSD` entries sum. Token counts sum from
`message.usage` when present. The opencode converter reads `info.cost`
(falling back to summed message costs); that shape carries no token
counts, so its records price USD only.

### Claude Code session-end hook

Copy `claude-code/hook.json` into `.claude/settings.json` (fixing the
script path), then collect a usage log the gate can price from:

```bash
export VERDICT_USAGE_DIR="$HOME/.verdict/usage"
# ... run sessions; on-stop.sh converts each finished transcript ...
verdict fetch owner/repo 123   # with VERDICT_USAGE="$HOME/.verdict/usage"
```

The hook is best-effort: every failure exits 0 after at most one stderr
line, so it never breaks the session.

## Validate a converted file

```bash
schemas/validate.sh schemas/usage.schema.json <out.json>
```

## Adding a harness

Copy one converter, keep the contract (run `validate.sh`), add a fixture
under the new directory, and extend `evals/cases/usage-adapters.sh`.
Vendor formats change without notice — when a converter starts refusing,
the harness changed its export, and only the converter needs an update;
the binary and the contract stay untouched.
