---
"@stainless-code/verdict": minor
---

`verdict demo [<public-github-pr-url>] [--agent <name>] [--keep] [--json]` runs one review with no setup and no risk. With a URL it reads a public pull request, through `gh` when it is signed in to github.com and the anonymous REST API otherwise. With no argument it reviews your working tree. It uses the first installed agent CLI on `PATH`, or `--agent`, and refuses `agent-missing` with an install hint when there is none. It runs one finder and prints the review exactly as `post` would publish it. Everything lands in a temporary `verdict-demo-*` directory, removed on exit unless `--keep`: nothing is written to your repository, and nothing is ever posted.
