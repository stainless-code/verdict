# What this changes and why

## Merge-bar checklist

- [ ] `make evals` green (or the affected `./evals/one.sh <case>` while iterating, full suite at the end)
- [ ] `go test -race ./...` green; `gofmt -l .` empty
- [ ] `golangci-lint run ./...` clean under the repo's `.golangci.yml`
- [ ] `bun fmt --check` on touched md/json/yml (never reflowed `skills/verdict/prompts/` examples)
- [ ] Changeset added (`.changeset/<name>.md`) — or docs-only / eval-only / comment-only, so none needed
- [ ] Golden diff reviewed (`-update` regenerations explained line-by-line, committed separately from unrelated changes)

## Notes for reviewers
