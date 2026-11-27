VERSION			?= $(shell git describe --tags --always)
export VERSION
TREE_STATE		?= $(shell git describe --always --dirty --exclude='*' | grep -q dirty && echo dirty || echo clean)
export TREE_STATE
COMMIT			?= $(shell git rev-parse HEAD)
export COMMIT
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

# Set up the linter. Version pinned via `tool` directive in go.mod.
LINTER := go tool golangci-lint

##@ Development

# All runtime env vars are set in compose.yml; overrides go on the make command line.

KOMMODITY_PORT     ?= 5000
COMPOSE_PROJECT_NAME ?= kommodity
# Single port knob: Caddy publishes on it and KOMMODITY_BASE_URL is derived from it.
CADDY_HOST_PORT ?= 5443
KOMMODITY_BASE_URL ?= https://localhost:$(CADDY_HOST_PORT)
export CADDY_HOST_PORT
export KOMMODITY_BASE_URL

generate-caddyfile: # Generate a Caddyfile for local development. Caddy listens in-container on
# CADDY_HOST_PORT (same as the published host port).
	echo "$(KOMMODITY_BASE_URL) {\n  reverse_proxy kommodity:$(KOMMODITY_PORT) {\n    transport http {\n      versions h2c\n    }\n  }\n  tls internal\n}" > Caddyfile

.PHONY: compose-up
compose-up: generate-caddyfile ## Start the docker compose stack (builds the kommodity image). Optional: COMPOSE_PROJECT_NAME=<name> or make compose-up COMPOSE_PROJECT_NAME=<name>.
	docker compose -p $(COMPOSE_PROJECT_NAME) up -d --build --force-recreate

.PHONY: compose-down
compose-down: ## Shut down the docker compose stack. Optional: COMPOSE_PROJECT_NAME=<name> (same one used for compose-up).
	docker compose -p $(COMPOSE_PROJECT_NAME) down --remove-orphans -v
	rm -f Caddyfile

.PHONY: compose-down-all
compose-down-all: ## Shut down every compose stack started from this compose.yml.
	for name in $$(docker compose ls --all --format json 2>/dev/null \
		| jq -r '.[] | select(.ConfigFiles | contains("$(CURDIR)/compose.yml")) | .Name'); do \
		docker compose -p $$name down --remove-orphans -v; \
	done
	rm -f Caddyfile

.PHONY: select-in-local-db
select-in-local-db: ## Query the compose stack's kine table via docker exec.
	docker compose -p $(COMPOSE_PROJECT_NAME) exec postgres \
		psql -U kommodity -d kommodity -c "SELECT * FROM kine;"

.PHONY: setup
setup: generate compose-up

.PHONY: fetch-providers
fetch-providers:
	./scripts/fetch-providers.sh
	./scripts/add-to-scheme-providers.sh
	./scripts/generate-provider-consts.sh

build: bin/kommodity ## Build the application.

build-api: bin/kommodity ## Build the api

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

lint: ## Run the linter.
	$(LINTER) run
	cd pkg/test && go tool -modfile=../../go.mod golangci-lint run

lint-fix: ## Run the linter and fix issues.
	$(LINTER) run --fix
	cd pkg/test && go tool -modfile=../../go.mod golangci-lint run --fix

generate: fetch-providers ## Run code generation.
	go generate ./...
	# openapi-gen has a bug where it strips the spec import from
	# intstr/zz_generated.openapi.go for method-driven types like IntOrString.
	git checkout -- pkg/openapi/intstr/zz_generated.openapi.go

.PHONY: teardown
teardown: compose-down ## Tear down the local development environment.

.PHONY: build-image
build-image: ## Build the Docker image.
	docker buildx build \
	-f Containerfile \
	-t kommodity:latest \
	. \
	--build-arg VERSION=$(VERSION) \
	--build-arg TREE_STATE=$(TREE_STATE) \
	--build-arg COMMIT=$(COMMIT) \
	--load


setup-kind-management-cluster:
	./scripts/setup-kind-management-cluster.sh

delete-kind-management-cluster:
	kind delete cluster --name kind-management

.PHONY: run-scaleway-integration-test
run-scaleway-integration-test: ## Runs Scaleway integration tests (requires Docker)
	cd pkg/test && go test -run TestCreateScalewayCluster -v -timeout 15m

.PHONY: run-hetzner-integration-test
run-hetzner-integration-test: ## Runs Hetzner integration tests (requires make build-image, HCLOUD_TOKEN and a Talos snapshot)
	cd pkg/test && go test -run TestCreateHetznerCluster -v -timeout 30m

.PHONY: run-kubevirt-integration-test
run-kubevirt-integration-test: ## Runs KubeVirt integration tests (requires Docker and kubectl)
	cd pkg/test && go test -run TestCreateKubevirtCluster -v -timeout 15m

.PHONY: run-byot-integration-test
run-byot-integration-test: ## Runs BYOT integration tests (requires Docker)
	cd pkg/test && go test -run 'TestByotCluster' -v -timeout 60m -parallel 2

.PHONY: run-helm-unit-tests
run-helm-unit-tests:
	helm unittest charts/*

.PHONY: terraform-docs
terraform-docs: ## Regenerate README.md for all Terraform modules.
	@for module in terraform/modules/*/; do \
		echo "Generating docs for $$module"; \
		terraform-docs markdown table --output-file README.md $$module; \
	done
