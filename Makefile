# Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# kai-gpu-fractioning
# -----------------------------------------------------------
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DOCKER_REPO_BASE ?= ghcr.io/kai-scheduler/kai-gpu-fractioning
# Target platform for image builds. GPU clusters are amd64; override for others.
# buildkit emulates (qemu) when the host arch differs.
PLATFORM ?= linux/amd64

LOCALBIN ?= $(CURDIR)/bin
ADDLICENSE ?= $(LOCALBIN)/addlicense
ADDLICENSE_VERSION ?= v1.2.0

# The exact copyright holder string OSRB checks for. Do not reword.
LICENSE_HOLDER := NVIDIA CORPORATION & AFFILIATES. All rights reserved.
LICENSE_YEAR ?= 2026

# addlicense does not honor .gitignore, so ignored source-like paths are listed
# here to keep validation reproducible in developer worktrees.
LICENSE_IGNORES := \
	-ignore 'bin/**' \
	-ignore 'dist/**' \
	-ignore '.context/**' \
	-ignore '.gocache/**' \
	-ignore '.gotmp/**' \
	-ignore '.local/**' \
	-ignore '.idea/**' \
	-ignore '.vscode/**' \
	-ignore '**/testdata/**'

# -----------------------------------------------------------
# Build
# -----------------------------------------------------------

.PHONY: build build-operator build-mpsd build-fractiond

build: build-operator build-mpsd build-fractiond

build-operator:
	$(MAKE) -C operator build

build-mpsd:
	CGO_ENABLED=1 go build -o bin/mpsd ./fractioning-manager/mpsd/cmd

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
# Air-gap / ImageLock
# -----------------------------------------------------------

IMAGE_LOCK_OUT_DIR ?= $(CURDIR)/dist
IMAGE_LOCK_PLATFORMS ?= linux/amd64 linux/arm64
IMAGE_LOCK_PLATFORM_ARGS = $(foreach platform,$(IMAGE_LOCK_PLATFORMS),--platform $(platform))

.PHONY: image-lock image-lock-verify image-lock-test

image-lock:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make image-lock VERSION=v1.2.3)" >&2; exit 1; }
	GOWORK=off go -C hack/imagelock run . \
		--version "$(VERSION)" \
		--chart "$(CURDIR)/operator/charts" \
		--out-dir "$(IMAGE_LOCK_OUT_DIR)" \
		$(IMAGE_LOCK_PLATFORM_ARGS)

image-lock-verify:
	GOWORK=off go -C hack/imagelock run . \
		--verify-only \
		--chart "$(CURDIR)/operator/charts"

image-lock-test:
	GOWORK=off go -C hack/imagelock test ./...

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

validate: license-check
	$(MAKE) -C operator validate
	go fmt ./fractioning-manager/...
	go vet ./fractioning-manager/...
	golangci-lint run ./fractioning-manager/...

# -----------------------------------------------------------
# License headers
#
# Every authored file carries the two-line Apache-2.0 SPDX header. This is
# enforced repo-wide (all modules, the Helm chart, the Dockerfiles and CI
# config), not left to convention — `license-check` is part of `validate` and
# runs as its own required CI job.
# -----------------------------------------------------------

.PHONY: addlicense gen-license license-check

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

addlicense: $(ADDLICENSE) ## Install addlicense locally.
$(ADDLICENSE): | $(LOCALBIN)
	test -s $(ADDLICENSE) || GOBIN=$(LOCALBIN) go install github.com/google/addlicense@$(ADDLICENSE_VERSION)

gen-license: addlicense ## Add the Apache-2.0 SPDX header to files that are missing it.
	$(ADDLICENSE) -c "$(LICENSE_HOLDER)" -y $(LICENSE_YEAR) -s=only -l apache -v \
		$(LICENSE_IGNORES) .

license-check: addlicense ## Verify every file carries the Apache-2.0 SPDX header.
	$(ADDLICENSE) -check -c "$(LICENSE_HOLDER)" -s=only -l apache \
		$(LICENSE_IGNORES) .

# -----------------------------------------------------------
# Third-party attribution
#
# THIRD-PARTY.txt is generated from the modules `go list -deps` reports for the
# four shipped binaries, and is copied into every image at /THIRD-PARTY.txt.
# -----------------------------------------------------------

.PHONY: third-party third-party-check

third-party: ## Regenerate THIRD-PARTY.txt from the linked dependency set.
	python3 hack/gen-third-party.py

third-party-check: ## Fail if THIRD-PARTY.txt does not match the linked dependency set.
	python3 hack/gen-third-party.py --check

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
# NOTE: operator and fractiond are CGO_ENABLED=0 and cross-compile natively on
# the build host. mpsd and metricsd link NVML via cgo, so their non-native arch
# stages run under qemu emulation (slower).

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
