# GRADE — self-grading probe on closed findings

Score a sample of closed findings against ground truth and record machine-readable
judgments that MAY diverge from the triage dispositions. Advisory prose is the
supplement, not the record: credit-weight evidence rests on the judgment columns
in the grades file, so that agreement is measured, not assumed.

---

## Checklist

1. **Task list** as first action.
2. **Read the bundle.** Run `./scripts/review.sh report owner/repo <bundle> --status all`
   and read `.review/<bundle>/findings.json` (sig, status, kb cites per finding).
   Closed findings are the sample: `addressed` / `false-positive` carry a
   baseline verdict; `open` / `wont-fix` / `deferred` carry none.
3. **Baseline.** Run `./scripts/review.sh calibrate owner/repo <bundle> --probe`
   (`--json` for the machine envelope). The script writes
   `.review/<bundle>/kb-grades.jsonl` — one row per finding with script-owned
   baseline columns (`verdict` from the disposition, first cited `rule`,
   `helpfulness`). Never hand-edit that file; the next probe run regenerates
   the baseline and overwrites direct edits. To change the baseline itself,
   triage the finding and re-run.
4. **Judge the sample.** Re-read each sampled closed finding against the code at
   its anchor: was the finding real? Did the cited rule point at the real cause
   or decorate a false alarm? Write your judgments as a fragment — one JSON
   object per line — to a temp path (never to `kb-grades.jsonl`):

   ```json
   { "sig": "<full sig>", "agent_verdict": "real", "agent_helpful": true }
   ```

   `agent_verdict` is `real`/`false`/`unknown`; `agent_helpful` is
   `true`/`false`/`null`. Judge only findings you actually re-read — leave the
   rest unsampled (`agent_verdict` stays `null`). Your verdict may disagree
   with the baseline: that dissent is the point, and it is recorded, not punished.

5. **Merge.** Run `./scripts/review.sh calibrate owner/repo <bundle> --probe
--judge-file <fragment>` (or repeatable `--judge SIG:VERDICT[:HELPFUL]` for
   one-liners). The script validates every sig against the findings store —
   unknown sigs refuse typed (`unknown-judgment`), malformed rows refuse as
   `bad-judgment` — and merges valid judgments as `agent_verdict` /
   `agent_helpful` / `agreement` columns. Judgments are sticky: re-runs keep
   prior agent columns for surviving sigs, so the sample survives triage
   refreshes of the baseline.
6. **Report the number.** The M4-consumable result is the agreement rate over
   doubly-judged rows (baseline known AND agent judged `real`/`false`), with
   the sample size: `judgments: sampled=S judged=J agreed=A disagreed=D
rate=R` (also `data.judgment` in the `--json` envelope). Name each dissent
   (sig, baseline vs yours, why) as advisory text — the file already holds the
   record, the prose holds the reasoning.
7. **Close advisory-only.** Name the top finding the probe confirms (highest
   severity real finding whose rule helped) as context for the operator. Then
   stop: no triage, no retune, no weight change on a probe run.

## Grade row shape (exact contract — one JSON object per line)

```json
{
  "at": "2026-10-31T00:00:00Z",
  "bundle": "123",
  "sig": "<full sig>",
  "verdict": "real",
  "rule": "await-err",
  "helpful": true,
  "agent_verdict": "real",
  "agent_helpful": true,
  "agreement": true,
  "grader": "calibrate-probe"
}
```

- Baseline (script-owned, regenerated from `findings.json` every run):
  `verdict` is `real` (status `addressed`), `false` (status
  `false-positive`), else `unknown`; `rule` is the first cited kb id, else
  `null`; `helpful` is `true`/`false` only when a rule is cited AND the
  baseline verdict is `real`/`false`, else `null`. Findings without ground
  truth grade `unknown`, never forced real/false.
- Judgment (agent-supplied via `--judge`/`--judge-file`, validated, sticky):
  `agent_verdict` is `real`/`false`/`unknown`, else `null` when unsampled;
  `agent_helpful` is `true`/`false`/`null`. `agreement` is `true`/`false`
  when both baseline and agent verdict are `real`/`false`, else `null`
  (unsampled or no baseline — excluded from the agreement rate).
- Additive: the probe writes only `kb-grades.jsonl` — findings, joins, and
  calibration outputs stay byte-identical. Offline: no post record, no network.
