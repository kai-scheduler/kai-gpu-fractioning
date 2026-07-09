# -----------------------------------------------------------
# E2E (metrics-only, stage 1). Requires k3d, kubectl, helm, docker, python3
# (see test/e2e/hack/requirements.txt).
#
#   make e2e                          # cluster up + load images + helm deploy + run tests
#   make e2e-cluster-down             # tear down the k3d cluster
#
# The gpu-sharing stack is installed the way it ships: the operator Helm chart
# (operator/charts) is deployed, and the operator creates the sharingd DaemonSet
# (which hosts the metricsd sidecar under test) and the mpsd DaemonSet. The Go
# suite only connects to the deployed cluster and asserts — it deploys nothing.
#
# All knobs are E2E_* env vars — see test/e2e/README.md and
# test/e2e/hack/create-cluster.py for the full list (node count, fake-gpu-operator
# version, GPUs per node, etc).
#
# Included from the root Makefile, so all paths here are relative to the repo
# root (make's `include` does not change the working directory).
# -----------------------------------------------------------

E2E_CLUSTER_NAME              ?= gpu-sharing-e2e
E2E_GPU_WORKER_NODES          ?= 2
E2E_NON_GPU_WORKER_NODES      ?= 1
E2E_FAKE_GPU_OPERATOR_VERSION ?=
PYTHON                        ?= python3

# Namespace the operator chart is installed into (and where the operator creates
# its DaemonSets). The Go suite reads this via E2E_OPERATOR_NAMESPACE.
E2E_OPERATOR_NAMESPACE        ?= gpu-sharing-operator
E2E_HELM_RELEASE              ?= gso

# Local image refs loaded into k3d. E2E_IMAGE_PREFIX is a local-only repo prefix
# (never pushed); pullPolicy is forced to Never so nodes use the loaded images.
E2E_IMAGE_PREFIX              ?= e2e
E2E_IMAGE_TAG                 ?= e2e
E2E_OPERATOR_IMAGE            ?= $(E2E_IMAGE_PREFIX)/gpu-sharing-operator:$(E2E_IMAGE_TAG)
E2E_SHARINGD_IMAGE            ?= $(E2E_IMAGE_PREFIX)/sharingd:$(E2E_IMAGE_TAG)
E2E_METRICSD_IMAGE            ?= $(E2E_IMAGE_PREFIX)/metricsd:$(E2E_IMAGE_TAG)
E2E_MPSD_IMAGE                ?= $(E2E_IMAGE_PREFIX)/mpsd:$(E2E_IMAGE_TAG)

# Build images for the host arch so they run natively on the local k3d nodes
# (which are the host arch) with no qemu cross-compile. amd64 CI runners get
# amd64; Apple Silicon gets arm64. Overridable, e.g. E2E_ARCH=amd64.
E2E_ARCH                      ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
E2E_PLATFORM                  ?= linux/$(E2E_ARCH)

export E2E_CLUSTER_NAME
export E2E_GPU_WORKER_NODES
export E2E_NON_GPU_WORKER_NODES
export E2E_FAKE_GPU_OPERATOR_VERSION

.PHONY: e2e e2e-cluster-up e2e-cluster-down e2e-cluster-deps \
	e2e-build-images e2e-build-metricsd-image e2e-load-images e2e-deploy e2e-undeploy \
	test-e2e test-e2e-metrics run-e2e

e2e: e2e-cluster-up e2e-load-images e2e-deploy test-e2e

# E2E_GO_TEST wraps a suite's `go test` invocation. Each suite lives in its own
# tests/<suite> package so a CI job can run exactly one suite.
E2E_GO_TEST = cd test/e2e && E2E_OPERATOR_NAMESPACE=$(E2E_OPERATOR_NAMESPACE) E2E_GPU_NODE_COUNT=$(E2E_GPU_WORKER_NODES) go test -tags e2e -v -timeout 20m

e2e-cluster-deps:
	$(PYTHON) -m pip install -q -r test/e2e/hack/requirements.txt

e2e-cluster-up: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py

e2e-cluster-down: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py --delete

