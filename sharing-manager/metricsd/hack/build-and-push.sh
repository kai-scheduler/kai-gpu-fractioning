#!/usr/bin/env bash
#
# build-and-push.sh — build the gpu-sharing-plugin image from the LOCAL source
# tree and push it to the JFrog registry as the :e2e tag (the fake-GPU test image).
#
# Why this script exists: the :dev image currently in the registry was built
# from a commit that is NOT in this repo and ignores `metrics.enabled: false`
# (it inits NVML unconditionally and crash-loops on non-GPU nodes). This builds
# a fresh image from your current HEAD, where metrics are correctly gated.
#
# Run it from anywhere; it cd's to the module root (the parent of hack/).
#
#   ./hack/build-and-push.sh
#
# Override any variable inline, e.g.:  TAG=mytest ./deploy/build-and-push.sh
#
# DO NOT pipe this to sh blindly — read it first; it pushes to a shared registry.

set -euo pipefail

# ---- Configuration ---------------------------------------------------------
REGISTRY="${REGISTRY:-runai.jfrog.io/op-containers-lab-virt}"
IMAGE="${IMAGE:-${REGISTRY}/gpu-sharing-plugin}"
# This script builds the FAKE-GPU TEST image: it is compiled with -tags e2e so
# the plugin detects the fake-gpu-operator's MOCK_NVIDIA_VISIBLE_DEVICES env var
# (real-gpu-cluster builds omit GO_TAGS and never do this). The image is tagged :e2e so
# it can't be confused with a real-gpu-cluster image. Production images are built via
# `make docker-buildx` with no GO_TAGS.
TAG="${TAG:-e2e}"
GO_TAGS="${GO_TAGS:-e2e}"
PLATFORM="${PLATFORM:-linux/amd64}"         # cluster nodes are amd64 (kind-on-AWS)

# Resolve the module root (this script lives in <root>/hack/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# Build metadata baked into the binary via -ldflags (see Dockerfile / Makefile).
VERSION="${VERSION:-${TAG}}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

echo ">> module root : ${ROOT_DIR}"
echo ">> image       : ${IMAGE}:${TAG}"
echo ">> go tags     : ${GO_TAGS:-<none>}"
echo ">> platform    : ${PLATFORM}"
echo ">> commit/date : ${COMMIT} / ${DATE}"

# ---- 0. (optional) sanity-test the code before shipping --------------------
# Comment out to skip. Catches a broken build before a slow image push. Tested
# with the same build tags the image uses, so the e2e fake-GPU tests run too.
go test -tags "${GO_TAGS}" ./...

# ---- 1. Authenticate to the registry ---------------------------------------
# Interactive: prompts for the JFrog username + identity token / API key.
# Skip if you are already logged in (`docker login` state is cached).
REGISTRY_HOST="${REGISTRY%%/*}"
docker login "${REGISTRY_HOST}"

# ---- 2. Ensure a buildx builder + amd64 emulation --------------------------
# This Mac is arm64 (Apple Silicon); the cluster is amd64. The Dockerfile uses
# CGO_ENABLED=1 (the NVML bindings need cgo), and cross-compiling cgo from
# arm64 -> amd64 needs a cross C toolchain that the golang image does not ship.
#
# To avoid that, we build the amd64 image UNDER EMULATION: we override the
# built-in BUILDPLATFORM arg to linux/amd64 so the golang build stage itself
# runs as an emulated amd64 container and GOARCH=amd64 becomes a *native*
# compile (cgo works). QEMU/binfmt provides the emulation.
docker run --privileged --rm tonistiigi/binfmt --install amd64 >/dev/null 2>&1 || true
docker buildx inspect gpu-sharing-builder >/dev/null 2>&1 \
  || docker buildx create --name gpu-sharing-builder --use
docker buildx use gpu-sharing-builder
docker buildx inspect --bootstrap >/dev/null

# ---- 3. Build for linux/amd64 and push -------------------------------------
# --push uploads directly to the registry (multi-arch/emulated images cannot be
# loaded into the local docker daemon, so we push instead of --load).
docker buildx build \
  --platform "${PLATFORM}" \
  --build-arg BUILDPLATFORM="${PLATFORM}" \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg DATE="${DATE}" \
  --build-arg GO_TAGS="${GO_TAGS}" \
  -t "${IMAGE}:${TAG}" \
  --push \
  .

echo
echo ">> pushed ${IMAGE}:${TAG}"
echo ">> next: ./deploy/test-nri.sh"

# ---- Fallback if step 3 fails with a cgo/cross-compiler error ---------------
# If overriding BUILDPLATFORM is not honored by your buildkit version, edit the
# Dockerfile line 3 from:
#     FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build
# to:
#     FROM --platform=linux/amd64 golang:1.24-bookworm AS build
# then re-run this script (keep the binfmt/QEMU step above).
