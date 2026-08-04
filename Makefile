.PHONY: help build test test-unit test-integration lint clean \
	build-hyperfleet-db build-operator build-api build-api-codegen \
	test-hyperfleet-db test-operator test-operator-int test-api test-api-codegen test-clientset \
	coverage-api-codegen \
	test-e2e test-e2e-api test-e2e-cli test-e2e-platform-monitoring test-e2e-zoa test-e2e-authz test-e2e-sdk \
	e2e-authz-infra-up e2e-authz-infra-down e2e-init-db \
	fmt vet verify deps \
	manifests generate generate-clientset verify-clientset \
	generate-public-deepcopy setup-envtest \
	codegen-passthrough codegen-registry codegen-verify codegen verify-codegen \
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

CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

TOOLS_DIR        := ./hack/tools
TOOLS_BIN_DIR    := $(TOOLS_DIR)/bin
GOLANGCI_LINT    := $(abspath $(TOOLS_BIN_DIR)/golangci-lint)
CONTROLLER_GEN   := $(abspath $(TOOLS_BIN_DIR)/controller-gen)
CLIENT_GEN       := $(abspath $(TOOLS_BIN_DIR)/client-gen)
WIRE_GEN         := $(abspath $(TOOLS_BIN_DIR)/wire-gen)
SETUP_ENVTEST    := $(abspath $(TOOLS_BIN_DIR)/setup-envtest)
GINKGO           := $(abspath $(TOOLS_BIN_DIR)/ginkgo)

# ── SDK generation ───────────────────────────────────────────────────────
SDK_MODULE        ?= github.com/openshift-online/rosa-hyperfleet-api
SDK_API_PKG       ?= $(SDK_MODULE)/hyperfleet-operator/api
SDK_INPUT         ?= v1alpha1
SDK_CLIENTSET     ?= generated
SDK_OUTPUT_DIR    ?= $(abspath clientset)
SDK_OUTPUT_PKG    ?= $(SDK_MODULE)/clientset
WIRE_INPUT_DIR        ?= $(abspath hyperfleet-operator/api/v1alpha1)
WIRE_OUTPUT_DIR       ?= $(abspath clientset/transport)
WIRE_OUTPUT_PKG       ?= transport
WRAPPERS_OUTPUT_DIR   ?= $(abspath clientset/wrappers)
WRAPPERS_OUTPUT_PKG   ?= wrappers
TYPED_PKG_IMPORT      ?= $(SDK_MODULE)/clientset/generated/typed/v1alpha1/internalversion
API_PKG_IMPORT        ?= $(SDK_MODULE)/hyperfleet-operator/api/v1alpha1
SDK_HEADER_FILE       ?= $(abspath hack/clientset/license-boilerplate.go.txt)

$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(SETUP_ENVTEST): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest

$(CLIENT_GEN): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/client-gen k8s.io/code-generator/cmd/client-gen

$(WIRE_GEN): hack/clientset/cmd/wire-gen/main.go
	cd hack/clientset/cmd/wire-gen && go build -o $(WIRE_GEN) .

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
	@echo "  test-clientset       Clientset unit tests (transport, wrappers)"
	@echo "  test-integration     Integration tests: FleetDB + operator (podman)"
	@echo "  test-e2e-authz       E2E authz (starts local infra)"
	@echo "  test-e2e-api         E2E API"
	@echo "  test-e2e-cli         E2E CLI"
	@echo "  test-e2e-zoa         E2E ZOA"
	@echo "  test-e2e-sdk         E2E SDK (Go clientset lifecycle)"
	@echo "  test-e2e-platform-monitoring  E2E monitoring"
	@echo ""
	@echo "  coverage-api-codegen Coverage report for codegen (hack/api-codegen)"
	@echo ""
	@echo "Code Quality:"
	@echo "  lint                 golangci-lint on all modules"
	@echo "  fmt                  Format Go source"
	@echo "  vet                  go vet on all modules"
	@echo "  verify               Verify go.mod tidiness"
	@echo ""
	@echo "Code Generation:"
	@echo "  manifests            Generate CRD manifests"
	@echo "  generate             Generate deepcopy methods (v1alpha1)"
	@echo "  generate-clientset   Generate typed client SDK from CRD types"
	@echo "  verify-clientset     Fail if generated clientset is out of date"
	@echo "  generate-public-deepcopy  Generate deepcopy methods (v2alpha1 public API)"
	@echo "  codegen              Full codegen pipeline (passthrough → deepcopy → registry)"
	@echo "  codegen-passthrough  Generate passthrough types from HyperShift"
	@echo "  codegen-registry     Generate field metadata registry from markers"
	@echo "  codegen-verify       Verify codegen outputs compile"
	@echo "  verify-codegen       Fail if codegen outputs are out of date"
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
	cd hack/api-codegen && go build -o ../../bin/conversion-gen ./cmd/conversion-gen
	cd hack/api-codegen && go build -o ../../bin/crd-variants ./cmd/crd-variants
	cd hack/api-codegen && go build -o ../../bin/featuregate-info ./cmd/featuregate-info
	cd hack/api-codegen && go build -o ../../bin/verify-configuration ./cmd/verify-configuration

