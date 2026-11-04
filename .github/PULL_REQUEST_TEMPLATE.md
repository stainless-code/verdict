# What this changes and why

## Merge-bar checklist

- [ ] `make evals` green (or the affected `./evals/one.sh <case>` while iterating, full suite at the end)
- [ ] `make test-race` (race + shuffle) green; `gofmt -l .` empty
- [ ] `golangci-lint run ./...` clean under the repo's `.golangci.yml`
- [ ] `bun fmt --check` on touched md/json/yml (never reflowed `skills/verdict/prompts/` examples)
- [ ] Changeset added (`.changeset/<name>.md`) — or docs-only / eval-only / comment-only, so none needed
- [ ] Golden diff reviewed (`-update` regenerations explained line-by-line, committed separately from unrelated changes)

## Tested on

CI proves Linux and Windows on every PR, and the macOS legs on a `main` push
and the nightly run. Tick what you ran by hand; an untested route stated out
loud is worth more than a guessed tick.

- [ ] `install.sh` — macOS / Linux
- [ ] `install.ps1` — Windows
- [ ] npm wrapper — `npm install -g @stainless-code/verdict` (macOS / Linux / Windows)
- [ ] Homebrew tap — `brew install stainless-code/tap/verdict` (macOS / Linux)
- [ ] Skill route — `install-skill.sh` or `verdict skill install` (macOS / Linux / Windows)

## Notes for reviewers
