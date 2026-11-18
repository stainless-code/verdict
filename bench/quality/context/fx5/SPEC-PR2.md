## PR 2 — the Procedure in the Bundle (STA-443 item 0)

- **`fetch`/`fetch-local`** always write `<bundle>/procedure.md`: the task-scoped assembly that `--show-prompts` builds today (system plus review Procedure, the plan, references to `lenses.txt` and the other bundle files), from the embedded payload.
  - `--show-prompts` keeps its printing role only.
  - `procedure.md` is a core bundle file in the manual's namespace table.
- **Provenance:** `prompts_hash` covers `procedure.md`'s bytes, so `record`/`post` show which Procedure the agent received.
- **`run` directive:** replace "Read the installed verdict review skill first" with "Read `<bundle>/procedure.md` first; it is the authoritative Procedure for this review (an installed skill may be older)". Apply the same to `drain`'s directive where a procedure applies.
- **Principle 3:** no reads of harness skill dirs from `run`/`fetch`.
- **Acceptance:**
  - every fetch kind writes `procedure.md`;
  - its hash appears in provenance;
  - the `run` and `drain` directives name it (a test derives the file name from the directive text, the F1-class rule);
  - the fetch/fetch-local `next:` hint names `<bundle>/procedure.md` instead of `PROCEDURE.md + prompts/...` (found in the #182 review: after 1b those files exist only in the binary);
  - `readTrustedPrompt` (`--show-prompts`) and `PromptsHash` read the embedded payload, so an installed user (CWD = their repo, no `procedure/` checkout) gets the real prompts and a non-empty-set hash; a test pins the hash from a CWD with no `procedure/`;
  - the full ladder is green.
- **Release hold:** do not cut a release between 1b and PR 2.
- **After PR 2 merges:** run the STA-443 A/B/C benchmark.
