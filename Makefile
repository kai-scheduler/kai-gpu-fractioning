# kai-gpu-fractioning
# -----------------------------------------------------------
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DOCKER_REPO_BASE ?= ghcr.io/kai-scheduler/kai-gpu-fractioning
# Target platform for image builds. GPU clusters are amd64; override for others.
# buildkit emulates (qemu) when the host arch differs.
PLATFORM ?= linux/amd64

# -----------------------------------------------------------
# Build
# -----------------------------------------------------------

.PHONY: build build-operator build-mpsd build-fractiond

build: build-operator build-mpsd build-fractiond

build-operator:
	$(MAKE) -C operator build

build-mpsd:
	go build -o bin/mpsd ./fractioning-manager/mpsd/cmd

build-fractiond:
	go build -o bin/fractiond ./fractioning-manager/fractiond/cmd

# -----------------------------------------------------------
# Test
# -----------------------------------------------------------

.PHONY: test test-operator test-fractioning-manager test-metricsd

test: test-operator test-fractioning-manager test-metricsd

test-operator:
	$(MAKE) -C operator test

test-fractioning-manager:
	go test ./fractioning-manager/... -race -count=1

# metricsd is a separate Go module (own go.mod), so `go test ./fractioning-manager/...`
# above does not descend into it. Delegate to its own Makefile, which handles the
# cgo/NVML build the metrics collector needs.
test-metricsd:
	$(MAKE) -C fractioning-manager/metricsd test

# -----------------------------------------------------------
# E2E — see test/e2e/e2e.mk (targets: e2e, e2e-cluster-up/down,
# e2e-deploy/undeploy, test-e2e, test-e2e-metrics).
# -----------------------------------------------------------

include test/e2e/e2e.mk

# -----------------------------------------------------------
# Code quality
# -----------------------------------------------------------

.PHONY: fmt vet lint validate

fmt:
	$(MAKE) -C operator fmt
	go fmt ./fractioning-manager/...

vet:
	$(MAKE) -C operator vet
	go vet ./fractioning-manager/...

lint:
	$(MAKE) -C operator lint
	golangci-lint run ./fractioning-manager/...

validate:
	$(MAKE) -C operator validate
	go fmt ./fractioning-manager/...
	go vet ./fractioning-manager/...
	golangci-lint run ./fractioning-manager/...

fix-boilerplate:
	$(MAKE) -C operator fix-boilerplate
	@year=$$(date +%Y); \
	for f in $$(find api -name '*.go' -not -path '*/vendor/*'); do \
		if ! head -2 "$$f" | grep -q 'Copyright'; then \
			echo "  FIXING: $$f"; \
			header=$$(sed "s/YEAR/$$year/" operator/hack/boilerplate.go.txt); \
			printf '%s\n\n' "$$header" | cat - "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; \
		fi; \
	done

# -----------------------------------------------------------
# Generate (CRDs, deepcopy, manifests) — delegated to operator
# -----------------------------------------------------------

.PHONY: generate manifests

generate:
	$(MAKE) -C operator generate

manifests:
	$(MAKE) -C operator manifests

# -----------------------------------------------------------
# Deploy
# -----------------------------------------------------------

.PHONY: deploy

deploy:
	$(MAKE) -C operator deploy

# -----------------------------------------------------------
# Docker
# -----------------------------------------------------------

.PHONY: docker-build docker-build-operator docker-build-mpsd docker-build-fractiond docker-build-metricsd
.PHONY: docker-push docker-push-operator docker-push-mpsd docker-push-fractiond docker-push-metricsd

docker-build: docker-build-operator docker-build-mpsd docker-build-fractiond docker-build-metricsd

docker-build-operator:
	$(MAKE) -C operator docker-build IMG=$(DOCKER_REPO_BASE)/operator:$(VERSION) PLATFORM=$(PLATFORM)

docker-build-mpsd:
	docker build --platform $(PLATFORM) -f fractioning-manager/mpsd/build/Dockerfile -t $(DOCKER_REPO_BASE)/mpsd:$(VERSION) .

docker-build-fractiond:
	docker build --platform $(PLATFORM) -f fractioning-manager/fractiond/build/Dockerfile -t $(DOCKER_REPO_BASE)/fractiond:$(VERSION) .

