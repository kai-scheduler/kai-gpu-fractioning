# Fake mpsd image for fake-GPU E2E clusters.
#
# It contains the REAL mpsd supervisor (same code as the production image) plus a
# FAKE nvidia-cuda-mps-control at the default path (/usr/bin/nvidia-cuda-mps-control)
# that just creates the control socket and idles. This exercises the real
# supervisor + readiness + controller status/node-condition flow without a GPU,
# and needs NO controller/CRD change (the supervisor uses the default binary path).
#
# Build from the REPO ROOT (context must include go.mod, pkg/, sharing-manager/, hack/):
#   docker build -f hack/fake-mps/Dockerfile -t <name>:<tag> .
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS builder
ARG TARGETARCH
WORKDIR /workspace

# Real mpsd supervisor (identical build to sharing-manager/mpsd/build/Dockerfile).
# pkg/ is copied before `go mod download` because the root go.mod has
# `replace github.com/kai-scheduler/gpu-sharing/pkg => ./pkg`.
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN go mod download
COPY sharing-manager/common/ sharing-manager/common/
COPY sharing-manager/mpsd/ sharing-manager/mpsd/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /mpsd ./sharing-manager/mpsd/cmd

# Fake nvidia-cuda-mps-control — standalone std-lib-only module (hack/fake-mps).
COPY hack/fake-mps/ /fake-mps-src/
RUN cd /fake-mps-src && \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH GOTOOLCHAIN=local go build -o /fake-mps .

# Runtime: alpine's busybox provides `test` (supports -S) for the readiness probe.
FROM alpine:3.20
COPY --from=builder /mpsd /usr/local/bin/mpsd
COPY --from=builder /fake-mps /usr/bin/nvidia-cuda-mps-control
ENTRYPOINT ["/usr/local/bin/mpsd"]
