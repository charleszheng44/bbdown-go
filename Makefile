# bbdown-go — development Makefile.
# Targets mirror the checks run in .github/workflows/ci.yml so local and CI
# results stay aligned.

GO        ?= go
GOFMT     ?= gofmt
BIN_DIR   ?= bin
BIN_NAME  ?= bbdown
PKG       ?= ./...
MAIN_PKG  ?= ./cmd/bbdown

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## Format all Go files in place.
	$(GOFMT) -w .

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is unformatted (matches CI).
	@out=$$($(GOFMT) -l .); \
	if [ -n "$$out" ]; then \
		echo "unformatted files:"; \
		echo "$$out"; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## Run staticcheck (requires honnef.co/go/tools/cmd/staticcheck).
	@command -v staticcheck >/dev/null || { \
		echo "staticcheck not found; install with:"; \
		echo "  go install honnef.co/go/tools/cmd/staticcheck@latest"; \
		exit 1; \
	}
	staticcheck $(PKG)

.PHONY: test
test: ## Run tests with the race detector.
	$(GO) test -race -count=1 $(PKG)

.PHONY: cover
cover: ## Run tests and write coverage.out.
	$(GO) test -race -count=1 -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: build
build: ## Build the bbdown binary into $(BIN_DIR).
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BIN_DIR)/$(BIN_NAME) $(MAIN_PKG)

.PHONY: install
install: ## Install bbdown to $$GOBIN / $$GOPATH/bin.
	$(GO) install -trimpath $(MAIN_PKG)

.PHONY: tidy
tidy: ## Run go mod tidy.
	$(GO) mod tidy

.PHONY: ci
ci: fmt-check vet test build ## Run the same checks as CI (minus staticcheck).

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf $(BIN_DIR) coverage.out
