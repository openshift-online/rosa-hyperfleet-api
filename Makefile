.PHONY: help build test test-unit test-integration lint clean \
	build-hyperfleet-db build-operator build-api build-api-codegen \
	test-hyperfleet-db test-operator test-operator-int test-api test-api-int test-api-codegen test-clientset \
	coverage-api-codegen \
	test-e2e test-e2e-api test-e2e-cli test-e2e-platform-monitoring test-e2e-zoa test-e2e-authz test-e2e-sdk test-e2e-rosa-cli \
	test-e2e test-e2e-api test-e2e-cli test-e2e-platform-monitoring test-e2e-authz test-e2e-sdk \
	e2e-authz-infra-up e2e-authz-infra-down e2e-init-db \
	fmt vet verify verify-mod deps mod-tidy \
	manifests generate generate-deepcopy generate-clientset verify-clientset setup-envtest \
	codegen-passthrough codegen-registry codegen-verify codegen verify-codegen \
    codegen-passthrough-clobber \
	codegen-conversion verify-conversion \
	generate-openapi verify-openapi swagger-ui \
	image-api image-operator image-push-api image-push-operator

# ── Configuration ────────────────────────────────────────────────────────

IMAGE_REPO_API      ?= quay.io/openshift-online/rosa-hyperfleet-api
IMAGE_REPO_OPERATOR ?= quay.io/openshift-online/hyperfleet-operator
IMAGE_TAG           ?= latest
GIT_SHA             := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GOOS                ?= linux
GOARCH              ?= amd64
PLATFORMS           ?= linux/amd64,linux/arm64

TEST_OUTPUT_DIR     ?= $(or $(ARTIFACT_DIR),./test-results)
DYNAMODB_ENDPOINT   ?= http://localhost:8180
CEDAR_AGENT_ENDPOINT?= http://localhost:8181

AWS_PROFILE ?=
AWS_REGION  ?=
FOCUS       ?=
SKIP        ?= Authz

ROSA_REPO_URL          ?= https://github.com/openshift/rosa
ROSA_REPO_BRANCH       ?= hyperfleet-v2
ROSA_MAKE_TARGET       ?= e2e-hyperfleet
ROSA_BUILD_TARGET      ?= install
ROSA_GINKGO_FOCUS      ?=
ROSA_GINKGO_SKIP       ?=
ROSA_GINKGO_LABEL_FILTER ?=

CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

TOOLS_DIR        := ./hack/tools
TOOLS_BIN_DIR    := $(TOOLS_DIR)/bin
GOLANGCI_LINT    := $(abspath $(TOOLS_BIN_DIR)/golangci-lint)
CONTROLLER_GEN   := $(abspath $(TOOLS_BIN_DIR)/controller-gen)
CLIENT_GEN       := $(abspath $(TOOLS_BIN_DIR)/client-gen)
BRIDGE_GEN         := $(abspath $(TOOLS_BIN_DIR)/bridge-gen)
SETUP_ENVTEST    := $(abspath $(TOOLS_BIN_DIR)/setup-envtest)
GINKGO           := $(abspath $(TOOLS_BIN_DIR)/ginkgo)

# ── SDK generation ───────────────────────────────────────────────────────
SDK_MODULE        ?= github.com/openshift-online/rosa-hyperfleet-api
SDK_API_PKG       ?= $(SDK_MODULE)/api
SDK_INPUT         ?= v1alpha1/public
SDK_CLIENTSET     ?= generated
SDK_OUTPUT_DIR    ?= $(abspath clientset)
SDK_OUTPUT_PKG    ?= $(SDK_MODULE)/clientset
BRIDGE_INPUT_DIR        ?= $(abspath api/v1alpha1/public)
BRIDGE_OUTPUT_DIR       ?= $(abspath clientset/transport)
BRIDGE_OUTPUT_PKG       ?= transport
PLATFORM_OUTPUT_DIR   ?= $(abspath clientset/platform)
PLATFORM_OUTPUT_PKG   ?= platform
TYPED_PKG_IMPORT      ?= $(SDK_MODULE)/clientset/generated/typed/v1alpha1/public
API_PKG_IMPORT        ?= $(SDK_MODULE)/api/v1alpha1/public
SDK_HEADER_FILE       ?= $(abspath hack/clientset/license-boilerplate.go.txt)

