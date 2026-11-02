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

.PHONY: gofmt-check
gofmt-check:
	test -z "$$(gofmt -l .)"

.PHONY: vet
vet:
	$(GO) vet ./...
	$(GO) mod verify

.PHONY: test-race
test-race:
	$(GO) test -race -count=1 ./...

.PHONY: vuln
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: ci-go
ci-go: gofmt-check schemas-check vet test-race vuln

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

# release-dry-run proves the whole release train without publishing
# anything: snapshot archives, the npm tree, the tap formula, `npm pack`
# content checks per package, and the contract tests (shim resolution
# included). No tag, no cosign, no attestation, no registry, no tap push.
.PHONY: release-dry-run
release-dry-run:
	$(GORELEASER) check
	rm -rf dist
	$(GORELEASER) release --snapshot --clean --skip=sign,sbom
	mkdir -p dist/fakebin
	cp scripts/fake-npm.sh dist/fakebin/npm
	chmod +x dist/fakebin/npm
	PATH="$(CURDIR)/dist/fakebin:$(PATH)" node scripts/publish-npm.mjs --dry-run
	node scripts/generate-formula.mjs
	for d in dist/npm/*/; do (cd "$$d" && npm pack --dry-run > /dev/null); done
	$(GO) test -count=1 ./release/

# clients-ts builds, drift-gates, tests, and typechecks the official TS
# client (clients/ts). The client ships as the ./client subpath export of
# the npm wrapper; generate-npm.mjs bundles clients/ts/dist when present.
.PHONY: clients-ts
clients-ts:
	node clients/ts/scripts/gen-ts-types.mjs --check
	node_modules/.bin/tsc -p clients/ts/tsconfig.json
	VERDICT_BIN="$(CURDIR)/$(BIN)" node --test clients/ts/test/client.test.mjs
	node_modules/.bin/tsc --noEmit -p clients/ts/tsconfig.types.json
