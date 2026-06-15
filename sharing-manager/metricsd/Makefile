REGISTRY ?= runai.jfrog.io/op-containers-lab-virt
IMAGE ?= $(REGISTRY)/gpu-sharing-plugin
TAG ?= dev
PLATFORMS ?= linux/amd64,linux/arm64
BIN_DIR ?= bin
CGO_ENABLED ?= 1
DOCKER ?= docker
# GO_TAGS selects optional build tags. Empty = production. "e2e" compiles the
# fake-GPU detection (internal/plugin/adapter_fakegpu_e2e.go) for testing on
# GPU-less clusters. Passed to both `go build` and the docker image build.
GO_TAGS ?=

VERSION ?= $(TAG)
COMMIT ?= $(shell git -C .. rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: fmt test test-e2e build clean docker-build docker-buildx

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

# Runs the unit tests with the fake-GPU detection compiled in (the e2e-tagged
# tests in internal/plugin). Does not require a cluster.
test-e2e:
	go test -tags e2e ./...

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -tags="$(GO_TAGS)" \
		-ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" \
		-o $(BIN_DIR)/metricsd ./cmd

clean:
	rm -rf bin coverage.out

docker-build:
	$(DOCKER) build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		--build-arg GO_TAGS=$(GO_TAGS) \
		-t $(IMAGE):$(TAG) .

docker-buildx:
	$(DOCKER) buildx build \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		--build-arg GO_TAGS=$(GO_TAGS) \
		-t $(IMAGE):$(TAG) .
