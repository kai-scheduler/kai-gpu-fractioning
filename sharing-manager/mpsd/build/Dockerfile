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

# Runtime stage — use base-debian12 (not nonroot) because MPS daemon needs
# root to create config directories and manage GPU processes.
FROM gcr.io/distroless/base-debian12

COPY --from=builder /mpsd /usr/local/bin/mpsd

ENTRYPOINT ["/usr/local/bin/mpsd"]