# ── Code generation ───────────────────────────────────────────────────────
OPENAPI_GENERATED ?= api/v1alpha1/public/generated-schemas.json
OPENAPI_SPEC      ?= api/v1alpha1/public/openapi.yaml
SWAGGER_UI_PORT ?= 8282

$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(SETUP_ENVTEST): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest

$(CLIENT_GEN): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/client-gen k8s.io/code-generator/cmd/client-gen

$(BRIDGE_GEN): hack/clientset/cmd/bridge-gen/main.go
	cd hack/clientset/cmd/bridge-gen && go build -o $(BRIDGE_GEN) .

$(GINKGO): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/ginkgo github.com/onsi/ginkgo/v2/ginkgo

# ── Help ─────────────────────────────────────────────────────────────────

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Build:"
	@echo "  build                Build all components"
	@echo "  build-api            Platform API server"
	@echo "  build-operator       Hyperfleet operator (manager + compactor)"
	@echo "  build-hyperfleet-db  Hyperfleet DB library"
	@echo "  build-api-codegen    API codegen tools (build-time generators)"
	@echo ""
	@echo "Test:"
	@echo "  test                 All tests (unit + integration)"
	@echo "  test-unit            Unit tests: API + operator + codegen + clientset (no external services)"
	@echo "  test-clientset       Clientset unit tests (transport, platform)"
	@echo "  test-integration     Integration tests: FleetDB + operator (podman) + API handlers"
	@echo "  test-api-int         API handler integration tests (build tag: integration)"
	@echo "  test-e2e-authz       E2E authz (starts local infra)"
	@echo "  test-e2e-api         E2E API"
	@echo "  test-e2e-cli         E2E CLI"
	@echo "  test-e2e-sdk         E2E SDK (Go clientset lifecycle)"
	@echo "  test-e2e-rosa-cli    E2E rosa CLI (clones rosa repo, builds CLI, runs hyperfleet tests)"
	@echo "                       Supports ROSA_GINKGO_FOCUS, ROSA_GINKGO_SKIP, ROSA_GINKGO_LABEL_FILTER"
	@echo "  test-e2e-platform-monitoring  E2E monitoring"
	@echo ""
	@echo "  coverage-api-codegen Coverage report for codegen (hack/api-codegen)"
	@echo ""
	@echo "Code Quality:"
	@echo "  lint                 golangci-lint on all modules"
	@echo "  fmt                  Format Go source"
	@echo "  vet                  go vet on all modules"
	@echo "  verify-mod           Verify go.mod tidiness"
	@echo ""
	@echo "Code Generation:"
	@echo "  manifests            Generate CRD manifests (controller-gen + CEL strip)"
	@echo "  generate             Run all code generators in one pass"
	@echo "  generate-deepcopy    Generate deepcopy methods only"
	@echo "  verify               Fail if any generated output is out of date"
	@echo "  generate-clientset   Generate typed client SDK from CRD types"
	@echo "  verify-clientset     Fail if generated clientset is out of date"
	@echo "  codegen-passthrough  Generate passthrough types from HyperShift"
	@echo "  codegen-registry     Generate field metadata registry from markers"
	@echo "  codegen-verify       Verify codegen outputs compile"
	@echo "  codegen              Run full codegen pipeline (passthrough + registry + verify)"
	@echo "  verify-codegen       Fail if codegen outputs are out of date"
	@echo "  codegen-conversion   Generate REST types and conversion functions from CRD types"
	@echo "  verify-conversion    Fail if conversion outputs are out of date"
	@echo "  generate-openapi     Generate and merge typed schemas into OpenAPI spec"
	@echo "  verify-openapi       Fail if OpenAPI spec is out of date with codegen"
	@echo "  swagger-ui           Run Swagger UI locally (default port 8282)"
	@echo "  setup-envtest        Install envtest binaries (etcd, kube-apiserver)"
	@echo "  deps                 Download and tidy all modules"
	@echo ""
	@echo "Images:"
	@echo "  image-api            Platform API image"
	@echo "  image-operator       Hyperfleet operator image"

# ── Build ────────────────────────────────────────────────────────────────

build: build-hyperfleet-db build-operator build-api

build-hyperfleet-db:
	cd hyperfleet-db && go build ./...

build-operator:
	cd hyperfleet-operator && go build -o ../bin/manager ./cmd/manager
	cd hyperfleet-operator && go build -o ../bin/compactor ./cmd/compactor

