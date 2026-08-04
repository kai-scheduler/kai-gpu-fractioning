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

# distroless/static is sufficient because fractiond is a statically-linked Go
# binary with no GPU or OS-level dependencies — it only communicates via the
# NRI Unix socket. Runs as root because the NRI socket is root-owned.
FROM gcr.io/distroless/static
COPY --from=builder /workspace/fractiond /fractiond
ENTRYPOINT ["/fractiond"]
