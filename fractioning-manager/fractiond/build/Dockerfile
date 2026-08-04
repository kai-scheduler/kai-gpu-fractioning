# Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Build on the native BUILDPLATFORM and cross-compile: fractiond is CGO_ENABLED=0,
# so this is pure-Go cross-compilation (no qemu emulation) to the target arch.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN go mod download
COPY fractioning-manager/ fractioning-manager/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o fractiond ./fractioning-manager/fractiond/cmd

# NVIDIA-approved distroless base pulled from NGC (OSRB requires an approved
# NVIDIA base container; gcr.io/distroless is not one). fractiond is a
# statically-linked Go binary with no GPU or OS-level dependencies — it only
# communicates via the NRI Unix socket — so nothing from the base is needed
# beyond a filesystem to run in, and no OS packages are added.
FROM nvcr.io/nvidia/distroless/go:v4.0.8
COPY --from=builder /workspace/fractiond /fractiond
# Attribution for the third-party code statically linked into the binary.
COPY THIRD-PARTY.txt /THIRD-PARTY.txt
# Root, because the NRI socket fractiond connects to is root-owned. The base
# image defaults to its non-root `nvs` user, so this has to be explicit.
USER 0:0
ENTRYPOINT ["/fractiond"]
