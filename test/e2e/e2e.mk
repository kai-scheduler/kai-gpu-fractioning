# -----------------------------------------------------------
# E2E (metrics-only, stage 1). Requires k3d, kubectl, helm, docker, python3
# (see test/e2e/hack/requirements.txt).
#
#   make e2e                          # cluster up + load plugin image + run tests
#   make e2e-cluster-down             # tear down the k3d cluster
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
E2E_PLUGIN_IMAGE              ?= gpu-sharing-plugin:e2e
E2E_FAKE_GPU_OPERATOR_VERSION ?=
PYTHON                        ?= python3

export E2E_CLUSTER_NAME
export E2E_GPU_WORKER_NODES
export E2E_NON_GPU_WORKER_NODES
export E2E_FAKE_GPU_OPERATOR_VERSION

.PHONY: e2e e2e-cluster-up e2e-cluster-down e2e-cluster-deps e2e-build-plugin-image e2e-load-plugin-image test-e2e test-e2e-metrics run-e2e

e2e: e2e-cluster-up e2e-load-plugin-image test-e2e

# E2E_GO_TEST wraps a suite's `go test` invocation. Each suite lives in its own
# tests/<suite> package so a CI job can run exactly one suite.
E2E_GO_TEST = cd test/e2e && E2E_PLUGIN_IMAGE=$(E2E_PLUGIN_IMAGE) E2E_GPU_NODE_COUNT=$(E2E_GPU_WORKER_NODES) go test -tags e2e -v -timeout 20m

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

# run-e2e runs the suite against whatever cluster the caller provides
# (set E2E_KUBECONFIG=...). The suite never provisions a cluster, so this works
# against any cluster regardless of how it was created — not just k3d.
run-e2e: test-e2e
