---
"@stainless-code/verdict": minor
---

Patch identity dedupes re-reviews across rebases. `fetch` and `fetch-local` write `.review/<bundle>/identity.json` (`git patch-id --stable`, labeled `diff-sha256` fallback), `post` refuses `already-reviewed-patch` on a same-patch sibling unless `--force` (recorded in `post.json` `forced[]`), `adopt --from auto` matches exactly one local twin, and `report --completeness` passes when the posted and fetched heads differ only by a rebase with the same patch.
