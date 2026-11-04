# Adding a verb

A verb is one user-facing command. It reaches five surfaces — CLI, MCP tool
table, Go facade (`pkg/verdict`), TypeScript client, and shell completion — and
two allowlists decide where it may stop. The steps below trace `trend` (a read
verb); `fetch` shows the mutating shape. `internal/archtest` is the checklist's
enforcement: `TestVerbRegistriesAgree`, `TestSurfaceParity*`, and
`TestPlatformTwins` fail when two registries disagree.

## 1. Service

Create `internal/core/<verb>/` with the request/response types and `Run`
(`internal/core/trend/service.go`, `rollup.go`). Read the store through
`internal/readcommon`; map exits through `internal/cli`. Add the package to
`coreSubpackages` in `internal/archtest/imports_test.go`, and to
`facadeAllowedDeps` when the facade calls it.

## 2. CLI adapter

Add `internal/<verb>/<verb>.go` — argv parsing, one call into the service,
warning routing, envelope rendering (`internal/trend/trend.go`). Add
`internal/<verb>` to `forbiddenDeps` in `internal/archtest/imports_test.go`;
`checkVerbAdapterCoverage` fails until you do.

## 3. Dispatch

`cmd/verdict/main.go`: read verbs go in the `readVerbs` map, mutating verbs
get a `case` in `dispatchMutating`, and fetch-shaped verbs in `dispatchFetch`.
A verb served before argument parsing (`mcp`) belongs in `run` and in the
`hiddenVerbs` allowlist of `internal/archtest/verb_twins_test.go`.

## 4. Usage, help, completion

- `cmd/verdict/usage.go`: add the verb to the frozen synopsis in `usageText`,
  write its block, and add it to `bareVerbs` when it takes no positionals.
- `cmd/verdict/main.go`: add it to `verbsHint`, the "did you mean" list.
- `scripts/verdict-complete.sh`: add it to `_verdict_verbs` and give it a
  `_verdict_flags` case.
- `cmd/verdict/verbhelp.go`: no edit unless the verb owns its help output
  (then add it to `ownHelpVerbs`).

## 5. Request types, MCP, schemas

- `internal/mcp/tools.go`: one tool spec (`name`, `title`, description,
  behavior hints, decode/wire/run).
- `internal/mcp/requests.go`: `decode<Verb>` and `wire<Verb>`.
- Regenerate the wire snapshots:
  `go test ./internal/mcp -run TestToolsListWireSnapshot -update-tools-list`.
- `internal/schemagen`: when the verb's `data` is a published shape, model it
  (`model.go`, `overlay.json` descriptions) and pin it in
  `conformance_verbs_test.go`; the envelope's `data` stays free-form for a
  verb that adds no contract.
- `make schemas` rewrites `skills/verdict/schemas/*.schema.json`;
  `make schemas-check` proves no drift. A breaking shape additionally needs
  `make schemas-baseline` and a reviewed baseline diff.
- `make tsgen` rewrites the Go-derived `clients/ts/src/refusal-codes.gen.ts`;
  a new refusal code needs its row in `internal/archtest/refusalcodes_test.go`.
  `node clients/ts/scripts/gen-ts-types.mjs` regenerates
  `clients/ts/src/types.gen.ts` from the published envelope schema.

## 6. Go facade

`pkg/verdict/verbs.go`: `<Verb>Request`/`<Verb>Response` and
`func (c *Client) <Verb>(ctx context.Context, req *<Verb>Request)`. Regenerate
the API golden with `go doc -all ./pkg/verdict > pkg/verdict/api.txt`. A verb
the facade must not expose goes in `cliOnlyVerbs`; a deliberate missing method
goes in `sdkGaps` — both in `internal/archtest/surfaces_test.go`, each with a
reason, and both fail when they go stale.

## 7. TypeScript client

`clients/ts/src/client.ts`: the `VerdictClient` interface method, the
`run("<verb>", args, opts)` entry in the implementation, and a case in
`clients/ts/test/client.test.mjs`. `make clients-ts` runs the generation check,
typecheck, and client tests.

## 8. Docs and templates

- `docs/manual.md`: the verb list under "Machine surface", the verb's own
  section (the `## Trend` or `## Local review` style heading), and the
  changelog line.
- `CONTRIBUTING.md`: the "Verbs are:" list.
- `.github/ISSUE_TEMPLATE/bug_report.yml` and `feature_request.yml`: the verb
  dropdown options.
- `skills/verdict/SKILL.md`: only when the agent's task router needs a row.

## 9. Tests and goldens

Unit tests beside the service and adapter, `internal/readgolden/testdata/<verb>/`
human and JSON cases (`go test ./internal/readgolden -update`), and an eval
under `evals/cases/` when the verb has a machine contract
(`./evals/one.sh <case>`). A new flag needs its refusal paths and exit codes
pinned.

## 10. Changeset

Add `.changeset/<slug>.md` with `"@stainless-code/verdict": minor` for a new
verb (`patch` for a flag or fix; see `CONTRIBUTING.md` § Versioning).

## 11. Green

Run `make build`, `make vet`, `make test-race`, `make evals`,
`make schemas-check`, `make clients-ts`, `golangci-lint run ./...`,
`gofmt -l .`, and `bun fmt --check .`.

Then the five surfaces and the two allowlists are green: CLI, MCP, Go facade,
TypeScript client, and completion; `cliOnlyVerbs` and `sdkGaps` unchanged or
widened deliberately.
