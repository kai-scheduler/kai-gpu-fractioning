# gpu-sharing-operator
# -----------------------------------------------------------
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REGISTRY ?= gcr.io/run-ai-prod
# Target platform for image builds. GPU clusters are amd64; override for others.
# buildkit emulates (qemu) when the host arch differs.
PLATFORM ?= linux/amd64

# -----------------------------------------------------------
# Build
# -----------------------------------------------------------

.PHONY: build build-operator build-mpsd build-sharingd

build: build-operator build-mpsd build-sharingd

build-operator:
	$(MAKE) -C operator build

build-mpsd:
	go build -o bin/mpsd ./sharing-manager/mpsd/cmd

build-sharingd:
	go build -o bin/sharingd ./sharing-manager/sharingd/cmd

# -----------------------------------------------------------
# Test
# -----------------------------------------------------------

.PHONY: test test-operator test-sharing-manager test-metricsd

test: test-operator test-sharing-manager test-metricsd

test-operator:
	$(MAKE) -C operator test

test-sharing-manager:
	go test ./sharing-manager/... -race -count=1

# metricsd is a separate Go module (own go.mod), so `go test ./sharing-manager/...`
# above does not descend into it. Delegate to its own Makefile, which handles the
# cgo/NVML build the metrics collector needs.
test-metricsd:
	$(MAKE) -C sharing-manager/metricsd test

# -----------------------------------------------------------
# Code quality
# -----------------------------------------------------------

.PHONY: fmt vet lint validate

fmt:
	$(MAKE) -C operator fmt
	go fmt ./sharing-manager/...

vet:
	$(MAKE) -C operator vet
	go vet ./sharing-manager/...

lint:
	$(MAKE) -C operator lint
	golangci-lint run ./sharing-manager/...

validate:
	$(MAKE) -C operator validate
	go fmt ./sharing-manager/...
	go vet ./sharing-manager/...
	golangci-lint run ./sharing-manager/...

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

.PHONY: docker-build docker-build-operator docker-build-mpsd docker-build-sharingd docker-build-metricsd
.PHONY: docker-push docker-push-operator docker-push-mpsd docker-push-sharingd docker-push-metricsd

docker-build: docker-build-operator docker-build-mpsd docker-build-sharingd docker-build-metricsd

docker-build-operator:
	$(MAKE) -C operator docker-build IMG=$(REGISTRY)/gpu-sharing-operator:$(VERSION) PLATFORM=$(PLATFORM)

docker-build-mpsd:
	docker build --platform $(PLATFORM) -f sharing-manager/mpsd/build/Dockerfile -t $(REGISTRY)/mpsd:$(VERSION) .

docker-build-sharingd:
	docker build --platform $(PLATFORM) -f sharing-manager/sharingd/build/Dockerfile -t $(REGISTRY)/sharingd:$(VERSION) .

# metricsd links NVML (cgo) and is built from the repo root so its replace of the
# shared sharingd module resolves. The build stage runs as the target platform so
# cgo uses a native toolchain. GO_TAGS=e2e builds the fake-GPU test image.
docker-build-metricsd:
	docker build --platform $(PLATFORM) -f sharing-manager/metricsd/Dockerfile -t $(REGISTRY)/metricsd:$(VERSION) .

docker-push: docker-push-operator docker-push-mpsd docker-push-sharingd docker-push-metricsd

docker-push-operator:
	$(MAKE) -C operator docker-push IMG=$(REGISTRY)/gpu-sharing-operator:$(VERSION)

docker-push-mpsd:
	docker push $(REGISTRY)/mpsd:$(VERSION)

docker-push-metricsd:
	docker push $(REGISTRY)/metricsd:$(VERSION)

docker-push-sharingd:
	docker push $(REGISTRY)/sharingd:$(VERSION)

# -----------------------------------------------------------
# Clean
# -----------------------------------------------------------

.PHONY: clean

clean:
	rm -rf bin/ coverage.out
	$(MAKE) -C operator clean
