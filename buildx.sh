#!/bin/bash
set -euo pipefail

REGISTRY="ghcr.io/never-stop-exploiting"
VERSION="${VERSION:-dev}"
PLATFORM="${PLATFORM:-linux/amd64}"

images=(
  "control-panel=docker/control-panel.Dockerfile"
  "scheduler=docker/scheduler.Dockerfile"
  "dispatcher=docker/dispatcher.Dockerfile"
  "web=docker/web.Dockerfile"
  "runner=docker/runner.Dockerfile"
  "submitter=docker/flag-submitter.Dockerfile"
)

for image in "${images[@]}"; do
  name="${image%%=*}"
  file="${image#*=}"
  echo "Building and pushing ${REGISTRY}/${name}:${VERSION} (${PLATFORM})..."
  docker buildx build \
    --platform "${PLATFORM}" \
    -t "${REGISTRY}/${name}:${VERSION}" \
    -f "${file}" \
    . \
    --push
done
