---
name: verdict-docs
description: "Read the Verdict documentation instead of answering from memory: how a review works, install and configuration, the JSON protocol, the MCP server, the schemas. Use when a task asks about Verdict itself; to run a review, load the `verdict` skill instead."
---

# Verdict docs

The docs live at https://stainless-code.com/verdict. When a task involves
Verdict, read the relevant page before relying on what you remember: the docs
are the source of truth, and they change.

This skill is the documentation guide. The review workflow — fetch, review,
gate, post — is a different skill, `verdict`, installed with `verdict skill
install` or `npx skills add stainless-code/verdict`. Load that one to review a
pull request or a local branch; load this one to answer questions about Verdict.

## Reading the docs

- Any page as Markdown: add `.md` to its URL (the home page is
  https://stainless-code.com/verdict/index.md).
- [llms.txt](https://stainless-code.com/verdict/llms.txt): every page with a
  one-line summary. Start here to pick the page — the list is generated from the
  docs on every build, so it is the current map and this file does not repeat it.
- [llms-full.txt](https://stainless-code.com/verdict/llms-full.txt): the
  narrative pages in one file, for when you need most of them at once.
- [Manual](https://stainless-code.com/verdict/manual.md): the operator contract
  — envelope, exit codes, store files, refusal codes, control files, provider
  matrix. It and the release notes are too long for `llms-full.txt`, so fetch
  them as their own Markdown.
- [Page index](https://stainless-code.com/verdict/api/docs/pages.json): the JSON
  API's list of pages, each readable as JSON or Markdown.
- [Changelog](https://stainless-code.com/verdict/changelog.md): release notes,
  newest first. Fetched per release as
  https://stainless-code.com/verdict/changelog/<version>.md.
