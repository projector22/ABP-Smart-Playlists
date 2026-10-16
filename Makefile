BINARY   := abp
PKG      := ./...
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
PREFIX   ?= $(HOME)/.local
BINDIR   ?= $(PREFIX)/bin
DIST     := dist
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

GO       ?= go

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build ./abp for this machine
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BINARY) .

.PHONY: run
run: ## Run the TUI (go run)
	$(GO) run -ldflags '-X main.version=$(VERSION)' .

.PHONY: install
install: build ## Install to $(BINDIR) (override with PREFIX=/usr/local)
	install -d $(BINDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY)"
	@echo "note: re-run 'abp cron install' if the binary moved, cron stores the absolute path"

.PHONY: uninstall
uninstall: ## Remove the installed binary
	rm -f $(BINDIR)/$(BINARY)

.PHONY: dist
dist: ## Cross-compile release tarballs into ./dist
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		name=$(BINARY)-$(VERSION)-$$os-$$arch; \
		echo "building $$name"; \
		mkdir -p $(DIST)/$$name && \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -ldflags '$(LDFLAGS)' -o $(DIST)/$$name/$(BINARY) . && \
		cp README.md $(DIST)/$$name/ && \
		tar -C $(DIST) -czf $(DIST)/$$name.tar.gz $$name && \
		rm -rf $(DIST)/$$name || exit 1; \
	done
	@cd $(DIST) && (sha256sum *.tar.gz 2>/dev/null || shasum -a 256 *.tar.gz) > checksums.txt
	@ls -1 $(DIST)

##@ Quality

.PHONY: test
test: ## Run unit tests
	$(GO) test $(PKG)

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race $(PKG)

.PHONY: cover
cover: ## Run tests and write coverage.html
	$(GO) test -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@$(GO) tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format code with gofmt
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs gofmt
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint (if installed)
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; \
	else echo "golangci-lint not installed, skipping (https://golangci-lint.run)"; fi

.PHONY: check
check: fmt-check vet test ## Everything CI should run

##@ Housekeeping

.PHONY: tidy
tidy: ## go mod tidy (generates go.sum)
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY) $(DIST) coverage.out coverage.html
