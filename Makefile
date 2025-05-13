VERSION		?= $(shell git describe --tags --always --dirty)
GO_FLAGS	:= -ldflags "-X 'main.version=$(VERSION)'"
SOURCES		:= $(shell find . -name '*.go')
UPX_FLAGS	?= -qq

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Dependencies

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Set up the linter.
LINTER := $(GOBIN)/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(LINTER) ## Download golangci-lint locally if necessary.
$(LINTER):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

##@ Development

.PHONY: run
run: ## Run the application locally.
	go run $(GO_FLAGS) cmd/kommodity/main.go

build: bin/kommodity ## Build the application.

bin/kommodity: $(SOURCES) ## Build the application.
	go build $(GO_FLAGS) -o bin/kommodity cmd/kommodity/main.go
ifneq ($(UPX_FLAGS),)
	upx $(UPX_FLAGS) bin/kommodity
endif

.PHONY: clean
clean: ## Clean the build artifacts.
	rm -f bin/kommodity

.PHONY: test
test: ## Run the tests.
	go test -cover -v ./...

lint: $(LINTER) ## Run the linter.
	$(LINTER) run
