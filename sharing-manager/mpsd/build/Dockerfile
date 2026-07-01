# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26.2-alpine AS builder
ARG TARGETARCH

WORKDIR /workspace

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY sharing-manager/common/ sharing-manager/common/
COPY sharing-manager/mpsd/ sharing-manager/mpsd/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /mpsd ./sharing-manager/mpsd/cmd

# Runtime stage — nvidia/cuda base provides nvidia-cuda-mps-control which mpsd
# needs to exec. The NVIDIA container runtime only injects device access, not
# the MPS control binary itself.
FROM nvidia/cuda:12.8.1-base-ubuntu24.04

COPY --from=builder /mpsd /usr/local/bin/mpsd

ENTRYPOINT ["/usr/local/bin/mpsd"]
