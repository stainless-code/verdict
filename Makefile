# Verdict Go builds. `make build` produces bin/verdict, the conventional
# repo-local binary the shell dispatches to under VERDICT_IMPL=go (go-port 02).
# Distribution stays undecided until ticket 08: this target is local-only.
GO := go
BIN := bin/verdict

.PHONY: build
build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/verdict

.PHONY: clean
clean:
	rm -f $(BIN)
