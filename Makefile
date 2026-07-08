BINARY := kubetui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race vet fmt lint run clean tidy spikes

build: ## Build the kubetui binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/kubetui

test: ## Run unit tests
	go test -timeout 120s ./...

race: ## Run tests under the race detector
	go test -race -timeout 180s ./...

vet: ## go vet
	go vet ./...

fmt: ## Format sources
	gofmt -w internal cmd hack

lint: ## Run golangci-lint (must be installed)
	golangci-lint run ./...

run: build ## Build and run against the current kubeconfig context
	./$(BINARY)

tidy: ## Tidy modules
	go mod tidy

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist

# Risk spikes (need a real cluster) — see hack/README.md
spikes: ## Build the hack/ risk-spike programs
	go build ./hack/...

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
