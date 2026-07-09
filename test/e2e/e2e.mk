# -----------------------------------------------------------
# E2E (metrics-only, stage 1). Requires k3d, kubectl, helm, docker, python3
# (see test/e2e/hack/requirements.txt).
#
#   make e2e                          # cluster up + load images + helm deploy + run tests
#   make e2e-cluster-down             # tear down the k3d cluster
#
# The gpu-sharing stack is built and installed the way it ships, by Skaffold
# (skaffold.yaml): it builds all four component images (operator, sharingd,
# metricsd, mpsd), loads them into the cluster, and `helm install`s the operator
# chart (operator/charts). The operator then creates the sharingd DaemonSet
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

# Namespace the operator chart is installed into (must match skaffold.yaml's
# release namespace). The Go suite reads this via E2E_OPERATOR_NAMESPACE.
E2E_OPERATOR_NAMESPACE        ?= gpu-sharing-operator

# Dedicated kubeconfig for the e2e cluster. create-cluster.py writes it here
# (it can't use ~/.kube/config — the NRI mount breaks `k3d kubeconfig`), and the
# deploy/test targets below point kubectl/helm/skaffold/go-test at it. This keeps
# the whole flow off your default context (e.g. a remote cluster) entirely.
E2E_KUBECONFIG                ?= $(HOME)/.kube/$(E2E_CLUSTER_NAME).yaml

# Host arch, passed to Skaffold for metricsd's TARGETARCH build-arg (its
# Dockerfile hardcodes ARG TARGETARCH=amd64, which otherwise wins over the build
# platform). amd64 CI runners get amd64; Apple Silicon gets arm64.
E2E_ARCH                      ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')

export E2E_CLUSTER_NAME
export E2E_GPU_WORKER_NODES
export E2E_NON_GPU_WORKER_NODES
export E2E_FAKE_GPU_OPERATOR_VERSION
export E2E_ARCH
# create-cluster.py reads E2E_KUBECONFIG to decide where to write the kubeconfig;
# the Go suite (config.go) reads it to connect. skaffold/helm/kubectl instead read
# KUBECONFIG, so the deploy/undeploy recipes set it inline from E2E_KUBECONFIG.
export E2E_KUBECONFIG

.PHONY: e2e e2e-cluster-up e2e-cluster-down e2e-cluster-deps \
	e2e-deploy e2e-undeploy test-e2e test-e2e-metrics run-e2e

e2e: e2e-cluster-up e2e-deploy test-e2e

# E2E_GO_TEST wraps a suite's `go test` invocation. Each suite lives in its own
# tests/<suite> package so a CI job can run exactly one suite.
E2E_GO_TEST = cd test/e2e && E2E_OPERATOR_NAMESPACE=$(E2E_OPERATOR_NAMESPACE) E2E_GPU_NODE_COUNT=$(E2E_GPU_WORKER_NODES) go test -tags e2e -v -timeout 20m

e2e-cluster-deps:
	$(PYTHON) -m pip install -q -r test/e2e/hack/requirements.txt

e2e-cluster-up: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py

e2e-cluster-down: e2e-cluster-deps
	$(PYTHON) test/e2e/hack/create-cluster.py --delete

# e2e-deploy builds all four images, loads them into the (k3d) cluster, and helm
# installs the operator chart — all via `skaffold run -p e2e` (skaffold.yaml).
# Skaffold auto-loads images into a k3d/kind kube-context, so the current context
# must point at the e2e cluster.
#
# Flags:
#   --platform pins the build to the host arch instead of letting Skaffold probe
#     the cluster for its node platform: that probe fails (and passes an empty
#     --platform to metricsd's `FROM --platform=$TARGETPLATFORM`) whenever the
#     current kube-context is unreachable or non-k3d.
#   --cache-artifacts=false skips Skaffold's registry-backed cache check, which
#     otherwise shells out to a docker credential helper (e.g. gcloud) just to
#     look up images we never push.
#   --verbosity=error silences Skaffold's "image [sharingd|mpsd|metricsd] is not
#     used" warnings: those images reach the pods indirectly (the chart passes
#     their repo/tag to the operator, which creates the DaemonSets), so Skaffold's
#     rendered-manifest scan can't see them. False positives, not real problems.
#
# It then waits on the sharingd DaemonSet specifically, NOT the operator's
# aggregate CR Ready condition: the chart also creates the mpsd DaemonSet, which
# sets RuntimeClassName "nvidia" and therefore never schedules on a plain k3d
# cluster (no such runtime) — that's out of scope for the metrics suite and must
# not gate it.
E2E_SKAFFOLD_FLAGS = --platform=linux/$(E2E_ARCH) --cache-artifacts=false --verbosity=error

e2e-deploy:
	KUBECONFIG=$(E2E_KUBECONFIG) skaffold run -p e2e $(E2E_SKAFFOLD_FLAGS)
	@echo "Waiting for the operator to create the sharingd DaemonSet..."
	@for i in $$(seq 1 60); do KUBECONFIG=$(E2E_KUBECONFIG) kubectl -n $(E2E_OPERATOR_NAMESPACE) get ds gpu-sharing-sharingd >/dev/null 2>&1 && break; sleep 2; done
	KUBECONFIG=$(E2E_KUBECONFIG) kubectl -n $(E2E_OPERATOR_NAMESPACE) rollout status ds/gpu-sharing-sharingd --timeout=180s

e2e-undeploy:
	KUBECONFIG=$(E2E_KUBECONFIG) skaffold delete -p e2e --verbosity=error

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
