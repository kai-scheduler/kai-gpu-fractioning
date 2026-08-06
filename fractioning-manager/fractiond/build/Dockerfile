# syntax=docker/dockerfile:1.7
# Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Build fractiond on the native BUILDPLATFORM and cross-compile it: fractiond is
# CGO_ENABLED=0, so this stays pure-Go and does not need qemu emulation.
FROM --platform=$BUILDPLATFORM golang:1.26 AS fractiond-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY fractioning-manager/ fractioning-manager/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -trimpath -ldflags="-s -w" -o /out/fractiond ./fractioning-manager/fractiond/cmd

# Build the node-driver labeler on the TARGET platform: it links go-nvml via cgo,
# and cross-compiling cgo needs a target cross-toolchain. Running this stage as
# $TARGETPLATFORM uses the native compiler for that platform (emulated by
# buildx/qemu when host != target).
FROM --platform=$TARGETPLATFORM golang:1.26-bookworm AS driver-labeler-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY fractioning-manager/fractiond/ fractioning-manager/fractiond/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -trimpath -ldflags="-s -w" -o /out/driver-labeler ./fractioning-manager/fractiond/cmd/driver-labeler

# NVIDIA-approved distroless base pulled from NGC (OSRB requires an approved
# NVIDIA base container; gcr.io/distroless is not one). The driver-labeler is
# CGO_ENABLED=1 and the NVML bindings dlopen libnvidia-ml.so at runtime; the
# driver library itself is injected by the NVIDIA container runtime. No OS
# packages are added on top of the base.
FROM nvcr.io/nvidia/distroless/go:v4.0.8 AS runtime-base
COPY --from=fractiond-builder /out/fractiond /fractiond
COPY --from=driver-labeler-builder /out/driver-labeler /driver-labeler
# Attribution for the third-party code statically linked into the binary.
COPY THIRD-PARTY.txt /THIRD-PARTY.txt
# Root, because the NRI socket fractiond connects to is root-owned. The base
# image defaults to its non-root `nvs` user, so this has to be explicit.
USER 0:0
ENTRYPOINT ["/fractiond"]

# E2E target: bake in NVIDIA's mock NVML library so the driver-labeler init
# container can run on fake-GPU k3d nodes without a real NVIDIA driver.
FROM ghcr.io/nvidia/nvml-mock:latest AS nvml-mock
RUN cp /usr/local/lib/libnvidia-ml.so.1 /libnvidia-ml.so.1 && \
    chmod 755 /libnvidia-ml.so.1 && \
    test -x /libnvidia-ml.so.1

FROM runtime-base AS e2e
ENV LD_LIBRARY_PATH=/usr/lib/nvml-mock
ENV MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml
COPY --from=nvml-mock /libnvidia-ml.so.1 /usr/lib/nvml-mock/libnvidia-ml.so.1

FROM runtime-base AS production
