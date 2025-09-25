VERSION			?= $(shell git describe --tags --always)
TREE_STATE      ?= $(shell git describe --always --dirty --exclude='*' | grep -q dirty && echo dirty || echo clean)
COMMIT			?= $(shell git rev-parse HEAD)
BUILD_DATE		?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GO_FLAGS		:= -ldflags "-X 'k8s.io/component-base/version.gitVersion=$(VERSION)' -X 'k8s.io/component-base/version.gitTreeState=$(TREE_STATE)' -X 'k8s.io/component-base/version.buildDate=$(BUILD_DATE)' -X 'k8s.io/component-base/version.gitCommit=$(COMMIT)'"
SOURCES			:= $(shell find . -name '*.go')
UPX_FLAGS		?= -qq

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

# Set up grpcurl.
GRPCURL := $(GOBIN)/grpcurl

.PHONY: grpcurl
grpcurl: $(GRPCURL) ## Download grpcurl locally if necessary.
$(GRPCURL):
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# Set up the linter.
LINTER := bin/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(LINTER) ## Download golangci-lint locally if necessary.
$(LINTER):
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b bin/ v2.4.0

##@ Development

.PHONY: .env
.env: ## Create a .env file from the template. Use sed to only add if it does not already exist.
	touch .env
	grep -q '^KOMMODITY_DB_URI=' .env || echo 'KOMMODITY_DB_URI=postgres://kommodity:kommodity@localhost:5432/kommodity?sslmode=disable' >> .env
	grep -q '^KOMMODITY_KINE_URI=' .env || echo 'KOMMODITY_KINE_URI=http://localhost:2379' >> .env
	grep -q '^KOMMODITY_PORT=' .env || echo 'KOMMODITY_PORT=8080' >> .env
	grep -q '^KOMMODITY_INSECURE_DISABLE_AUTHENTICATION=' .env || echo 'KOMMODITY_INSECURE_DISABLE_AUTHENTICATION=true' >> .env

.PHONY: setup
setup: generate
	docker compose up -d --build --force-recreate

.PHONY: run
run: ## Run the application locally.
	LOG_FORMAT=console \
	LOG_LEVEL=info \
	go run $(GO_FLAGS) cmd/kommodity/main.go

.PHONY: fetch-providers
fetch-providers: 
	./scripts/fetch-providers.sh
	./scripts/add-to-scheme-providers.sh

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

lint-fix: $(LINTER) ## Run the linter and fix issues.
	$(LINTER) run --fix

generate: .env fetch-providers ## Run code generation.
	go generate ./...

teardown: ## Tear down the local development environment.
	docker compose down --remove-orphans -v
	rm -f .env