# Build all four images the operator chart deploys. operator/sharingd/mpsd reuse
# the root docker-build-* targets (retagged into the local e2e prefix); metricsd
# needs GO_TAGS=e2e for its fake-GPU detection, so it has a dedicated recipe.
# metricsd's Dockerfile hardcodes `ARG TARGETARCH=amd64`, so TARGETARCH must be
# passed as a build-arg (not just --platform) to build natively for the host.
e2e-build-images: e2e-build-metricsd-image
	$(MAKE) docker-build-operator REGISTRY=$(E2E_IMAGE_PREFIX) VERSION=$(E2E_IMAGE_TAG) PLATFORM=$(E2E_PLATFORM)
	$(MAKE) docker-build-sharingd REGISTRY=$(E2E_IMAGE_PREFIX) VERSION=$(E2E_IMAGE_TAG) PLATFORM=$(E2E_PLATFORM)
	$(MAKE) docker-build-mpsd     REGISTRY=$(E2E_IMAGE_PREFIX) VERSION=$(E2E_IMAGE_TAG) PLATFORM=$(E2E_PLATFORM)

e2e-build-metricsd-image:
	docker build --platform $(E2E_PLATFORM) --build-arg GO_TAGS=e2e --build-arg TARGETARCH=$(E2E_ARCH) -t $(E2E_METRICSD_IMAGE) -f sharing-manager/metricsd/Dockerfile .

e2e-load-images: e2e-build-images
	k3d image import $(E2E_OPERATOR_IMAGE) $(E2E_SHARINGD_IMAGE) $(E2E_METRICSD_IMAGE) $(E2E_MPSD_IMAGE) --cluster $(E2E_CLUSTER_NAME)

# e2e-deploy installs the operator chart with the locally loaded images and waits
# for the operator to create the sharingd DaemonSet (which carries the metricsd
# sidecar under test) and roll it out.
#
# It waits on the sharingd DaemonSet specifically, NOT the operator's aggregate CR
# Ready condition: the chart also creates the mpsd DaemonSet, which sets
# RuntimeClassName "nvidia" and therefore never schedules on a plain k3d cluster
# (no such runtime) — that's out of scope for the metrics suite and must not gate it.
e2e-deploy:
	helm upgrade -i $(E2E_HELM_RELEASE) operator/charts -n $(E2E_OPERATOR_NAMESPACE) --create-namespace \
		--set images.operator.repository=$(E2E_IMAGE_PREFIX)/gpu-sharing-operator --set images.operator.tag=$(E2E_IMAGE_TAG) --set images.operator.pullPolicy=Never \
		--set images.sharingd.repository=$(E2E_IMAGE_PREFIX)/sharingd --set images.sharingd.tag=$(E2E_IMAGE_TAG) --set images.sharingd.pullPolicy=Never \
		--set images.metricsd.repository=$(E2E_IMAGE_PREFIX)/metricsd --set images.metricsd.tag=$(E2E_IMAGE_TAG) --set images.metricsd.pullPolicy=Never \
		--set images.mpsd.repository=$(E2E_IMAGE_PREFIX)/mpsd --set images.mpsd.tag=$(E2E_IMAGE_TAG) --set images.mpsd.pullPolicy=Never \
		--wait --timeout 120s
	@echo "Waiting for the operator to create the sharingd DaemonSet..."
	@for i in $$(seq 1 60); do kubectl -n $(E2E_OPERATOR_NAMESPACE) get ds gpu-sharing-sharingd >/dev/null 2>&1 && break; sleep 2; done
	kubectl -n $(E2E_OPERATOR_NAMESPACE) rollout status ds/gpu-sharing-sharingd --timeout=180s

e2e-undeploy:
	helm uninstall $(E2E_HELM_RELEASE) -n $(E2E_OPERATOR_NAMESPACE) --ignore-not-found

# test-e2e runs every suite. CI jobs should call a specific suite target
# (e.g. test-e2e-metrics) so each job runs only its own suite.
test-e2e:
	$(E2E_GO_TEST) ./tests/...

test-e2e-metrics:
	$(E2E_GO_TEST) ./tests/metrics/...

# run-e2e runs the suite against whatever cluster the caller provides
# (set E2E_KUBECONFIG=...). The suite never provisions a cluster, so this works
# against any cluster regardless of how it was created — not just k3d.
run-e2e: test-e2e
