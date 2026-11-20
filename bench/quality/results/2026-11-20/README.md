# Backtest fixtures results snapshot — 2026-11-20

Shipped-configuration (`C2`) recordings scored offline with `verdict backtest fixtures` against the immutable known-defect catalog in [`bench/quality/fixtures.json`](../../fixtures.json).

- **Date:** `2026-11-20` (recordings collected `2026-11-19` on `main` commit `9d7fc865f`, published `2026-11-20`)
- **Arm:** `C2` — the shipped default `verdict run` flow (`fetch-local --range <base>..<head> --spec <SPEC*> --context <rest>`, `run --agent pi --timeout 2400` with default `--rounds 2`, `record`, `report --check important --max-risk medium`). Internal `V-*` ablation arms are excluded.
- **Fixtures:** `fx1` (`STA-440 part 2, first build`, 5 known defects, 2 bonus) and `fx2` (`STA-351 PR A`, 6 known defects, 0 bonus). These recordings predate the `--spec` cap raise to 150 (#193): `fx2`'s `SPEC.md` (107 checklist items) exceeded the 60-item cap in force on `9d7fc865f`, so `fx2` ran with `SPEC.md` passed as `--context`.
- **Drivers and sample size (`N`):**
  - `pi-opencode-go-hy4-flash-free` (`hy4-flash`): `N=5` on `fx1`, `N=5` on `fx2`
  - `pi-deepseek-deepseek-flash` (`DeepSeek V4.1 Flash`): `N=3` on `fx1`, `N=3` on `fx2`
  - Combined: `N=8` per fixture (`16` scored runs total)
- **Calibration set untouched:** `bench/quality/<fixture>/recordings/` is unchanged; every invocation reproduces `8/8` calibration rows.

## Reproduce

From the repository root, run the product scorer against this snapshot:

```bash
verdict backtest fixtures --recordings bench/quality/results/2026-11-20
```

Single-fixture and machine-readable diagnostics (for per-driver splits):

```bash
verdict backtest fixtures --fixture fx1 --recordings bench/quality/results/2026-11-20
verdict backtest fixtures --fixture fx2 --recordings bench/quality/results/2026-11-20
verdict backtest fixtures --recordings bench/quality/results/2026-11-20 --json
```

## Reproduced numbers

Safety (headline):

- **False greens:** `0/16` across all scored runs (`0/8` on `fx1`, `0/8` on `fx2`, `0` unknown gate outcomes) — no approval or passing gate without verified evidence.
- **Hallucinated quotes:** `0/46` (`0.0000` measured rate across `36` gating findings on `fx1` and `10` on `fx2`, `0` unverifiable).
- **`review-insufficient` refusals:** `0`.
- **Calibration:** `8/8` rows reproduced.

Recall (tracked number — rates describe recorded reviews on these two fixtures, not a universal model claim):

| Fixture | Known defects | Driver                  | N   | Recall per run                           | Mean     | Union    | False green | Hallucinated quotes |
| ------- | ------------- | ----------------------- | --- | ---------------------------------------- | -------- | -------- | ----------- | ------------------- |
| `fx1`   | 5 (+2 bonus)  | `hy4-flash`             | 5   | `0.8000, 0.8000, 0.2000, 0.8000, 0.8000` | `0.6800` | `1.0000` | `0/5`       | `0/21` (`0.0000`)   |
| `fx1`   | 5 (+2 bonus)  | `DeepSeek V4.1 Flash`   | 3   | `0.8000, 0.8000, 0.8000`                 | `0.8000` | `0.8000` | `0/3`       | `0/15` (`0.0000`)   |
| `fx1`   | 5 (+2 bonus)  | Combined (both drivers) | 8   | `0.8000` ×7, `0.2000` ×1                 | `0.7250` | `1.0000` | `0/8`       | `0/36` (`0.0000`)   |
| `fx2`   | 6             | `hy4-flash`             | 5   | `0.1667, 0.1667, 0.3333, 0.3333, 0.1667` | `0.2333` | `0.5000` | `0/5`       | `0/6` (`0.0000`)    |
| `fx2`   | 6             | `DeepSeek V4.1 Flash`   | 3   | `0.6667, 0.1667, 0.3333`                 | `0.3889` | `0.6667` | `0/3`       | `0/4` (`0.0000`)    |
| `fx2`   | 6             | Combined (both drivers) | 8   | `0.6667` ×1, `0.3333` ×3, `0.1667` ×4    | `0.2917` | `0.6667` | `0/8`       | `0/10` (`0.0000`)   |
