# gpu-sharing-operator
# -----------------------------------------------------------
MODULE   := github.com/run-ai/gpu-sharing-operator
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REGISTRY ?= gcr.io/run-ai-prod

BIN_DIR  := bin
LDFLAGS  := -ldflags "-X $(MODULE)/internal/version.Version=$(VERSION)"

# Components
OPERATOR_CMD  := ./operator/cmd
MPSD_CMD      := ./sharing-manager/mpsd/cmd
SHARINGD_CMD  := ./sharing-manager/sharingd/cmd

# -----------------------------------------------------------
# Build
# -----------------------------------------------------------

.PHONY: build build-operator build-mpsd build-sharingd

build: build-operator build-mpsd build-sharingd

build-operator:
	go build $(LDFLAGS) -o $(BIN_DIR)/operator $(OPERATOR_CMD)

build-mpsd:
	go build $(LDFLAGS) -o $(BIN_DIR)/mpsd $(MPSD_CMD)

build-sharingd:
	go build $(LDFLAGS) -o $(BIN_DIR)/sharingd $(SHARINGD_CMD)

# -----------------------------------------------------------
# Test
# -----------------------------------------------------------

.PHONY: test test-unit test-cover

test: test-unit

test-unit:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out

# -----------------------------------------------------------
# Code quality
# -----------------------------------------------------------

.PHONY: fmt vet lint validate

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

validate: fmt vet lint

# -----------------------------------------------------------
# Generate (CRDs, deepcopy, manifests)
# -----------------------------------------------------------

.PHONY: generate manifests

generate:
	controller-gen object paths="./..."

manifests:
	controller-gen crd rbac:roleName=gpu-sharing-operator paths="./..." output:crd:dir=config/crd

# -----------------------------------------------------------
# Docker (skeletons — Dockerfiles added in later phases)
# -----------------------------------------------------------

.PHONY: docker-build docker-build-operator docker-build-mpsd docker-build-sharingd
.PHONY: docker-push docker-push-operator docker-push-mpsd docker-push-sharingd

docker-build: docker-build-operator docker-build-mpsd docker-build-sharingd

docker-build-operator:
	docker build -f operator/build/Dockerfile -t $(REGISTRY)/gpu-sharing-operator:$(VERSION) .

docker-build-mpsd:
	docker build -f sharing-manager/mpsd/build/Dockerfile -t $(REGISTRY)/mpsd:$(VERSION) .

docker-build-sharingd:
	docker build -f sharing-manager/sharingd/build/Dockerfile -t $(REGISTRY)/sharingd:$(VERSION) .

docker-push: docker-push-operator docker-push-mpsd docker-push-sharingd

docker-push-operator:
	docker push $(REGISTRY)/gpu-sharing-operator:$(VERSION)

docker-push-mpsd:
	docker push $(REGISTRY)/mpsd:$(VERSION)

docker-push-sharingd:
	docker push $(REGISTRY)/sharingd:$(VERSION)

# -----------------------------------------------------------
# Clean
# -----------------------------------------------------------

.PHONY: clean

clean:
	rm -rf $(BIN_DIR) coverage.out
