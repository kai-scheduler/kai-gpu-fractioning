# Build on the native BUILDPLATFORM and cross-compile: sharingd is CGO_ENABLED=0,
# so this is pure-Go cross-compilation (no qemu emulation) to the target arch.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY sharing-manager/ sharing-manager/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -ldflags="-s -w" -o sharingd ./sharing-manager/sharingd/cmd

# distroless/static is sufficient because sharingd is a statically-linked Go
# binary with no GPU or OS-level dependencies — it only communicates via the
# NRI Unix socket. Runs as root because the NRI socket is root-owned.
FROM gcr.io/distroless/static
COPY --from=builder /workspace/sharingd /sharingd
ENTRYPOINT ["/sharingd"]
