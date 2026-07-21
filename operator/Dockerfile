# Build on the native BUILDPLATFORM and cross-compile to the target arch. The
# operator is CGO_ENABLED=0, so cross-compilation is pure Go (no emulation) —
# much faster than running the whole build under qemu for a foreign --platform.
FROM --platform=$BUILDPLATFORM golang:1.26.4 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the API module (lightweight, changes rarely).
COPY api/go.mod api/go.sum api/
COPY api/v1alpha1/ api/v1alpha1/

# Copy pkg module (local replace in operator/go.mod).
COPY pkg/ pkg/

# Copy operator module manifests and download deps first for layer caching.
COPY operator/go.mod operator/go.sum operator/
RUN cd operator && go mod download

# Copy operator source.
COPY operator/cmd/       operator/cmd/
COPY operator/internal/  operator/internal/

RUN cd operator && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o manager cmd/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/operator/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
