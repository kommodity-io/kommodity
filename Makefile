VERSION		:= $(shell git describe --tags --always --dirty)
GO_FLAGS	:= -ldflags "-X 'main.Version=$(VERSION)'"
SOURCES		:= $(shell find . -name '*.go')

##@ Development

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: run
run: bin/kommodity ## Run the application locally.
	bin/kommodity

build: bin/kommodity ## Build the application.

bin/kommodity: $(SOURCES) ## Build the application.
	go build $(GO_FLAGS) -o bin/kommodity cmd/kommodity/main.go

.PHONY: clean
clean: ## Clean the build artifacts.
	rm -f bin/kommodity

.PHONY: test
test: ## Run the tests.
	go test -cover -v ./...