build-api:
	cd platform-api && go build -o ../bin/rosa-hyperfleet-api ./cmd

build-api-codegen:
	cd hack/api-codegen && go build -o ../../bin/passthrough-gen ./cmd/passthrough-gen
	cd hack/api-codegen && go build -o ../../bin/marker-scanner ./cmd/marker-scanner
	cd hack/api-codegen && go build -o ../../bin/openapi-gen ./cmd/openapi-gen
	cd hack/api-codegen && go build -o ../../bin/openapi-merge ./cmd/openapi-merge
	cd hack/api-codegen && go build -o ../../bin/conversion-gen ./cmd/conversion-gen
	cd hack/api-codegen && go build -o ../../bin/crd-variants ./cmd/crd-variants
	cd hack/api-codegen && go build -o ../../bin/featuregate-info ./cmd/featuregate-info
	cd hack/api-codegen && go build -o ../../bin/verify-configuration ./cmd/verify-configuration

# ── Test ─────────────────────────────────────────────────────────────────

test: test-unit test-integration

test-unit: test-api test-operator test-api-codegen test-clientset

test-integration: test-hyperfleet-db test-operator-int test-api-int

test-api-int:
	cd platform-api && go test -v -race -count=1 -tags integration ./pkg/handlers/...

test-api:
	cd platform-api && go test -v -race -count=1 $$(go list ./... | grep -v '/test/e2e')

test-api-codegen:
	cd hack/api-codegen && go test -v -race -count=1 ./...

test-clientset:
	cd clientset && go test -v -race -count=1 ./...

coverage-api-codegen:
	cd hack/api-codegen && go test -race -coverprofile=coverage.out ./...
	cd hack/api-codegen && go tool cover -func=coverage.out
	@echo ""
	@echo "HTML report: hack/api-codegen/coverage.html"
	cd hack/api-codegen && go tool cover -html=coverage.out -o coverage.html

test-operator: $(SETUP_ENVTEST)
	@ASSETS=$$($(SETUP_ENVTEST) use -p path --bin-dir $(ENVTEST_BIN_DIR)) && \
		echo "envtest assets: $$ASSETS" && \
		cd hyperfleet-operator && KUBEBUILDER_ASSETS="$$ASSETS" go test -v -race -count=1 ./internal/...

test-hyperfleet-db:
	cd hyperfleet-db && go test -v -race -count=1 ./...

test-operator-int:
	cd hyperfleet-operator && go test -v -race -count=1 ./test/...

test-e2e: test-e2e-api

test-e2e-api: $(GINKGO)
	E2E_BASE_URL="$${BASE_URL}" E2E_ACCOUNT_ID="$${E2E_ACCOUNT_ID}" \
	E2E_RHOBS_API_URL="$${RHOBS_API_URL}" \
	$(GINKGO) -vv --skip="Authz" \
		$(if $(E2E_LABEL_FILTER),--label-filter="$(E2E_LABEL_FILTER)") \
		--junit-report=junit-api.xml --output-dir=$(TEST_OUTPUT_DIR) \
		./test/e2e-api

test-e2e-cli: $(GINKGO)
	E2E_BASE_URL="$${BASE_URL}" E2E_ACCOUNT_ID="$${E2E_ACCOUNT_ID}" \
	E2E_RHOBS_API_URL="$${RHOBS_API_URL}" \
	E2E_ALERTMANAGER_URL="$${ALERTMANAGER_URL}" \
	ROSACTL_BIN="$${ROSACTL_BIN}" AWS_REGION="$${AWS_REGION}" \
	$(GINKGO) -vv --junit-report=junit-cli.xml \
		$(if $(E2E_LABEL_FILTER),--label-filter="$(E2E_LABEL_FILTER)") \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-cli

test-e2e-platform-monitoring: $(GINKGO)
	E2E_RHOBS_API_URL="$${RHOBS_API_URL}" \
	$(GINKGO) -vv --junit-report=junit-platform-monitoring.xml \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-platform-monitoring

