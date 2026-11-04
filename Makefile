# Verdict Go builds. `make build` produces bin/verdict, the built binary
# every eval case invokes via $EVAL_CLI (go-port 08: the binary is the only
# front door; no bash/shell leg remains).
#
# `make ci-go` runs everything the CI go job gates on except golangci-lint
# (CI runs the pinned official action so the repo config lints exactly as
# merged). `make evals` builds bin/verdict and runs the full eval suite
# against it (CI runs this on macOS and Ubuntu).
GO := go
BIN := bin/verdict
GOVULNCHECK_VERSION ?= v1.9.0
# Pinned deadcode for the unreachable-function ratchet (make deadcode).
DEADCODE_VERSION ?= v0.52.0
# Pinned GoReleaser for the release train (also pinned in
# .github/workflows/release.yml and ci.yml). Install outside the repo when
# missing: GOBIN=/tmp/grbin go install
# github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION).
GORELEASER ?= goreleaser
GORELEASER_VERSION ?= v2.19.0

.PHONY: build
build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/verdict
	@# The skill shim finds a binary at <skill>/bin/verdict; point a checkout's
	@# skill dir at the build output (untracked, gitignored, never shipped).
	ln -sfn ../../bin skills/verdict/bin

.PHONY: hooks
hooks:
	git config core.hooksPath hooks
	@echo "hooks: pre-commit + pre-push wired ($(CURDIR)/hooks, relative to the checkout)"

.PHONY: gofmt-check
gofmt-check:
	test -z "$$(gofmt -l .)"

.PHONY: vet
vet:
	$(GO) vet ./...
	$(GO) mod verify

.PHONY: cross
cross:
	sh scripts/cross.sh

.PHONY: test-race
test-race:
	$(GO) test -race -shuffle=on -count=1 ./...

# fuzz runs every fuzz target for FUZZTIME each; the nightly ci.yml lane calls
# it. An internal/archtest guard fails when a target in the tree is missing
# here, or when an entry names a package that does not declare it.
FUZZTIME ?= 30s
FUZZ_TARGETS := \
	./internal/core/threads:FuzzProviderMappers \
	./internal/hunks:FuzzFiles \
	./internal/hunks:FuzzHeaderPaths \
	./internal/hunks:FuzzParse \
	./internal/jq:FuzzDecodeJSONLOrdered \
	./internal/jq:FuzzDecodeOrdered \
	./internal/mcp:FuzzServeFrames \
	./internal/mcpconfig:FuzzMergeJSON \
	./internal/mcpconfig:FuzzMergeTOML \
	./internal/release:FuzzExtractTarGz \
	./internal/release:FuzzExtractZip

.PHONY: fuzz
fuzz:
	@set -eu; for target in $(FUZZ_TARGETS); do \
		pkg=$${target%%:*}; fn=$${target##*:}; \
		echo "fuzz: $$fn in $$pkg for $(FUZZTIME)"; \
		$(GO) test -run='^$$' -fuzz="^$$fn$$" -fuzztime=$(FUZZTIME) "$$pkg"; \
	done

.PHONY: vuln
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# deadcode fails on an unreachable function that scripts/deadcode-baseline.txt
# does not record (and on a stale baseline line). --test counts helpers
# reachable only from tests.
.PHONY: deadcode
deadcode:
	sh scripts/deadcode.sh $(DEADCODE_VERSION)

.PHONY: ci-go
ci-go: gofmt-check schemas-check vet deadcode test-race vuln

.PHONY: evals
evals: build
	test -x "$(CURDIR)/$(BIN)" || { echo "evals: $(BIN) missing after build (not silently testing nothing)" >&2; exit 1; }
	EVAL_CLI="$(CURDIR)/$(BIN)" ./evals/run.sh

# evals-go stays as an alias for muscle memory; the suite is binary-only.
.PHONY: evals-go
evals-go: evals

.PHONY: clean
clean:
	rm -f $(BIN)

.PHONY: schemas
schemas:
	$(GO) run ./cmd/schemagen -write

.PHONY: schemas-check
schemas-check:
	$(GO) run ./cmd/schemagen

# schemas-baseline snapshots the currently accepted published schemas. The
# additive-evolution test compares the generator against this snapshot; update
# it, in the same commit, only for a deliberate breaking change.
.PHONY: schemas-baseline
schemas-baseline:
	$(GO) run ./cmd/schemagen -write -dir internal/schemagen/testdata/baseline

# formula-audit renders dist/verdict.rb and grades it with brew style and
# brew audit --strict inside a throwaway, trusted local tap (Homebrew 7 refuses
# both on a bare file). Needs Homebrew; skips with a notice where it is absent.
# `make release-dry-run` and scripts/release.sh (before the tap push) both call
# it, so the tap never carries a formula that style or audit rejects.
.PHONY: formula-audit
formula-audit:
	node scripts/generate-formula.mjs --dist dist --out dist/verdict.rb
	sh scripts/brew-audit.sh dist/verdict.rb

# release-dry-run proves the whole release train without publishing
# anything: snapshot archives, the TS client build the wrapper bundles, the
# npm tree, the tap formula (rendered and brew-audited), `npm pack` content
# checks per package, and the contract tests (shim resolution included). No
# tag, no cosign, no attestation, no registry, no tap push. Needs `bun
# install` (the client build) and node for the npm scripts. It also extracts
# the release notes GoReleaser will publish, so an unreadable CHANGELOG fails
# here rather than mid-release.
.PHONY: release-dry-run
release-dry-run:
	@[ -x node_modules/.bin/tsc ] || { echo "release-dry-run: node_modules/.bin/tsc missing: run bun install first" >&2; exit 1; }
	$(GORELEASER) check
	node scripts/release-notes.mjs "$$(node -p "require('./package.json').version")" > /dev/null
	rm -rf dist
	$(GORELEASER) release --snapshot --clean --skip=sign,sbom
	mkdir -p dist/fakebin
	cp scripts/fake-npm.sh dist/fakebin/npm
	chmod +x dist/fakebin/npm
	node clients/ts/scripts/gen-ts-types.mjs --check
	node_modules/.bin/tsc -p clients/ts/tsconfig.json
	PATH="$(CURDIR)/dist/fakebin:$(PATH)" node scripts/publish-npm.mjs --dry-run --require-client
	node scripts/generate-formula.mjs --dist dist --out dist/verdict.rb
	sh scripts/brew-audit.sh dist/verdict.rb
	for d in dist/npm/*/; do (cd "$$d" && npm pack --dry-run > /dev/null); done
	$(GO) test -count=1 ./release/

# clients-ts builds, drift-gates, tests, and typechecks the official TS
# client (clients/ts). The client ships as the ./client subpath export of
# the npm wrapper; generate-npm.mjs bundles clients/ts/dist when present.
# Two gates: gen-ts-types.mjs --check covers the schema/codegen sources (Go
# free, Node only), `go run ./cmd/tsgen` covers the refusal codes rendered
# from the Go refusal-code registry (internal/core/refusalcodes.go).
.PHONY: clients-ts
clients-ts: build
	go run ./cmd/tsgen
	node clients/ts/scripts/gen-ts-types.mjs --check
	node_modules/.bin/tsc -p clients/ts/tsconfig.json
	VERDICT_BIN="$(CURDIR)/$(BIN)" node --test clients/ts/test/client.test.mjs
	node_modules/.bin/tsc --noEmit -p clients/ts/tsconfig.types.json

# tsgen rewrites the TS client sources derived from Go (the refusal codes);
# the check form runs inside clients-ts.
.PHONY: tsgen
tsgen:
	go run ./cmd/tsgen -write
