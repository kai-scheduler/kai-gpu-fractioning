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
# E2E (metrics-only, stage 1). Requires k3d, kubectl, helm, docker, python3
# (see test/e2e/hack/requirements.txt).
#
#   make e2e                         # cluster up + load plugin image + run tests
#   make e2e-cluster-down             # tear down the k3d cluster
#
# All knobs are E2E_* env vars — see test/e2e/README.md and
# test/e2e/hack/create-cluster.py for the full list (node count, fake-gpu-operator
# version, GPUs per node, etc).
# -----------------------------------------------------------

E2E_CLUSTER_NAME              ?= gpu-sharing-e2e
E2E_GPU_WORKER_NODES          ?= 2
E2E_NON_GPU_WORKER_NODES      ?= 1
E2E_PLUGIN_IMAGE              ?= gpu-sharing-plugin:e2e
E2E_FAKE_GPU_OPERATOR_VERSION ?=
PYTHON                        ?= python3

export E2E_CLUSTER_NAME
export E2E_GPU_WORKER_NODES
export E2E_NON_GPU_WORKER_NODES
export E2E_FAKE_GPU_OPERATOR_VERSION

.PHONY: e2e e2e-cluster-up e2e-cluster-down e2e-cluster-deps e2e-build-plugin-image e2e-load-plugin-image test-e2e test-e2e-metrics

e2e: e2e-cluster-up e2e-load-plugin-image test-e2e

# E2E_GO_TEST wraps a suite's `go test` invocation. Each suite lives in its own
# tests/<suite> package so a CI job can run exactly one suite.
E2E_GO_TEST = cd test/e2e && E2E_PLUGIN_IMAGE=$(E2E_PLUGIN_IMAGE) E2E_EXPECTED_GPU_NODES=$(E2E_GPU_WORKER_NODES) go test -tags e2e -v -timeout 20m

e2e-cluster-deps:
	$(PYTHON) -m pip install -q -r test/e2e/hack/requirements.txt

e2e-cluster-up: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py

e2e-cluster-down: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py --delete

e2e-build-plugin-image:
	docker build --build-arg GO_TAGS=e2e -t $(E2E_PLUGIN_IMAGE) -f sharing-manager/metricsd/Dockerfile sharing-manager/metricsd

e2e-load-plugin-image: e2e-build-plugin-image
	k3d image import $(E2E_PLUGIN_IMAGE) --cluster $(E2E_CLUSTER_NAME)

# test-e2e runs every suite. CI jobs should call a specific suite target
# (e.g. test-e2e-metrics) so each job runs only its own suite.
test-e2e:
	$(E2E_GO_TEST) ./tests/...

test-e2e-metrics:
	$(E2E_GO_TEST) ./tests/metrics/...

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
