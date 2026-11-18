# STA-440 part 2 spec — the wider agent table plus an identity probe

Status: for build, 2026-11-17, against main `291c83ae5` (part 1, #176, merged). Evidence: `.scratch/factory/sta-440/RESEARCH-2.md` (primary sources, read 2026-11-17). The orchestrator spot-verified auggie, vibe, cline and junie (cline's exit and junie's `--model` are still open; see §3).

## 1. The identity probe (new data fields and one new refusal code)

`agentDef` gains two optional fields:

- `ProbeArgs []string`: a harmless command that only the intended agent answers successfully, normally its headless subcommand's help (e.g. `["ask", "--help"]`, `["-p", "--help"]`).
- `ProbeMatch string`: text the probe's combined output must contain (case-sensitive, a plain substring).

**Behavior:**

- On a real launch (never under `--print`, which spawns nothing), when `ProbeArgs` is set, run `<resolved binary> <ProbeArgs...>`:
  - with a 5 s timeout;
  - with the same child environment and redaction as the launch;
  - with no stdin;
  - capturing stdout and stderr, capped at 64 KiB.
- If it exits non-zero, times out, or lacks `ProbeMatch`, refuse the NEW typed code `agent-mismatch` before launching anything. The message names the agent, the resolved path, and the probe result; the hint is: try defining it in settings.json `agents` with an absolute `binary` path, or pass `--agent` for the one you meant.
- Record the probe in `launcher.json`: `probe: {args, ok}`.
- Operator-defined agents may also set `probe_args` and `probe_match`, with the same validation (closed keys; `probe_match` is required when `probe_args` is set).
- The new code needs registration in `refusalcodes.go` (literal at the emission site, so the archtest registry scan sees it), a hint, a troubleshooting entry, and `make tsgen`.

**When a built-in row MUST declare a probe** (the bar for this PR): its binary name is also the name of another known tool. Check with `brew info <binary>` and `brew search --formula "/^<binary>$/"` locally, plus the vendor docs. Record the check result in the row's comment. Known cases from research: `fx` (the JSON viewer `fx`). Expect also `forge` (Laravel Forge), `grok`, `kilo`, `cortex`, `bob`, `jazz`. Check each one; do not assume.

Rows whose binary has no known clash do NOT get a probe (no extra process).

## 2. The rows to add

Use RESEARCH-2's step-2 table. One row per agent, sorted by name, with a source comment (URL + read date 2026-11-17, plus the clash-check note when a probe is declared). Use `PromptFlag` when the research says NEEDS-FIELD (prompt flag).

- **Include:** adal, auggie, autohand, bob, cline\*, codearts, codebuddy, commandcode (binary `commandcode`, an official alias of `cmd`; avoids `cmd.exe` on Windows), cn (Continue), cortex, dcode (Deep Agents), devin, dexto, forge, fx, grok, hermes, jazz, junie\*, kilo, kimchi, kimi, kode, mcode (MiniMax), neovate, openclaw, pa (Posit Assistant), qoder, qodercn, rovodev (binary `acli`, Args `rovodev run`), vibe (Mistral), zcode.
- **Exclude:**
  - iflow: shut down 2026-04-17
  - openhands, reasonix: unmaintained
  - tabnine: deprecated 2026-12-31
  - warp, ona: no headless mode
  - loaf, codemaker, sarvam-code: unclear
- **Naming:** the `--agent` name is the vendor's product slug (e.g. `continue` for `cn`?). Keep names short, unambiguous, and lowercase. Where the binary name is the natural name, use it. Record the choice in the row comment, and list every name in the manual table.

\* Open from the spot checks:

- **cline:** confirm from the vendor CLI reference that a positional prompt runs once and exits (not interactive in a TTY). If it is not confirmable, exclude cline with a reason.
- **junie:** confirm the model flag from the Junie CLI reference. If there is none, set `ModelFlag` empty (it only matters for `--print --stage` renders).

Rows with no model flag in the research (bob, forge, rovodev, zcode, openhands) get an empty `ModelFlag`.

**Required-argument rows:**

- `jazz` needs `--agent <id>`; leave it to pass-through and note it in the manual.
- `goose`-style provider coupling: note it in the manual.

## 3. Tests

- **Rows:** an argv and `--print` test per new row (table-driven, as part 1 does). The source-comment archtest already covers new rows.
- **Probe:**
  - a matching probe launches;
  - a non-zero exit, a timeout (use a tiny timeout or a fake), and a missing `ProbeMatch` each refuse `agent-mismatch` with ZERO launches;
  - `--print` runs no probe;
  - the operator-defined probe validates.

  Fake binaries must derive behavior from argv. Shell-script stubs live in `*_unix_test.go`; build absolute paths with `filepath.Join(t.TempDir(), …)`. Add a Windows-safe probe test using a Go-built helper if the repo has one (grep `stubshim`/`testenv`), or keep the probe tests unix-only with a reason.

- **Eval:** extend `run-custom-agent`, or add `run-agent-probe`: a stub named `fx` that is NOT the agent refuses `agent-mismatch`, and the real-shaped stub passes.

## 4. Docs

- **Manual table:** every new row (binary, args, prompt placement, install, write-permission flag, source).
- **Manual:** a "binary name clashes and the identity probe" paragraph.
- **Troubleshooting:** an entry for `agent-mismatch`.
- **Elsewhere:** `references/commands.md`, and a changeset (minor; name the new code and the probe fields).

## 5. Out of scope

- Unifying the agent lists (STA-441).
- Skills directories.
- Installing agents.