test-e2e-sdk: $(GINKGO)
	E2E_BASE_URL="$${BASE_URL}" \
	E2E_ACCOUNT_ID="$${E2E_ACCOUNT_ID}" \
	E2E_CUSTOMER_ACCOUNT_ID="$${E2E_CUSTOMER_ACCOUNT_ID}" \
	CUSTOMER_AWS_PROFILE="$${CUSTOMER_AWS_PROFILE}" \
	AWS_REGION="$${AWS_REGION}" \
	ROSACTL_BIN="$${ROSACTL_BIN}" \
	HYPERFLEET_VERSION="$${HYPERFLEET_VERSION}" \
	$(GINKGO) -vv --timeout=3h --junit-report=junit-sdk.xml \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-sdk

test-e2e-rosa-cli:
	@echo "Cloning rosa repo into temporary directory..."
	@ROSA_TMPDIR=$$(mktemp -d) && \
	trap "rm -rf $$ROSA_TMPDIR" EXIT && \
	echo "Temporary directory: $$ROSA_TMPDIR" && \
	git clone --depth=1 --branch $(ROSA_REPO_BRANCH) $(ROSA_REPO_URL) $$ROSA_TMPDIR && \
	echo "Building rosa CLI..." && \
	cd $$ROSA_TMPDIR && $(MAKE) $(ROSA_BUILD_TARGET) && \
	export PATH="$$PWD:$$PATH" && \
	echo "Running rosa hyperfleet E2E tests..." && \
	name=$${CLUSTER_NAME:-hf-e2e-$$(date +%s)} && \
	export HYPERFLEET_URL="$${HYPERFLEET_URL}" && \
	export CLUSTER_NAME="$$name" && \
	export OPERATOR_ROLES_PREFIX="$${OPERATOR_ROLES_PREFIX:-$$name}" && \
	export AWS_DEFAULT_REGION="$${AWS_DEFAULT_REGION:-$${AWS_REGION}}" && \
	if [ -n "$(ROSA_GINKGO_FOCUS)$(ROSA_GINKGO_SKIP)$(ROSA_GINKGO_LABEL_FILTER)" ]; then \
		echo "Running with custom ginkgo filters..." && \
		ginkgo run -v --timeout 3h \
			$(if $(ROSA_GINKGO_FOCUS),--focus="$(ROSA_GINKGO_FOCUS)") \
			$(if $(ROSA_GINKGO_SKIP),--skip="$(ROSA_GINKGO_SKIP)") \
			$(if $(ROSA_GINKGO_LABEL_FILTER),--label-filter="$(ROSA_GINKGO_LABEL_FILTER)") \
			./tests/e2e/; \
	else \
		echo "Running default rosa e2e-hyperfleet target..." && \
		$(MAKE) $(ROSA_MAKE_TARGET); \
	fi


# ── E2E Infrastructure ──────────────────────────────────────────────────

e2e-authz-infra-up:
	podman-compose -f hack/podman-compose.e2e-authz.yaml up -d
	@echo "Waiting for services to be ready..."
	@sleep 5
	@$(MAKE) e2e-init-db

e2e-authz-infra-down:
	podman-compose -f hack/podman-compose.e2e-authz.yaml down -v

e2e-init-db:
	./scripts/e2e-init-dynamodb.sh

test-e2e-authz: e2e-authz-infra-up
	@./scripts/run-e2e-authz.sh

# ── Code Quality ─────────────────────────────────────────────────────────

fmt:
	cd hyperfleet-db && go fmt ./...
	cd hyperfleet-operator && go fmt ./...
	cd platform-api && go fmt ./...
	cd hack/api-codegen && go fmt ./...
	cd clientset && go fmt ./...
	cd hack/clientset/cmd/bridge-gen && go fmt ./...

vet:
	cd hyperfleet-db && go vet ./...
	cd hyperfleet-operator && go vet ./...
	cd platform-api && go vet ./...
	cd hack/api-codegen && go vet ./...
	cd clientset && go vet ./...
	cd hack/clientset/cmd/bridge-gen && go vet ./...

lint: $(GOLANGCI_LINT)
	cd hyperfleet-db && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hyperfleet-operator && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd platform-api && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hack/api-codegen && $(GOLANGCI_LINT) run --config ../../.golangci.yml --timeout 5m ./...
	cd clientset && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hack/clientset/cmd/bridge-gen && $(GOLANGCI_LINT) run --config $(abspath .golangci.yml) --timeout 5m ./...

# All Go modules in the repo (used by verify and MintMaker/Renovate post-upgrade).
override MOD_TIDY_DIRS := hyperfleet-db api hyperfleet-operator platform-api test clientset hack/tools hack/api-codegen
MOD_TIDY_FILES := $(foreach d,$(MOD_TIDY_DIRS),$(d)/go.mod $(d)/go.sum)

