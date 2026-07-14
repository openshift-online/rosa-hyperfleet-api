.PHONY: build test test-unit test-authz test-coverage test-e2e test-e2e-api test-e2e-cli test-e2e-platform-monitoring test-e2e-zoa lint clean image image-push run generate generate-swagger help fmt vet

BINARY_NAME := rosa-hyperfleet-api
IMAGE_REPO ?= quay.io/openshift-online/rosa-regional-platform-api
IMAGE_TAG ?= latest
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GOOS ?= linux
GOARCH ?= amd64
PLATFORMS ?= linux/amd64,linux/arm64
# In Prow CI, ARTIFACT_DIR is set automatically; locally default to ./test-results
TEST_OUTPUT_DIR ?= $(or $(ARTIFACT_DIR),./test-results)
DYNAMODB_ENDPOINT ?= http://localhost:8180
CEDAR_AGENT_ENDPOINT ?= http://localhost:8181

# AWS settings - these can be overridden by environment variables or command line
AWS_PROFILE ?=
AWS_REGION ?=
FOCUS ?=
SKIP ?= Authz

# CI container settings - for reproducing Prow CI environment locally
CI_IMAGE_REPO ?= quay.io/openshift-online/rosa-regional-platform-api-ci
CI_IMAGE_TAG ?= latest
REPO_URL ?= https://github.com/openshift-online/rosa-hyperfleet-api
GIT_REF ?= main

# Detect host platform for native builds
HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH := $(shell uname -m)
ifeq ($(HOST_ARCH),x86_64)
	HOST_ARCH := amd64
endif
ifeq ($(HOST_ARCH),aarch64)
	HOST_ARCH := arm64
endif

# Show available make targets
help:
	@echo "Available targets:"
	@echo ""
	@echo "Build & Run:"
	@echo "  build          - Build the binary"
	@echo "  run            - Run locally with debug settings"
	@echo "  clean          - Clean build artifacts"
	@echo ""
	@echo "Testing:"
	@echo "  test                           - Run all unit tests (excludes e2e)"
	@echo "  test-unit                      - Run unit tests for a specific package (PKG=./pkg/authz/...)"
	@echo "  test-authz                     - Run authorization package tests only"
	@echo "  test-coverage                  - Run unit tests with coverage report"
	@echo "  test-e2e                       - Run e2e integration tests (native, excludes CLI tests)"
	@echo "  test-e2e-cli                   - Run e2e CLI tests only (HCP cluster creation)"
	@echo "  test-e2e-awscreds              - Run AWS credentials check test only"
	@echo "  test-e2e-container             - Run e2e tests in container"
	@echo "                                   Supports: AWS_PROFILE=..., FOCUS='pattern', SKIP='pattern'"
	@echo "  test-e2e-authz                 - Run authz e2e tests with local infrastructure"
	@echo ""
	@echo "E2E Infrastructure:"
	@echo "  e2e-authz-infra-up   - Start DynamoDB Local and cedar-agent containers"
	@echo "  e2e-authz-infra-down - Stop E2E infrastructure"
	@echo "  e2e-init-db    - Initialize DynamoDB tables"
	@echo ""
	@echo "Code Quality:"
	@echo "  lint                - Run golangci-lint"
	@echo "  lint-ci-container   - Run linter in CI container with fresh clone"
	@echo "                        Supports: REPO_URL=..., GIT_REF=..."
	@echo "  fmt                 - Format code with gofmt"
	@echo "  vet                 - Run go vet"
	@echo "  verify              - Verify go.mod is tidy"
	@echo ""
	@echo "Docker:"
	@echo "  image                    - Build Docker image"
	@echo "  image-push               - Push Docker image"
	@echo "  image-e2e                - Build E2E test container (single platform)"
	@echo "  image-e2e-multiarch      - Build E2E test container (multiarch)"
	@echo "  image-e2e-push-multiarch - Build and push E2E test container (multiarch)"
	@echo ""
	@echo "Code Generation:"
	@echo "  deps           - Download and tidy dependencies"
	@echo "  generate       - Generate OpenAPI code"
	@echo "  generate-swagger - Regenerate swagger-ui.html"
	@echo ""
	@echo "  all            - Run all checks (deps, fmt, vet, lint, test, build)"

# Build the binary
build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY_NAME) ./cmd/$(BINARY_NAME)

# Run all unit tests (excludes e2e), ci calls test, so disable mod check here
test:
	go test -v -race -count=1 $(shell go list ./... | grep -v '/test/e2e')

# Run unit tests for a specific package (usage: make test-unit PKG=./pkg/authz/...)
PKG ?= ./...
test-unit:
	go test -v -race -count=1 $(PKG)

# Run authorization package tests only
test-authz:
	go test -v -race -count=1 ./pkg/authz/...

# Run tests with coverage (excludes e2e)
test-coverage:
	go test -v -race -coverprofile=coverage.out $(shell go list ./... | grep -v '/test/e2e')
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Kept for backwards compatibility. Not sure if this is used elsewhere, but feel free to delete
# if this isn't necessary
test-e2e: test-e2e-api

