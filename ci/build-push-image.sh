#!/usr/bin/env bash
# Build and push the rosa-hyperfleet-api container image.
#
# Called by CI (Prow post-submit, GitHub Actions, or manually) after merging
# to main to keep the published image in sync with the source code.
#
# Required env vars:
#   QUAY_USERNAME  - quay.io robot account username
#   QUAY_PASSWORD  - quay.io robot account password / token
#
# Optional env vars:
#   IMAGE_REPO  - destination image (default: quay.io/cdoan0/rosa-regional-platform-api)
#   GIT_SHA     - commit SHA to tag the image with (default: git rev-parse HEAD)

set -euo pipefail

IMAGE_REPO="${IMAGE_REPO:-quay.io/cdoan0/rosa-regional-platform-api}"
GIT_SHA="${GIT_SHA:-$(git rev-parse HEAD)}"
SHORT_SHA="${GIT_SHA:0:7}"

echo "Building image: ${IMAGE_REPO}:${SHORT_SHA}"

# Detect container runtime
if command -v docker &>/dev/null; then
  RUNTIME=docker
elif command -v podman &>/dev/null; then
  RUNTIME=podman
else
  echo "ERROR: neither docker nor podman found" >&2
  exit 1
fi

# Log in to quay.io
echo "${QUAY_PASSWORD}" | ${RUNTIME} login quay.io -u "${QUAY_USERNAME}" --password-stdin

# Build
${RUNTIME} build --platform linux/amd64 \
  -t "${IMAGE_REPO}:latest" \
  -t "${IMAGE_REPO}:${SHORT_SHA}" \
  .

# Push
${RUNTIME} push "${IMAGE_REPO}:latest"
${RUNTIME} push "${IMAGE_REPO}:${SHORT_SHA}"

echo "Done: pushed ${IMAGE_REPO}:latest and ${IMAGE_REPO}:${SHORT_SHA}"