# ── Test ─────────────────────────────────────────────────────────────────

test: test-unit test-integration

test-unit: test-api test-operator test-api-codegen test-clientset

test-integration: test-hyperfleet-db test-operator-int

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
	ROSACTL_BIN="$${ROSACTL_BIN}" AWS_REGION="$${AWS_REGION}" \
	$(GINKGO) -vv --junit-report=junit-cli.xml \
		$(if $(E2E_LABEL_FILTER),--label-filter="$(E2E_LABEL_FILTER)") \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-cli

test-e2e-platform-monitoring: $(GINKGO)
	E2E_RHOBS_API_URL="$${RHOBS_API_URL}" \
	$(GINKGO) -vv --junit-report=junit-platform-monitoring.xml \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-platform-monitoring

test-e2e-zoa: $(GINKGO)
	E2E_BASE_URL="$${BASE_URL}" E2E_ACCOUNT_ID="$${E2E_ACCOUNT_ID}" \
	$(GINKGO) -vv --junit-report=junit-zoa.xml \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-zoa

test-e2e-sdk: $(GINKGO)
	BASE_URL="$${BASE_URL}" \
	E2E_ACCOUNT_ID="$${E2E_ACCOUNT_ID}" \
	E2E_CUSTOMER_ACCOUNT_ID="$${E2E_CUSTOMER_ACCOUNT_ID}" \
	CUSTOMER_AWS_PROFILE="$${CUSTOMER_AWS_PROFILE}" \
	AWS_REGION="$${AWS_REGION}" \
	ROSACTL_BIN="$${ROSACTL_BIN}" \
	HYPERFLEET_VERSION="$${HYPERFLEET_VERSION}" \
	$(GINKGO) -vv --timeout=3h --junit-report=junit-sdk.xml \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-sdk

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
	cd hack/clientset/cmd/wire-gen && go fmt ./...

vet:
	cd hyperfleet-db && go vet ./...
	cd hyperfleet-operator && go vet ./...
	cd platform-api && go vet ./...
	cd hack/api-codegen && go vet ./...
	cd clientset && go vet ./...
	cd hack/clientset/cmd/wire-gen && go vet ./...

lint: $(GOLANGCI_LINT)
	cd hyperfleet-db && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hyperfleet-operator && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd platform-api && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hack/api-codegen && $(GOLANGCI_LINT) run --config ../../.golangci.yml --timeout 5m ./...
	cd clientset && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hack/clientset/cmd/wire-gen && $(GOLANGCI_LINT) run --config $(abspath .golangci.yml) --timeout 5m ./...

