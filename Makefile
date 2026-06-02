# gpu-sharing-operator
# -----------------------------------------------------------
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REGISTRY ?= gcr.io/run-ai-prod

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

.PHONY: test test-operator test-sharing-manager

test: test-operator test-sharing-manager

test-operator:
	$(MAKE) -C operator test

test-sharing-manager:
	go test ./sharing-manager/... -race -count=1

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

validate: fmt vet lint

# -----------------------------------------------------------
# Generate (CRDs, deepcopy, manifests) — delegated to operator
# -----------------------------------------------------------

.PHONY: generate manifests

generate:
	$(MAKE) -C operator generate

manifests:
	$(MAKE) -C operator manifests

# -----------------------------------------------------------
# Docker
# -----------------------------------------------------------

.PHONY: docker-build docker-build-operator docker-build-mpsd docker-build-sharingd
.PHONY: docker-push docker-push-operator docker-push-mpsd docker-push-sharingd

docker-build: docker-build-operator docker-build-mpsd docker-build-sharingd

docker-build-operator:
	$(MAKE) -C operator docker-build IMG=$(REGISTRY)/gpu-sharing-operator:$(VERSION)

docker-build-mpsd:
	docker build -f sharing-manager/mpsd/build/Dockerfile -t $(REGISTRY)/mpsd:$(VERSION) .

docker-build-sharingd:
	docker build -f sharing-manager/sharingd/build/Dockerfile -t $(REGISTRY)/sharingd:$(VERSION) .

docker-push: docker-push-operator docker-push-mpsd docker-push-sharingd

docker-push-operator:
	$(MAKE) -C operator docker-push IMG=$(REGISTRY)/gpu-sharing-operator:$(VERSION)

docker-push-mpsd:
	docker push $(REGISTRY)/mpsd:$(VERSION)

docker-push-sharingd:
	docker push $(REGISTRY)/sharingd:$(VERSION)

# -----------------------------------------------------------
# Clean
# -----------------------------------------------------------

.PHONY: clean

clean:
	rm -rf bin/ coverage.out
	$(MAKE) -C operator clean
