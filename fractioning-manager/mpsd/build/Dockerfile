# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder
ARG TARGETARCH

WORKDIR /workspace

# Cache dependencies
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN go mod download

# Copy source
COPY fractioning-manager/common/ fractioning-manager/common/
COPY fractioning-manager/mpsd/ fractioning-manager/mpsd/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /mpsd ./fractioning-manager/mpsd/cmd

# Runtime stage — nvidia/cuda base provides nvidia-cuda-mps-control which mpsd
# needs to exec. The NVIDIA container runtime only injects device access, not
# the MPS control binary itself.
FROM nvidia/cuda:13.3.1-base-ubuntu24.04

COPY --from=builder /mpsd /usr/local/bin/mpsd

ENTRYPOINT ["/usr/local/bin/mpsd"]
