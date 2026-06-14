# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
# GO_TAGS selects optional build tags. Empty (the default) = production. Set to
# "e2e" to compile the fake-GPU detection used for testing on GPU-less clusters
# (the internal/plugin/fakegpu detector). Never ship an e2e-tagged image to prod.
ARG GO_TAGS=""

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -tags="${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/metricsd ./cmd

FROM gcr.io/distroless/base-debian12

USER 0:0
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
COPY --from=build /out/metricsd /usr/local/bin/metricsd

ENTRYPOINT ["/usr/local/bin/metricsd"]
