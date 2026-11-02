# Verdict Go builds. `make build` produces bin/verdict, the conventional
# repo-local binary the shell dispatches to under VERDICT_IMPL=go (go-port 02).
# Distribution stays undecided until ticket 08: this target is local-only.
#
# `make ci-go` runs everything the CI go job gates on except golangci-lint
# (CI runs the pinned official action so the repo config lints exactly as
# merged). `make evals-go` builds bin/verdict and runs the full eval suite
# against the Go implementation (CI runs both legs: default and evals-go).
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

.PHONY: evals-go
evals-go: build
	test -x "$(CURDIR)/$(BIN)" || { echo "evals-go: $(BIN) missing after build (not silently testing bash)" >&2; exit 1; }
	VERDICT_IMPL=go VERDICT_BIN="$(CURDIR)/$(BIN)" ./evals/run.sh

.PHONY: clean
clean:
	rm -f $(BIN)

.PHONY: schemas
schemas:
	$(GO) run ./cmd/schemagen -write

.PHONY: schemas-check
schemas-check:
	$(GO) run ./cmd/schemagen