mod-tidy:
	@set -e; for d in $(MOD_TIDY_DIRS); do \
		echo "go mod tidy: $$d"; \
		(cd "$$d" && go mod tidy); \
	done

verify-mod: mod-tidy
	git diff --exit-code $(MOD_TIDY_FILES)

deps:
	@set -e; for d in $(MOD_TIDY_DIRS); do \
		(cd "$$d" && go mod download && go mod tidy); \
	done

# ── Code Generation ──────────────────────────────────────────────────────

CRD_VARIANTS     := $(abspath bin/crd-variants)
CRD_BASES_DIR    := hyperfleet-operator/config/crd/bases

manifests: $(CONTROLLER_GEN) build-api-codegen
	cd hyperfleet-operator && $(CONTROLLER_GEN) crd:allowDangerousTypes=true paths="../api/v1alpha1" output:crd:dir=config/crd/bases
	$(CRD_VARIANTS) --strip-passthrough-cel --api-dir api/v1alpha1 --crd-dir $(CRD_BASES_DIR)

generate-deepcopy: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths="./api/..."

generate-clientset: codegen-conversion $(CLIENT_GEN) $(BRIDGE_GEN)
	cd api && $(CLIENT_GEN) \
		--input-base "$(SDK_API_PKG)" \
		--input "$(SDK_INPUT)" \
		--clientset-name "$(SDK_CLIENTSET)" \
		--output-dir "$(SDK_OUTPUT_DIR)" \
		--output-pkg "$(SDK_OUTPUT_PKG)" \
		--go-header-file "$(SDK_HEADER_FILE)"
	$(BRIDGE_GEN) \
		--mode platform \
		--input-dir "$(BRIDGE_INPUT_DIR)" \
		--output-dir "$(PLATFORM_OUTPUT_DIR)" \
		--output-pkg "$(PLATFORM_OUTPUT_PKG)" \
		--typed-pkg-import "$(TYPED_PKG_IMPORT)" \
		--typed-client-prefix "V1alpha1Public" \
		--api-pkg-import "$(API_PKG_IMPORT)" \
		--go-header-file "$(SDK_HEADER_FILE)"

verify-clientset: generate-clientset
	git diff --exit-code clientset/

codegen-passthrough: codegen-registry
	cd api && ../bin/passthrough-gen \
		-import-path github.com/openshift/hypershift/api/hypershift/v1beta1 \
		-types HostedClusterSpec,NodePoolSpec \
		-output-dir v1alpha1 \
		-package v1alpha1 \
		-registry ../hack/api-codegen/pkg/registry/field_metadata.json
	rm -f api/v1alpha1/zz_generated.passthrough.go.raw

codegen-passthrough-clobber:
	rm -f api/v1alpha1/zz_generated.passthrough.go
	cd api && ../bin/passthrough-gen \
  		-import-path github.com/openshift/hypershift/api/hypershift/v1beta1 \
  		-types HostedClusterSpec,NodePoolSpec \
  		-output-dir v1alpha1 \
  		-package v1alpha1 \
		-registry ../hack/api-codegen/pkg/registry/field_metadata.json

codegen-registry: generate-deepcopy build-api-codegen
	./bin/marker-scanner \
		-input-dirs api/v1alpha1 \
		-output-file hack/api-codegen/pkg/registry/field_metadata.go \
		$(if $(VERBOSE),-verbose)

codegen-verify: codegen-registry
	cd api && go build ./...
	cd platform-api && go build ./...

codegen: codegen-verify

verify-codegen: codegen
	git diff --exit-code api/v1alpha1/zz_generated.deepcopy.go
	git diff --exit-code hack/api-codegen/pkg/registry/

# generate runs all code generators in dependency order.
# codegen-registry already depends on codegen-passthrough and generate-deepcopy,
# so those are transitively covered; they are listed explicitly here for clarity.
generate: codegen-registry manifests codegen-conversion generate-clientset generate-openapi

verify: verify-codegen verify-conversion verify-clientset verify-openapi verify-mod

CONVERSION_OUTPUT_DIR   ?= platform-api/pkg/conversion/v1alpha1
CONVERSION_OUTPUT_PKG   ?= github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion
CONVERSION_CRD_PKG      ?= github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1
CONVERSION_REST_DIR     ?= api/v1alpha1/public
CONVERSION_REST_PKG     ?= github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public

