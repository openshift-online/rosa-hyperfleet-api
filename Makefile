.PHONY: help build test test-unit test-integration lint clean \
	build-hyperfleet-db build-operator build-api build-api-codegen \
	test-hyperfleet-db test-operator test-operator-int test-api test-api-codegen \
	coverage-api-codegen \
	test-e2e test-e2e-api test-e2e-cli test-e2e-platform-monitoring test-e2e-zoa test-e2e-authz \
	e2e-authz-infra-up e2e-authz-infra-down e2e-init-db \
	fmt vet verify deps \
	manifests generate setup-envtest \
	codegen-passthrough codegen-registry codegen-verify \
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
SETUP_ENVTEST    := $(abspath $(TOOLS_BIN_DIR)/setup-envtest)
GINKGO           := $(abspath $(TOOLS_BIN_DIR)/ginkgo)

$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(SETUP_ENVTEST): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); go build -tags=tools -o $(abspath $(TOOLS_BIN_DIR))/setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest

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
	@echo "  test-unit            Unit tests: API + operator + codegen (no external services)"
	@echo "  test-integration     Integration tests: FleetDB + operator (podman)"
	@echo "  test-e2e-authz       E2E authz (starts local infra)"
	@echo "  test-e2e-api         E2E API"
	@echo "  test-e2e-cli         E2E CLI"
	@echo "  test-e2e-zoa         E2E ZOA"
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
	@echo "  generate             Generate deepcopy methods"
	@echo "  codegen-passthrough  Run passthrough-gen (raw + curated types)"
	@echo "  codegen-registry     Run marker-scanner (field_metadata.go/.json)"
	@echo "  codegen-verify       Verify codegen packages compile"
	@echo "  setup-envtest        Install envtest binaries (etcd, kube-apiserver)"
	@echo "  deps                 Download and tidy all modules"
	@echo ""
	@echo "Images:"
	@echo "  image-api            Platform API image"
	@echo "  image-operator       Hyperfleet operator image"

# ── Build ────────────────────────────────────────────────────────────────

build: build-hyperfleet-db build-operator build-api build-api-codegen

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

test-unit: test-api test-operator test-api-codegen

test-integration: test-hyperfleet-db test-operator-int

test-api:
	cd platform-api && go test -v -race -count=1 $$(go list ./... | grep -v '/test/e2e')

test-api-codegen:
	cd hack/api-codegen && go test -v -race -count=1 ./...

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

vet:
	cd hyperfleet-db && go vet ./...
	cd hyperfleet-operator && go vet ./...
	cd platform-api && go vet ./...
	cd hack/api-codegen && go vet ./...

lint: $(GOLANGCI_LINT)
	cd hyperfleet-db && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hyperfleet-operator && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd platform-api && $(GOLANGCI_LINT) run --config ../.golangci.yml --timeout 5m ./...
	cd hack/api-codegen && $(GOLANGCI_LINT) run --config ../../.golangci.yml --timeout 5m ./...

verify:
	cd hyperfleet-db && go mod tidy
	cd hyperfleet-operator/api && go mod tidy
	cd hyperfleet-operator && go mod tidy
	cd platform-api && go mod tidy
	cd test && go mod tidy
	cd hack/tools && go mod tidy
	cd hack/api-codegen && go mod tidy
	git diff --exit-code \
		hyperfleet-db/go.mod hyperfleet-db/go.sum \
		hyperfleet-operator/api/go.mod hyperfleet-operator/api/go.sum \
		hyperfleet-operator/go.mod hyperfleet-operator/go.sum \
		platform-api/go.mod platform-api/go.sum \
		test/go.mod test/go.sum \
		hack/tools/go.mod hack/tools/go.sum \
		hack/api-codegen/go.mod hack/api-codegen/go.sum

deps:
	cd hyperfleet-db && go mod download && go mod tidy
	cd hyperfleet-operator/api && go mod download && go mod tidy
	cd hyperfleet-operator && go mod download && go mod tidy
	cd platform-api && go mod download && go mod tidy
	cd test && go mod download && go mod tidy
	cd hack/api-codegen && go mod download && go mod tidy

# ── Code Generation ──────────────────────────────────────────────────────

manifests: $(CONTROLLER_GEN)
	cd hyperfleet-operator && $(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=config/crd/bases

generate: $(CONTROLLER_GEN)
	cd hyperfleet-operator && $(CONTROLLER_GEN) object paths="./api/..."

ENVTEST_BIN_DIR ?= $(shell pwd)/.envtest

setup-envtest: $(SETUP_ENVTEST)
	$(SETUP_ENVTEST) use --bin-dir $(ENVTEST_BIN_DIR)

# ── Codegen Pipeline ────────────────────────────────────────────────────

HYPERSHIFT_IMPORT_PATH ?= github.com/openshift/hypershift/api/hypershift/v1beta1
HYPERSHIFT_TYPES       ?= HostedClusterSpec,NodePoolSpec
V1ALPHA1_DIR           := hyperfleet-operator/api/v1alpha1
REGISTRY_DIR           := platform-api/internal/codegen/registry

codegen-passthrough: build-api-codegen
	./bin/passthrough-gen \
		--source-dir=$$(cd hyperfleet-operator/api && go list -f '{{.Dir}}' $(HYPERSHIFT_IMPORT_PATH)) \
		--types=$(HYPERSHIFT_TYPES) \
		--output-dir=$(V1ALPHA1_DIR) \
		--package=v1alpha1
	rm -f $(V1ALPHA1_DIR)/zz_generated.passthrough.go

codegen-registry: build-api-codegen
	./bin/marker-scanner \
		--input-dirs=$(V1ALPHA1_DIR) \
		--output-file=$(REGISTRY_DIR)/field_metadata.go

codegen-verify: build-api-codegen
	cd hyperfleet-operator/api && go build ./...
	cd platform-api && go build ./internal/codegen/...

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