# Run e2e tests (native - works on Linux, macOS, Windows)
# Excludes CLI tests (use test-e2e-cli for those) and Authz tests (use test-e2e-authz)
test-e2e-api:
	E2E_BASE_URL="${BASE_URL}" E2E_ACCOUNT_ID="${E2E_ACCOUNT_ID}" \
	E2E_RHOBS_API_URL="${RHOBS_API_URL}" \
	ginkgo -vv \
	--skip="Authz" --junit-report=junit-api.xml \
	--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-api

test-e2e-api-quiet:
	E2E_BASE_URL="${BASE_URL}" E2E_ACCOUNT_ID="${E2E_ACCOUNT_ID}" \
	E2E_RHOBS_API_URL="${RHOBS_API_URL}" \
	ginkgo --skip="Authz" \
	--junit-report=junit-api.xml \
	--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-api

# Run e2e CLI tests only (HCP cluster creation via rosactl)
# Requires: E2E_BASE_URL, ROSACTL_BIN, AWS_REGION, CUSTOMER_AWS_ACCESS_KEY_ID, CUSTOMER_AWS_SECRET_ACCESS_KEY
test-e2e-cli:
	@E2E_BASE_URL="${BASE_URL}" \
		E2E_ACCOUNT_ID="${E2E_ACCOUNT_ID}" \
		E2E_RHOBS_API_URL="${RHOBS_API_URL}" \
		ROSACTL_BIN="${ROSACTL_BIN}" \
		AWS_REGION="${AWS_REGION}" \
		ginkgo -vv --junit-report=junit-cli.xml \
		$(if $(E2E_LABEL_FILTER),--label-filter="$(E2E_LABEL_FILTER)") \
		--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-cli

test-e2e-platform-monitoring:
	E2E_RHOBS_API_URL="${RHOBS_API_URL}" \
	ginkgo -vv --junit-report=junit-platform-monitoring.xml \
	--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-platform-monitoring

test-e2e-zoa:
	E2E_BASE_URL="${BASE_URL}" E2E_ACCOUNT_ID="${E2E_ACCOUNT_ID}" \
	ginkgo -vv --junit-report=junit-zoa.xml \
	--output-dir=$(TEST_OUTPUT_DIR) ./test/e2e-zoa

# Run just the AWS credentials check test
test-e2e-awscreds:
	ginkgo -vv --focus="AWS Credentials Check" ./test/e2e-api

# E2E infrastructure targets
.PHONY: e2e-authz-infra-up e2e-authz-infra-down e2e-init-db test-e2e-authz

# Start DynamoDB Local and cedar-agent containers
e2e-authz-infra-up:
	podman-compose -f hack/podman-compose.e2e-authz.yaml up -d
	@echo "Waiting for services to be ready..."
	@sleep 5
	@$(MAKE) e2e-init-db

# Stop E2E infrastructure
e2e-authz-infra-down:
	podman-compose -f hack/podman-compose.e2e-authz.yaml down -v

# Initialize DynamoDB tables
e2e-init-db:
	./scripts/e2e-init-dynamodb.sh

# Run authz E2E tests (starts infrastructure, runs tests, keeps infra running)
test-e2e-authz: e2e-authz-infra-up
	@./scripts/run-e2e-authz.sh

# Run authz E2E tests with cleanup (stops infrastructure after tests)
test-e2e-authz-clean: test-e2e-authz e2e-authz-infra-down

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Run linter
lint:
	golangci-lint run ./...

# Run linter in CI container with fresh clone (reproduces Prow CI exactly)
lint-ci-container:
	@echo "Building CI container image..."
	docker build -f ci/Containerfile -t $(CI_IMAGE_REPO):$(CI_IMAGE_TAG) .
	@echo "Running linter in CI container with fresh clone from $(REPO_URL)@$(GIT_REF)..."
	docker run --rm \
		$(CI_IMAGE_REPO):$(CI_IMAGE_TAG) \
		bash -c "git clone $(REPO_URL) /tmp/repo && cd /tmp/repo && git checkout $(GIT_REF) && ./ci/lint.sh"

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

# Build Docker image
image:
	docker build --platform $(GOOS)/$(GOARCH) -t $(IMAGE_REPO):$(IMAGE_TAG) .
	docker tag $(IMAGE_REPO):$(IMAGE_TAG) $(IMAGE_REPO):$(GIT_SHA)

# Build E2E test container (single platform)
image-e2e:
	docker build -f Containerfile.e2e \
		--platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REPO)-e2e:$(IMAGE_TAG) .
	docker tag $(IMAGE_REPO)-e2e:$(IMAGE_TAG) $(IMAGE_REPO)-e2e:$(GIT_SHA)

# Build E2E test container for multiple architectures
image-e2e-multiarch:
	docker buildx build -f Containerfile.e2e \
		--platform $(PLATFORMS) \
		-t $(IMAGE_REPO)-e2e:$(IMAGE_TAG) \
		-t $(IMAGE_REPO)-e2e:$(GIT_SHA) \
		--load .