verify:
	cd hyperfleet-db && go mod tidy
	cd hyperfleet-operator/api && go mod tidy
	cd hyperfleet-operator && go mod tidy
	cd api/public/v2alpha1 && go mod tidy
	cd platform-api && go mod tidy
	cd test && go mod tidy
	cd hack/tools && go mod tidy
	cd hack/api-codegen && go mod tidy
	git diff --exit-code \
		hyperfleet-db/go.mod hyperfleet-db/go.sum \
		hyperfleet-operator/api/go.mod hyperfleet-operator/api/go.sum \
		hyperfleet-operator/go.mod hyperfleet-operator/go.sum \
		api/public/v2alpha1/go.mod api/public/v2alpha1/go.sum \
		platform-api/go.mod platform-api/go.sum \
		test/go.mod test/go.sum \
		hack/tools/go.mod hack/tools/go.sum \
		hack/api-codegen/go.mod hack/api-codegen/go.sum

deps:
	cd hyperfleet-db && go mod download && go mod tidy
	cd hyperfleet-operator/api && go mod download && go mod tidy
	cd hyperfleet-operator && go mod download && go mod tidy
	cd api/public/v2alpha1 && go mod download && go mod tidy
	cd platform-api && go mod download && go mod tidy
	cd test && go mod download && go mod tidy
	cd hack/api-codegen && go mod download && go mod tidy

# ── Code Generation ──────────────────────────────────────────────────────

manifests: $(CONTROLLER_GEN)
	cd hyperfleet-operator && $(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=config/crd/bases

generate: $(CONTROLLER_GEN)
	cd hyperfleet-operator && $(CONTROLLER_GEN) object paths="./api/..."

generate-clientset: $(CLIENT_GEN) $(WIRE_GEN)
	cd hyperfleet-operator/api && $(CLIENT_GEN) \
		--input-base "$(SDK_API_PKG)" \
		--input "$(SDK_INPUT)" \
		--clientset-name "$(SDK_CLIENTSET)" \
		--output-dir "$(SDK_OUTPUT_DIR)" \
		--output-pkg "$(SDK_OUTPUT_PKG)" \
		--go-header-file "$(SDK_HEADER_FILE)"
	$(WIRE_GEN) \
		--mode mappings \
		--input-dir "$(WIRE_INPUT_DIR)" \
		--output-dir "$(WIRE_OUTPUT_DIR)" \
		--output-pkg "$(WIRE_OUTPUT_PKG)" \
		--go-header-file "$(SDK_HEADER_FILE)"
	$(WIRE_GEN) \
		--mode wrappers \
		--input-dir "$(WIRE_INPUT_DIR)" \
		--output-dir "$(WRAPPERS_OUTPUT_DIR)" \
		--output-pkg "$(WRAPPERS_OUTPUT_PKG)" \
		--typed-pkg-import "$(TYPED_PKG_IMPORT)" \
		--api-pkg-import "$(API_PKG_IMPORT)" \
		--go-header-file "$(SDK_HEADER_FILE)"

verify-clientset: generate-clientset
	git diff --exit-code clientset/

codegen-passthrough: build-api-codegen
	cd api/public/v2alpha1 && ../../../bin/passthrough-gen \
		-import-path github.com/openshift/hypershift/api/hypershift/v1beta1 \
		-types HostedClusterSpec,NodePoolSpec \
		-output-dir . \
		-package v2alpha1
	mv api/public/v2alpha1/zz_generated.passthrough.go api/public/v2alpha1/zz_generated.passthrough.go.raw

generate-public-deepcopy: codegen-passthrough $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths="./api/public/v2alpha1/..."

codegen-registry: generate-public-deepcopy build-api-codegen
	./bin/marker-scanner \
		-input-dirs api/public/v2alpha1 \
		-output-file platform-api/internal/codegen/registry/field_metadata.go \
		$(if $(VERBOSE),-verbose)

codegen-verify: codegen-registry
	cd api/public/v2alpha1 && go build ./...
	cd platform-api && go build ./internal/codegen/...

codegen: codegen-verify

verify-codegen: codegen
	git diff --exit-code api/public/v2alpha1/zz_generated.deepcopy.go
	git diff --exit-code platform-api/internal/codegen/registry/

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

# ── All ──────────────────────────────────────────────────────────────────

all: deps fmt vet lint test build
