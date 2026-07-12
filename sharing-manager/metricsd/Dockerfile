# syntax=docker/dockerfile:1.7
#
# Build context MUST be the repository root (not sharing-manager/metricsd): the
# metricsd module replaces github.com/run-ai/gpu-sharing-operator => ../../ to use
# the shared sharingd/mapping packages, so the root module source must be present
# in the build. Build with:  docker build -f sharing-manager/metricsd/Dockerfile .

# Build on the TARGET platform, not the build platform: metricsd links NVML via
# cgo, and cross-compiling cgo needs a target cross-toolchain (the native gcc
# rejects the cross flags, e.g. "-m64" when building amd64 on an arm64 host).
# Running the build stage as $TARGETPLATFORM uses a native toolchain (emulated by
# buildx/qemu when host≠target), so cgo compiles cleanly. Select the target with
# `docker buildx build --platform linux/amd64`.
FROM --platform=$TARGETPLATFORM golang:1.26-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
# GO_TAGS selects optional build tags. Empty (the default) = production. Set to
# "e2e" to compile the fake-GPU detection used for testing on GPU-less clusters.
# Never ship an e2e-tagged image to prod.
ARG GO_TAGS=""

WORKDIR /workspace

# Root module go.mod/go.sum, then the whole sharing-manager tree (both the
# metricsd module and the shared sharingd packages it references via replace).
COPY go.mod go.sum ./
COPY sharing-manager/ sharing-manager/

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd sharing-manager/metricsd && \
    CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -tags="${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/metricsd ./cmd

# distroless/base-debian12 (glibc) because the NVML bindings load libnvidia-ml.so
# at runtime; the driver is injected by the NVIDIA container runtime.
FROM gcr.io/distroless/base-debian12

USER 0:0
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
COPY --from=build /out/metricsd /usr/local/bin/metricsd

ENTRYPOINT ["/usr/local/bin/metricsd"]
