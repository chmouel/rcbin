BINARY      := rc
PKG         := ./...
CMD         := ./cmd/rc
PREFIX      ?= $(HOME)/.local
BINDIR      ?= $(PREFIX)/bin

GO          ?= go
GOFLAGS     ?=
GOBIN       := $(CURDIR)/bin

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the rc binary into ./bin
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(GOBIN)/$(BINARY) $(CMD)

.PHONY: install
install: ## Install rc into $(BINDIR)
	install -d $(BINDIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/$(BINARY) $(CMD)

.PHONY: test
test: ## Run all tests
	$(GO) test $(GOFLAGS) $(PKG)

.PHONY: race
race: ## Run all tests with the race detector
	$(GO) test $(GOFLAGS) -race $(PKG)

.PHONY: cover
cover: ## Run tests and write a coverage profile to coverage.out
	$(GO) test $(GOFLAGS) -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: fmt
fmt: ## Format all Go sources
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	$(GO) mod tidy

.PHONY: golangci
golangci: ## Run golangci-lint
	golangci-lint run

.PHONY: lint
lint: fmt-check vet golangci ## Run gofmt check, go vet, and golangci-lint

.PHONY: check
check: lint test ## Run lint and tests

.PHONY: cross
cross: ## Cross-compile for linux and darwin (amd64/arm64)
	GOOS=linux  GOARCH=amd64 $(GO) build -ldflags '$(LDFLAGS)' -o $(GOBIN)/$(BINARY)-linux-amd64   $(CMD)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags '$(LDFLAGS)' -o $(GOBIN)/$(BINARY)-darwin-amd64  $(CMD)
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags '$(LDFLAGS)' -o $(GOBIN)/$(BINARY)-darwin-arm64  $(CMD)

.PHONY: run
run: ## Build and run rc (use ARGS="...")
	$(GO) run $(CMD) $(ARGS)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(GOBIN) coverage.out

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