# metricsd links NVML (cgo) and is built from the repo root so its replace of the
# shared fractiond module resolves. The build stage runs as the target platform so
# cgo uses a native toolchain. GO_TAGS=e2e builds the fake-GPU test image.
docker-build-metricsd:
	docker build --platform $(PLATFORM) -f fractioning-manager/metricsd/Dockerfile -t $(DOCKER_REPO_BASE)/metricsd:$(VERSION) .

docker-push: docker-push-operator docker-push-mpsd docker-push-fractiond docker-push-metricsd

docker-push-operator:
	$(MAKE) -C operator docker-push IMG=$(DOCKER_REPO_BASE)/operator:$(VERSION)

docker-push-mpsd:
	docker push $(DOCKER_REPO_BASE)/mpsd:$(VERSION)

docker-push-metricsd:
	docker push $(DOCKER_REPO_BASE)/metricsd:$(VERSION)

docker-push-fractiond:
	docker push $(DOCKER_REPO_BASE)/fractiond:$(VERSION)

# -----------------------------------------------------------
# Multi-arch image build/push (release)
# -----------------------------------------------------------
# Release builds go through `docker buildx` so a single command produces a
# multi-platform manifest. Multi-platform manifests can't be loaded into the
# local daemon, so they push directly (DOCKER_BUILDX_OUTPUT defaults to --push).
# Requires a docker-container buildx builder (CI: docker/setup-buildx-action) plus
# QEMU/binfmt (docker/setup-qemu-action) for building any non-native platform.
#
#   make docker-buildx VERSION=v0.1.0 \
#     DOCKER_BUILD_PLATFORM=linux/amd64,linux/arm64             # multi-arch, push
#   make docker-buildx-operator DOCKER_BUILDX_OUTPUT=--load \
#     DOCKER_BUILD_PLATFORM=linux/amd64                         # single-arch, local
#
# NOTE: operator/mpsd/fractiond are CGO_ENABLED=0 and cross-compile natively on the
# build host; metricsd links NVML via cgo, so its non-native arch is built under
# qemu emulation (slower).

# Comma-separated platform list. GPU nodes may be amd64 or arm64 (e.g. GB200 is
# arm64), so releases build both — the release workflow passes the full list. The
# default here is a single arch for convenient local `--load` builds.
DOCKER_BUILD_PLATFORM ?= linux/amd64
# Passed to `docker buildx build`: --push to publish, --load for a local single-arch image.
DOCKER_BUILDX_OUTPUT  ?= --push
# Escape hatch for any extra `docker buildx build` flags.
DOCKER_BUILDX_ARGS    ?=

# Release metadata for images that bake it into the binary (currently metricsd only).
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: docker-buildx docker-buildx-operator docker-buildx-mpsd docker-buildx-fractiond docker-buildx-metricsd

# $(1)=Dockerfile path, $(2)=image name, $(3)=optional extra build args.
# Build context is always the repo root.
define buildx-image
docker buildx build --platform $(DOCKER_BUILD_PLATFORM) $(DOCKER_BUILDX_OUTPUT) $(DOCKER_BUILDX_ARGS) $(3) \
	-f $(1) -t $(DOCKER_REPO_BASE)/$(2):$(VERSION) .
endef

docker-buildx: docker-buildx-operator docker-buildx-mpsd docker-buildx-fractiond docker-buildx-metricsd

docker-buildx-operator:
	$(call buildx-image,operator/Dockerfile,operator)

docker-buildx-mpsd:
	$(call buildx-image,fractioning-manager/mpsd/build/Dockerfile,mpsd)

docker-buildx-fractiond:
	$(call buildx-image,fractioning-manager/fractiond/build/Dockerfile,fractiond)

# metricsd bakes version metadata into the binary via ldflags (ARG VERSION/COMMIT/DATE
# in its Dockerfile), so pass them through — otherwise a release image reports version=dev.
docker-buildx-metricsd:
	$(call buildx-image,fractioning-manager/metricsd/Dockerfile,metricsd,--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(GIT_COMMIT) --build-arg DATE=$(BUILD_DATE))

# -----------------------------------------------------------
# Clean
# -----------------------------------------------------------

.PHONY: clean

clean:
	rm -rf bin/ coverage.out
	$(MAKE) -C operator clean