# Build and push E2E test container for multiple architectures
image-e2e-push-multiarch:
	docker buildx build -f Containerfile.e2e \
		--platform $(PLATFORMS) \
		-t $(IMAGE_REPO)-e2e:$(IMAGE_TAG) \
		-t $(IMAGE_REPO)-e2e:$(GIT_SHA) \
		--push .

# Build ginkgo command with focus/skip flags
GINKGO_CMD := ginkgo -vv
ifneq ($(FOCUS),)
	GINKGO_CMD += --focus="$(FOCUS)"
endif
ifneq ($(SKIP),)
	GINKGO_CMD += --skip="$(SKIP)"
endif
GINKGO_CMD += --junit-report=junit.xml --output-dir=/app/test-results ./test/e2e-api

# Since we're using dynamic credentials in our aws config, we need to export the
# credentials to the container
test-e2e-container: image-e2e-multiarch
	@echo "✅ Exporting static credentials from profile $(AWS_PROFILE)..."
	@eval "$$(aws configure export-credentials --profile $(AWS_PROFILE) --format env-no-export)" && \
	docker run --rm \
		-e E2E_BASE_URL="$(BASE_URL)" \
		-e E2E_ACCOUNT_ID="$(E2E_ACCOUNT_ID)" \
		-e AWS_ACCESS_KEY_ID="$$AWS_ACCESS_KEY_ID" \
		-e AWS_SECRET_ACCESS_KEY="$$AWS_SECRET_ACCESS_KEY" \
		-e AWS_SESSION_TOKEN="$$AWS_SESSION_TOKEN" \
		-e AWS_REGION="$(AWS_REGION)" \
		-v $(PWD)/test-results:/app/test-results \
		$(IMAGE_REPO)-e2e:$(IMAGE_TAG) \
		$(GINKGO_CMD)

# Push Docker image
image-push: image
	docker push $(IMAGE_REPO):$(IMAGE_TAG)
	docker push $(IMAGE_REPO):$(GIT_SHA)

# Run locally
run: build
	./$(BINARY_NAME) serve \
		--log-level=debug \
		--log-format=text \
		--maestro-url=http://localhost:8001 \
		--allowed-accounts=123456789012

# Download dependencies
deps:
	go mod download
	go mod tidy

# Generate OpenAPI code (requires oapi-codegen)
generate:
	@echo "OpenAPI code generation not yet configured"
	@echo "Install oapi-codegen: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest"

# Regenerate swagger-ui.html from openapi.yaml (requires yq)
generate-swagger:
	@which yq > /dev/null || (echo "Error: yq is required. Install with: brew install yq" && exit 1)
	@echo "Generating openapi/swagger-ui.html from openapi/openapi.yaml..."
	@( \
		echo '<!DOCTYPE html>'; \
		echo '<html lang="en">'; \
		echo '<head>'; \
		echo '  <meta charset="UTF-8">'; \
		echo '  <title>ROSA Hyperfleet API - Swagger UI</title>'; \
		echo '  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5.10.5/swagger-ui.css">'; \
		echo '  <style>'; \
		echo '    html {'; \
		echo '      box-sizing: border-box;'; \
		echo '      overflow: -moz-scrollbars-vertical;'; \
		echo '      overflow-y: scroll;'; \
		echo '    }'; \
		echo '    *, *:before, *:after {'; \
		echo '      box-sizing: inherit;'; \
		echo '    }'; \
		echo '    body {'; \
		echo '      margin: 0;'; \
		echo '      padding: 0;'; \
		echo '    }'; \
		echo '  </style>'; \
		echo '</head>'; \
		echo '<body>'; \
		echo '  <div id="swagger-ui"></div>'; \
		echo '  <script src="https://unpkg.com/swagger-ui-dist@5.10.5/swagger-ui-bundle.js"></script>'; \
		echo '  <script src="https://unpkg.com/swagger-ui-dist@5.10.5/swagger-ui-standalone-preset.js"></script>'; \
		echo '  <script>'; \
		echo '    window.onload = function() {'; \
		echo '      const ui = SwaggerUIBundle({'; \
		echo "        url: window.location.origin + '/openapi.yaml',"; \
		echo '        spec: '; \
		yq eval -o=json -I=2 '.' openapi/openapi.yaml | sed 's/^/  /'; \
		echo ','; \
		echo "        dom_id: '#swagger-ui',"; \
		echo '        deepLinking: true,'; \
		echo '        presets: ['; \
		echo '          SwaggerUIBundle.presets.apis,'; \
		echo '          SwaggerUIStandalonePreset'; \
		echo '        ],'; \
		echo '        plugins: ['; \
		echo '          SwaggerUIBundle.plugins.DownloadUrl'; \
		echo '        ],'; \
		echo '        layout: "StandaloneLayout"'; \
		echo '      });'; \
		echo '      window.ui = ui;'; \
		echo '    };'; \
		echo '  </script>'; \
		echo '</body>'; \
		echo '</html>'; \
	) > docs/index.html
	@echo "Done! Generated docs/index.html"

# Verify go.mod is tidy
verify:
	go mod tidy
	git diff --exit-code go.mod go.sum

# All checks
all: deps fmt vet lint test build
