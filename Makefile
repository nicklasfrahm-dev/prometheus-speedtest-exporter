REGISTRY	:= ghcr.io
REPO		:= nicklasfrahm-dev/prometheus-speedtest-exporter
SOURCES		:= $(shell find . -name "*.go")
PLATFORM	?= $(shell go version | cut -d " " -f 4)
GOOS		:= $(shell echo $(PLATFORM) | cut -d "/" -f 1)
GOARCH		:= $(shell echo $(PLATFORM) | cut -d "/" -f 2)
VERSION		?= $(shell git describe --tags --always --dirty)
BUILD_FLAGS	:= -ldflags="-s -w -X main.version=$(VERSION)"
PLATFORMS	?= linux/amd64,linux/arm64

BINARY		?= bin/prometheus-speedtest-exporter

GOLANGCI_LINT_VERSION	:= v2.12.2
GOLANGCI_LINT		:= bin/golangci-lint

.DEFAULT_GOAL := help

.PHONY: run
run: ## Run the exporter with go run
	go run $(BUILD_FLAGS) ./cmd/prometheus-speedtest-exporter

.PHONY: build
build: $(BINARY) ## Build the binary

$(BINARY): $(SOURCES)
	@mkdir -p $(@D)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(BUILD_FLAGS) -o $(BINARY) ./cmd/prometheus-speedtest-exporter

$(GOLANGCI_LINT):
	@mkdir -p $(@D)
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run the linter
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## Run the test suite
	go test -race ./...

.PHONY: container
container: ## Build the container image for PLATFORMS (default: linux/amd64,linux/arm64)
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(REGISTRY)/$(REPO):latest \
		-t $(REGISTRY)/$(REPO):$(VERSION) \
		--build-arg VERSION=$(VERSION) \
		-f Containerfile .

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
