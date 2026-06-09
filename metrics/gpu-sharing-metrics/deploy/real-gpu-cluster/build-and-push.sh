#!/usr/bin/env bash
# build-and-push.sh — build the real-gpu-cluster gpu-sharing-plugin image and push it.
#
# Unlike deploy/build-and-push.sh (the e2e/fake-GPU image), this builds WITHOUT
# any Go build tags. The real-gpu-cluster image detects GPU allocation from real
# /dev/nvidia* device nodes rather than MOCK_NVIDIA_VISIBLE_DEVICES.
#
#   ./deploy/real-gpu-cluster/build-and-push.sh
#
# Override variables inline:
#   TAG=v0.1.0 ./deploy/real-gpu-cluster/build-and-push.sh

set -euo pipefail

REGISTRY="${REGISTRY:-runai.jfrog.io/op-containers-lab-virt}"
IMAGE="${IMAGE:-${REGISTRY}/gpu-sharing-plugin}"
TAG="${TAG:-latest}"
PLATFORM="${PLATFORM:-linux/amd64}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

VERSION="${VERSION:-${TAG}}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

echo ">> module root : ${ROOT_DIR}"
echo ">> image       : ${IMAGE}:${TAG}"
echo ">> platform    : ${PLATFORM}"
echo ">> commit/date : ${COMMIT} / ${DATE}"

go test ./...

docker login runai.jfrog.io

docker run --privileged --rm tonistiigi/binfmt --install amd64 >/dev/null 2>&1 || true
docker buildx inspect gpu-sharing-builder >/dev/null 2>&1 \
  || docker buildx create --name gpu-sharing-builder --use
docker buildx use gpu-sharing-builder
docker buildx inspect --bootstrap >/dev/null

docker buildx build \
  --platform "${PLATFORM}" \
  --build-arg BUILDPLATFORM="${PLATFORM}" \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg DATE="${DATE}" \
  -t "${IMAGE}:${TAG}" \
  --push \
  .

echo
echo ">> pushed ${IMAGE}:${TAG}"
echo ">> next: kubectl apply -f deploy/production/daemonset.yaml"
