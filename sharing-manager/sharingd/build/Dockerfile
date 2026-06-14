FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY sharing-manager/ sharing-manager/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o sharingd ./sharing-manager/sharingd/cmd

# distroless/static is sufficient because sharingd is a statically-linked Go
# binary with no GPU or OS-level dependencies — it only communicates via the
# NRI Unix socket. Runs as root because the NRI socket is root-owned.
FROM gcr.io/distroless/static
COPY --from=builder /workspace/sharingd /sharingd
ENTRYPOINT ["/sharingd"]
