---
"@stainless-code/verdict": patch
---

`gate --live` reads threads on every host and fails closed when it cannot. The
live-thread dimension skipped the read for any bundle whose provider was not
GitHub — it warned `live recheck skipped: <provider> threads unreadable here`
and passed the gate without looking, so `--live strict` on a GitLab, GitCafe,
Forgejo, Bitbucket, or Azure bundle was a pass that proved nothing. The probe
now reads the bundle's own host through the same per-host adapter the `threads`
refresh uses (GitHub keeps its resolution-only `gh api graphql` read, unchanged
byte for byte), counts unresolved threads the same fail-closed way, and treats a
read that stopped before the host's last page as unread rather than clean:
`--live auto` warns and gates on the local dimensions, `--live strict` fails
closed with the existing strict refusal. Only local bundles skip the lookup now,
because they carry no host threads at all.