codegen-conversion: codegen-registry build-api-codegen
	./bin/conversion-gen \
		--api-version=v1alpha1 \
		--crd-package=$(CONVERSION_CRD_PKG) \
		--input-dirs=./api/v1alpha1 \
		--output-dir=$(CONVERSION_OUTPUT_DIR) \
		--output-package=$(CONVERSION_OUTPUT_PKG) \
		--rest-output-dir=$(CONVERSION_REST_DIR) \
		--rest-package=$(CONVERSION_REST_PKG)

verify-conversion: codegen-conversion
	cd api && go build ./...
	cd platform-api && go build ./...
	git diff --exit-code $(CONVERSION_REST_DIR)/ $(CONVERSION_OUTPUT_DIR)/ platform-api/pkg/conversion/types.go

generate-openapi: codegen-conversion
	cd hack/api-codegen && go build -o ../../bin/openapi-gen ./cmd/openapi-gen
	./bin/openapi-gen \
		-input-dirs ./api/v1alpha1 \
		-output-file $(OPENAPI_GENERATED)
	# TODO: auto-discover schemas via +hyperfleet:public-api=true marker so new CRDs
	# are included automatically — requires extending openapi-merge to scan
	# generated-schemas.json description fields for the marker text.
	./bin/openapi-merge \
		-spec $(OPENAPI_SPEC) \
		-generated $(OPENAPI_GENERATED) \
		-schemas Cluster,ClusterList,ClusterSpec,ClusterStatus,NodePool,NodePoolList,NodePoolSpec,NodePoolStatus,OidcConfig,OidcConfigList,OidcConfigStatus,PlacementReference,HostedClusterSpecPassthrough,NodePoolSpecPassthrough,ClusterConfiguration,KubeletConfig,MachineConfigSpec

verify-openapi: generate-openapi
	git diff --exit-code $(OPENAPI_SPEC)

swagger-ui:
	@echo "Swagger UI available at http://localhost:$(SWAGGER_UI_PORT)"
	$(CONTAINER_ENGINE) run --rm -p $(SWAGGER_UI_PORT):8080 \
		-e SWAGGER_JSON=/spec/openapi.yaml \
		-v $(CURDIR)/$(OPENAPI_SPEC):/spec/openapi.yaml:ro \
		swaggerapi/swagger-ui

ENVTEST_BIN_DIR ?= $(shell pwd)/.envtest

setup-envtest: $(SETUP_ENVTEST)
	$(SETUP_ENVTEST) use --bin-dir $(ENVTEST_BIN_DIR)

# ── Images ───────────────────────────────────────────────────────────────

image-api:
	$(CONTAINER_ENGINE) build -f platform-api/Containerfile \
		--platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REPO_API):$(IMAGE_TAG) .
	$(CONTAINER_ENGINE) tag $(IMAGE_REPO_API):$(IMAGE_TAG) $(IMAGE_REPO_API):$(GIT_SHA)

image-operator:
	$(CONTAINER_ENGINE) build -f hyperfleet-operator/Containerfile \
		--platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REPO_OPERATOR):$(IMAGE_TAG) .
	$(CONTAINER_ENGINE) tag $(IMAGE_REPO_OPERATOR):$(IMAGE_TAG) $(IMAGE_REPO_OPERATOR):$(GIT_SHA)

image-push-api: image-api
	$(CONTAINER_ENGINE) push $(IMAGE_REPO_API):$(IMAGE_TAG)
	$(CONTAINER_ENGINE) push $(IMAGE_REPO_API):$(GIT_SHA)

image-push-operator: image-operator
	$(CONTAINER_ENGINE) push $(IMAGE_REPO_OPERATOR):$(IMAGE_TAG)
	$(CONTAINER_ENGINE) push $(IMAGE_REPO_OPERATOR):$(GIT_SHA)

# ── Clean ────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/
	rm -f coverage.out coverage.html
	rm -rf test-results/

clean-test-containers:
	@$(CONTAINER_ENGINE) ps -a --format "{{.Names}}" \
	  | grep -E "^pgctl-" \
	  | xargs -r $(CONTAINER_ENGINE) rm -f \
	  && echo "test containers removed" || true

# ── All ──────────────────────────────────────────────────────────────────

all: deps fmt vet lint test build
