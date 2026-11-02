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
