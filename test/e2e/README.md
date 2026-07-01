# gpu-sharing-operator e2e tests

Stage 1: metrics-only e2e tests for `gpu-sharing-plugin` (the metricsd NRI
plugin), running against a real k3d cluster with
[fake-gpu-operator](https://github.com/run-ai/fake-gpu-operator) installed.

## Quick start

```sh
export E2E_FAKE_GPU_OPERATOR_VERSION=0.1.0   # see releases link below
make e2e
```

This creates a k3d cluster, installs fake-gpu-operator, builds and loads the
`:e2e`-tagged `gpu-sharing-plugin` image into the cluster, and runs the test
suite. Tear the cluster down afterwards with:

```sh
make e2e-cluster-down
```

## How it's split

Cluster lifecycle and the Go test suite are deliberately separate concerns:

| Step | Owner | What |
|---|---|---|
| Create k3d cluster (N worker nodes) | `test/e2e/hack/create-cluster.py` | `k3d cluster create` with retry-on-failure |
| Install fake-gpu-operator | `test/e2e/hack/create-cluster.py` | `helm upgrade -i` with per-pool GPU count/product/memory |
| Verify GPU nodes | `test/e2e/suite` (`nodes.VerifyGPUNodes`) | precondition check, no mutation |
| Deploy `gpu-sharing-plugin` DaemonSet | `test/e2e/suite` (`plugin.Deploy`) | the one thing the Go suite deploys — it's what's under test |
| Run test assertions | `test/e2e/tests` | metrics scrape/assert |

This mirrors [Grove's e2e split](https://github.com/ai-dynamo/grove/tree/main/operator/e2e):
a standalone script (there: `create-e2e-cluster.py`; here: `create-cluster.py`)
provisions the cluster and cluster-level dependencies, while the Go suite only
connects, verifies preconditions, and deploys/tests the component actually
under test. `create-cluster.py` reuses grove's pattern — pydantic-settings for
`E2E_*`-prefixed config, typer for the CLI, retry-on-create-failure — but is
scoped down to what this project needs: a k3d cluster with fake-gpu-operator
and nothing else, so the dependency list is just `typer`, `pydantic-settings`,
`sh` — no docker SDK (nothing here builds or pre-pulls images) and no `rich`
(plain stdout is enough for CI logs).

## Cluster configuration (`test/e2e/hack/create-cluster.py`)

All configurable via `E2E_*` environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `E2E_CLUSTER_NAME` | `gpu-sharing-e2e` | k3d cluster name |
| `E2E_WORKER_NODES` | `2` | number of agent nodes, each labeled into the fake-GPU node pool |
| `E2E_K3S_IMAGE` | `rancher/k3s:v1.31.5-k3s1` | k3s node image |
| `E2E_GPU_NODE_POOL` | `default` | fake-gpu-operator node pool name |
| `E2E_GPUS_PER_NODE` | `2` | GPUs advertised per node in that pool |
| `E2E_GPU_PRODUCT` | `NVIDIA A100-SXM4-40GB` | simulated GPU product |
| `E2E_GPU_MEMORY_MIB` | `40960` | simulated GPU memory (MiB) |
| `E2E_FAKE_GPU_OPERATOR_VERSION` | *(required)* | fake-gpu-operator Helm chart version — see [releases](https://github.com/run-ai/fake-gpu-operator/releases) |
| `E2E_MAX_RETRIES` | `3` | cluster-creation retry attempts |

```sh
pip install -r test/e2e/hack/requirements.txt

E2E_WORKER_NODES=4 E2E_FAKE_GPU_OPERATOR_VERSION=0.1.0 test/e2e/hack/create-cluster.py
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
| `make e2e` | cluster up → build+load plugin image → run tests |
| `make e2e-cluster-up` | create the k3d cluster + install fake-gpu-operator |
| `make e2e-cluster-down` | delete the k3d cluster |
| `make e2e-cluster-deps` | `pip install -r test/e2e/hack/requirements.txt` |
| `make e2e-build-plugin-image` | `docker build --build-arg GO_TAGS=e2e` |
| `make e2e-load-plugin-image` | build + `k3d image import` into `E2E_CLUSTER_NAME` |
| `make test-e2e` | run the Go suite only (cluster + image assumed already up/loaded) |

`E2E_CLUSTER_NAME`, `E2E_WORKER_NODES`, `E2E_PLUGIN_IMAGE`,
`E2E_FAKE_GPU_OPERATOR_VERSION` are overridable `make` variables mirroring the
script's env vars. `PYTHON` overrides the Python interpreter (default
`python3`).

## Go test suite configuration

| Variable | Default | Purpose |
|---|---|---|
| `E2E_KUBECONFIG` / `KUBECONFIG` | `~/.kube/config` | cluster to connect to |
| `E2E_TEST_NAMESPACE` | `runai-proj-1` | namespace for test workload pods |
| `E2E_PLUGIN_NAMESPACE` | `gpu-sharing` | namespace the plugin DaemonSet is deployed into |
| `E2E_GPU_NODE_SELECTOR` | `nvidia.com/gpu.present=true` | label selector used to verify GPU nodes |
| `E2E_EXPECTED_GPU_NODES` | `0` (unchecked) | exact GPU node count to assert, if > 0 — set to `E2E_WORKER_NODES` |
| `E2E_PLUGIN_IMAGE` | *(manifest default)* | override the plugin image, e.g. a locally built `:e2e` tag |
| `E2E_PLUGIN_IMAGE_PULL_POLICY` | `Never` if `E2E_PLUGIN_IMAGE` is set, else unset | pull policy for the overridden image (k3d-imported images have no registry to pull from) |
| `E2E_POD_READY_TIMEOUT` | `2m` | timeout waiting for test pods to become Running |
| `E2E_DAEMONSET_READY_TIMEOUT` | `3m` | timeout waiting for the plugin DaemonSet rollout |
| `E2E_POLL_INTERVAL` | `2s` | poll interval used by all wait helpers |

## What's covered

- `TestE2E_GPUSharingPluginMetricsEndpointHealthy` — smoke test: the plugin's
  `/metrics` endpoint is reachable and returns valid Prometheus output.

## CI

`.github/workflows/e2e.yaml` runs `make e2e` on PRs touching
`sharing-manager/metricsd/**` or `test/e2e/**`: it installs `k3d` (official
install script) and Helm, then runs the same `make e2e-cluster-up` /
`make e2e-load-plugin-image` / `make test-e2e` targets used locally, and tears
the cluster down in an `if: always()` step. On failure it dumps node/pod state
and plugin/status-updater logs before tearing down.

## Package layout

Each Go package is single-purpose, mirroring grove's `e2e/` split so
dependencies only point one way (leaf packages like `waiter`/`metrics` have
no dependency on the rest of the framework, so future additions — e.g. a
diagnostics collector — can depend on them without an import cycle):

- `config/` — env-driven `Config` (no deps)
- `cluster/` — kubeconfig → `Client` (clientset + rest.Config); depends on `config`
- `waiter/` — generic poll-until-condition helper (no deps)
- `nodes/` — GPU node count/label precondition check; depends on `cluster`
- `plugin/` — deploys the plugin DaemonSet manifest, waits for rollout; depends on `cluster`, `waiter`
- `pods/` — list helpers for pods running in the cluster; depends on `cluster`
- `portforward/` — SPDY port-forward to a pod (like `kubectl port-forward`); depends on `cluster`
- `metrics/` — scrape + parse Prometheus text format (no deps)
- `suite/` — ties `config`+`cluster`+`nodes`+`plugin` together; used by `tests/main_test.go`
- `hack/` — `create-cluster.py` + `requirements.txt`, the k3d + fake-gpu-operator provisioning script (Python, not Go — see above for why)
