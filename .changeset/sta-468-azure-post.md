---
"@stainless-code/verdict": minor
---

`post` now submits to Azure DevOps over the same provider-adapter seam as the
other hosts: each finding lands as one review thread anchored on its file line
(`threadContext.filePath` with the leading slash, `rightFileStart`/`rightFileEnd`
for a new-side line and `leftFileStart`/`leftFileEnd` for a deleted one), then
one general thread carries the review body. Every call is one `az rest`
invocation, so `AZURE_DEVOPS_EXT_PAT` and `az login` stay inside `az`: no token
reaches an argv or an error sentence, and each write stages its JSON body in a
private `0600` file instead of the command line.

Azure takes no review event, so `approve` and `request_changes` stay false
there and a bound verdict posts its threads plus the body thread, with a
warning and a `withheld` entry in `post.json`. Anchors keep the honesty the
other hosts carry — a finding with no placeable anchor is withheld and
counted, an anchor whose side the change does not have on that file is
withheld, and an unreadable bundle hunk index refuses before any write. A
thread carries no commit pin, so `post` reads the live head first and refuses
the post when it moved since fetch. The open-thread recheck counts only
anchored threads that are still active or pending, so verdict's own summary
thread never blocks a second run, and the summary upsert patches the opening
comment of the thread it wrote instead of stacking a new one.
