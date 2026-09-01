#!/bin/sh
# Build the triple-binary E2E image and run all suites. The container builds
# homonto, onto, and to and runs the suites (see test/docker/run-all.sh) against
# a disposable $HOME, so the host is never touched. Exits non-zero if the build
# or any suite fails.
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${HOMONTO_DOCKER_E2E_IMAGE:-homonto-docker-e2e}"

cd "$ROOT"
docker build -f test/docker/Dockerfile -t "$IMAGE" .
docker run --rm "$IMAGE"
