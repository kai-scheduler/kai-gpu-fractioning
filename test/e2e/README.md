# gpu-sharing-operator e2e tests

Stage 1: metrics-only e2e tests for the metricsd metrics endpoint, running
against a real k3d cluster with
[fake-gpu-operator](https://github.com/run-ai/fake-gpu-operator) installed. The
gpu-sharing stack is installed the way it ships — via the operator Helm chart
(`operator/charts`) — and the operator creates the `sharingd` DaemonSet, which
hosts the **metricsd sidecar** under test. (metricsd is no longer a standalone
DaemonSet; it runs as a container in the sharingd pods.)

## Quick start

```sh
export E2E_FAKE_GPU_OPERATOR_VERSION=0.1.0   # see releases link below
make e2e
```

This creates a k3d cluster, installs fake-gpu-operator, builds and loads the
four `:e2e`-tagged images (operator, sharingd, metricsd, mpsd), installs the
operator Helm chart pointing at those images, and runs the test suite. Tear the
cluster down afterwards with:

```sh
make e2e-cluster-down
```

## How it's split

Cluster lifecycle, deployment, and the Go test suite are deliberately separate
concerns — the suite only connects and asserts, it deploys nothing:

| Step | Owner | What |
|---|---|---|
| Create k3d cluster (N worker nodes, NRI enabled) | `test/e2e/hack/create-cluster.py` | `k3d cluster create` with retry-on-failure |
| Install fake-gpu-operator | `test/e2e/hack/create-cluster.py` | `helm upgrade -i` with per-pool GPU count/product/memory |
| Build + load the 4 images | `make e2e-load-images` | host-arch `:e2e` images imported into k3d |
| Install the operator + create DaemonSets | `make e2e-deploy` (`helm` + operator) | `helm upgrade -i operator/charts`; the operator reconciles the CR and creates the sharingd (+ metricsd sidecar) and mpsd DaemonSets |
| Verify GPU nodes | `test/e2e/suite` (`nodes.VerifyGPUNodes`) | precondition check, no mutation |
| Run test assertions | `test/e2e/tests` | scrape the metricsd sidecar `/metrics`, assert |

> **mpsd on k3d:** the operator always creates an `mpsd` DaemonSet too, and its
> pods set `runtimeClassName: nvidia`, which a plain k3d cluster has no runtime
> for — so mpsd stays unschedulable and the operator's aggregate `Ready`
> condition is `False`. That's expected and out of scope for the metrics suite:
> `e2e-deploy` waits on the **sharingd DaemonSet** rollout, not the CR's
> aggregate readiness.

a standalone script (`create-cluster.py`) provisions the cluster and
cluster-level dependencies, while the Go suite only connects, verifies
preconditions, and deploys/tests the component actually under test.
`create-cluster.py` uses pydantic-settings for `E2E_*`-prefixed config, typer for
the CLI, and retry-on-create-failure, scoped down to what this project needs: a
k3d cluster with fake-gpu-operator
and nothing else, so the dependency list is just `typer`, `pydantic-settings`,
`sh` — no docker SDK (nothing here builds or pre-pulls images) and no `rich`
(plain stdout is enough for CI logs).

## Cluster configuration (`test/e2e/hack/create-cluster.py`)

All configurable via `E2E_*` environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `E2E_CLUSTER_NAME` | `gpu-sharing-e2e` | k3d cluster name |
| `E2E_GPU_WORKER_NODES` | `2` | number of agent nodes, each labeled into the fake-GPU node pool |
| `E2E_K3S_IMAGE` | `rancher/k3s:v1.31.5-k3s1` | k3s node image |
| `E2E_GPU_NODE_POOL` | `default` | fake-gpu-operator node pool name |
| `E2E_GPUS_PER_NODE` | `2` | GPUs advertised per node in that pool |
| `E2E_GPU_PRODUCT` | `NVIDIA A100-SXM4-40GB` | simulated GPU product |
| `E2E_GPU_MEMORY_MIB` | `40960` | simulated GPU memory (MiB) |
| `E2E_FAKE_GPU_OPERATOR_VERSION` | *(required)* | fake-gpu-operator Helm chart version — see [releases](https://github.com/run-ai/fake-gpu-operator/releases) |
| `E2E_MAX_RETRIES` | `3` | cluster-creation retry attempts |

```sh
pip install -r test/e2e/hack/requirements.txt

E2E_GPU_WORKER_NODES=4 E2E_FAKE_GPU_OPERATOR_VERSION=0.1.0 test/e2e/hack/create-cluster.py
test/e2e/hack/create-cluster.py --delete
test/e2e/hack/create-cluster.py --skip-fake-gpu-operator   # cluster only, e.g. for iterating on the script itself
```

Requires `k3d`, `kubectl`, `helm` (unless `--skip-fake-gpu-operator`), and
Python 3.9+ on PATH.

**Not installed by this script:** `nvml-mock` (NVIDIA's real-NVML simulator).
Without it, `gpu-sharing-plugin`'s NVML calls report zero memory/SM
utilization. If a later test needs real-looking values, install
`sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml` manually and
set `LD_LIBRARY_PATH` per its comments; this adds privileged host mounts and
hardcoded PID matching, so it's opt-in rather than part of the default e2e
cluster.

## Makefile targets

| Target | What |
|---|---|
| `make e2e` | cluster up → build+load images → helm deploy → run tests |
| `make e2e-cluster-up` | create the k3d cluster + install fake-gpu-operator |
| `make e2e-cluster-down` | delete the k3d cluster |
| `make e2e-cluster-deps` | `pip install -r test/e2e/hack/requirements.txt` |
| `make e2e-build-images` | build all four `:e2e` images (operator, sharingd, metricsd, mpsd) for the host arch |
| `make e2e-load-images` | build + `k3d image import` all four into `E2E_CLUSTER_NAME` |
| `make e2e-deploy` | `helm upgrade -i operator/charts` with the loaded images, then wait for the sharingd DaemonSet rollout |
| `make e2e-undeploy` | `helm uninstall` the operator release |
| `make test-e2e` | run all Go suites (cluster deployed already) |
| `make test-e2e-metrics` | run only the metrics suite (`./tests/metrics/...`) |
| `make run-e2e` | alias for `make test-e2e` — run against any cluster (`E2E_KUBECONFIG=...`), regardless of how it was created/deployed |

`E2E_CLUSTER_NAME`, `E2E_GPU_WORKER_NODES`, `E2E_OPERATOR_NAMESPACE`,
`E2E_IMAGE_PREFIX`, `E2E_IMAGE_TAG`, `E2E_ARCH`, `E2E_FAKE_GPU_OPERATOR_VERSION`
are overridable `make` variables. `PYTHON` overrides the Python interpreter
(default `python3`).

## Go test suite configuration

| Variable | Default | Purpose |
|---|---|---|
| `E2E_KUBECONFIG` / `KUBECONFIG` | `~/.kube/config` | cluster to connect to |
| `E2E_OPERATOR_NAMESPACE` | `gpu-sharing-operator` | namespace the operator is installed into (and where it creates the sharingd/mpsd DaemonSets) |
| `E2E_GPU_NODE_SELECTOR` | `nvidia.com/gpu.present=true` | label selector used to verify GPU nodes |
| `E2E_GPU_NODE_COUNT` | `0` (unchecked) | exact GPU node count to assert, if > 0 — set to `E2E_GPU_WORKER_NODES` |

## What's covered

- `TestE2E_GPUSharingPluginMetricsEndpointHealthy` — smoke test: the metricsd
  sidecar's `/metrics` endpoint (port 2112) in the operator-created sharingd
  pods is reachable and returns valid Prometheus output.

## CI

`.github/workflows/e2e.yaml` runs the pipeline on PRs touching
`sharing-manager/**` or `test/e2e/**`: it installs `k3d` (official install
script) and Helm via the `e2e-setup` composite action, then runs
`make e2e-load-images` → `make e2e-deploy` (helm-installs the operator, as grove
deployed its operator in-workflow) → `make test-e2e-metrics`, and tears the
cluster down in an `if: always()` step. On failure it dumps node/pod state, the
`GpuSharingConfig` status, and sharingd/operator/status-updater logs before
tearing down.

## Package layout

Each Go package is single-purpose, so dependencies only point one way (the
`metrics` leaf has no dependency on the rest of the framework, so future
additions can depend on it without an import cycle). The k8s-touching helpers
live under `k8s/`; deployment is not a Go package — it's `helm` (see
`e2e.mk`'s `e2e-deploy`):

- `config/` — env-driven `Config` (no deps)
- `k8s/cluster/` — kubeconfig → `Client` (controller-runtime client + rest.Config); depends on `config`
- `k8s/nodes/` — GPU node count/label precondition check; depends on `cluster`
- `k8s/pods/` — list helpers for pods running in the cluster; depends on `cluster`
- `k8s/portforward/` — SPDY port-forward to a pod (like `kubectl port-forward`); depends on `cluster`
- `metrics/` — scrape + parse Prometheus text format (no deps)
- `suite/` — ties `config`+`cluster`+`nodes` together; used by `tests/*/main_test.go`
- `tests/metrics/` — the metrics suite: scrapes the metricsd sidecar `/metrics`
- `hack/` — `create-cluster.py` + `requirements.txt`, the k3d + fake-gpu-operator provisioning script (Python, not Go — see above for why)
