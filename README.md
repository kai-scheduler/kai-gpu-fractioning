# GPU Sharing Operator

A Kubernetes operator that enables **multiple pods to safely share a single GPU with enforced memory boundaries**.

The operator manages the full lifecycle of GPU sharing on a cluster: it configures [NVIDIA MPS](https://docs.nvidia.com/deploy/mps/index.html) for hardware-level memory enforcement and deploys an [NRI](https://github.com/containerd/nri) plugin that injects per-container GPU memory limits at container creation time — before the container process starts.

<!-- TODO(Hagay): positioning / messaging pass. In particular, how we frame this
relative to KAI Scheduler: gpu-sharing enforces the memory boundaries and exports
per-pod metrics for the fractional GPUs that KAI Scheduler assigns. Want to make
the "works with KAI Scheduler" story explicit up top? -->

## How It Works

1. A cluster admin installs the operator and a `GpuSharingConfig` custom resource is created (the Helm chart ships a default one).
2. The **operator** (controller) reconciles the CR and rolls out the node-level components as DaemonSets to the selected GPU nodes.
3. **mpsd** runs an NVIDIA MPS control daemon on each node, enabling per-process GPU memory accounting and enforcement.
4. **sharingd** registers as an NRI plugin with the container runtime. When a pod carrying GPU-memory annotations is created, sharingd injects the MPS memory-limit environment variables (and the MPS pipe mount) into the container **before it starts**.
5. MPS enforces the memory cap at the hardware level — a container that exceeds its allocation gets a CUDA out-of-memory error instead of impacting its neighbors on the same GPU.
6. **metricsd** (a sidecar alongside sharingd) exports per-pod GPU memory and utilization metrics for the shared GPUs.

## Architecture

```
┌───────────────────────────────────────────────────────────┐
│  Controller (Deployment)                                  │
│  Reconciles GpuSharingConfig CR → manages the DaemonSets  │
└───────────────┬───────────────────────────┬───────────────┘
                │                           │
                ▼                           ▼
┌───────────────────────┐   ┌───────────────────────────────┐
│  mpsd (DaemonSet)     │   │  sharingd (DaemonSet)         │
│  Per-node MPS control │   │  NRI plugin — injects GPU     │
│  daemon; GPU memory   │   │  memory limits into containers│
│  accounting/enforce   │   │  at creation time             │
│                       │   │   └── metricsd (sidecar):     │
│                       │   │       per-pod GPU metrics     │
└───────────────────────┘   └───────────────────────────────┘
```

## Components

| Component | Description |
|-----------|-------------|
| **operator** | Kubernetes controller that reconciles `GpuSharingConfig` and manages the node-level DaemonSets |
| **mpsd** | Runs and supervises the NVIDIA MPS control daemon on each GPU node |
| **sharingd** | NRI plugin that enforces per-container GPU memory limits at container creation |
| **metricsd** | Sidecar that exports per-pod GPU memory/utilization metrics for shared GPUs |

## Prerequisites

- Kubernetes 1.28+
- containerd 2.0+ with **NRI enabled**, or CRI-O with NRI support
- An `nvidia` [RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/) (mpsd and metricsd run under it) — typically provided by the [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator)
- NVIDIA GPU driver installed on nodes; Volta+ GPUs (compute capability 7.0+)
- A scheduler that assigns fractional GPUs — designed to run alongside [KAI Scheduler](https://github.com/NVIDIA/KAI-Scheduler) <!-- TODO(Hagay): confirm exact supported versions / any hard dependency to state here -->

## Install

The operator and its Helm chart are published as OCI artifacts to GitHub Container Registry.

```sh
helm install gpu-sharing \
  oci://ghcr.io/kai-scheduler/gpu-sharing/gpu-sharing \
  --version <VERSION> \
  --namespace gpu-sharing --create-namespace
```

The chart installs the CRD, the controller, and a default `GpuSharingConfig` targeting nodes labelled `nvidia.com/gpu.present=true`. Verify the rollout:

```sh
kubectl -n gpu-sharing get pods
kubectl get gpusharingconfig default -o yaml   # check .status.conditions → Ready
```

Common chart values (see [`operator/charts/values.yaml`](operator/charts/values.yaml) for the full list):

| Value | Default | Purpose |
|-------|---------|---------|
| `metricsAgent.enabled` | `true` | run the metricsd metrics sidecar |
| `metricsAgent.runtimeClassName` | `nvidia` | RuntimeClass for metricsd (needs GPU/NVML access) |
| `metrics.enabled` / `metrics.port` | `true` / `8080` | controller metrics endpoint (plain HTTP) |
| `prometheus.enabled` | `false` | install a `ServiceMonitor` + `PodMonitor` (requires the Prometheus-Operator CRDs) |
| `nodeSelector` | `{}` | scheduling constraint for the **controller** Deployment |

> The GPU **nodeSelector** for the DaemonSets is set on the `GpuSharingConfig` CR (`spec.nodeSelector`), not the chart-level `nodeSelector`.

## Requesting a fractional GPU

A workload opts into GPU sharing with **per-container** pod annotations that declare its GPU-memory request and limit:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: fractional-gpu
  annotations:
    # nvidia.com/container.<container-name>.gpu-memory.<request|limit>
    nvidia.com/container.trainer.gpu-memory.request: 8Gi
    nvidia.com/container.trainer.gpu-memory.limit: 16Gi
spec:
  containers:
    - name: trainer
      image: nvcr.io/nvidia/cuda:12.4.1-base-ubuntu22.04
      command: ["sleep", "infinity"]
```

- The container name in the annotation key selects which container the limits apply to; a pod may carry annotations for several containers.
- **request** feeds accounting/metrics (the pod's GPU fraction); **limit** is the hard memory cap MPS enforces. If only one is set, the other defaults to it.
- The GPU **device assignment** (`nvidia.com/container.<name>.gpus.devices`) is set by the scheduler (KAI Scheduler); sharingd injects `NVIDIA_VISIBLE_DEVICES` from it. <!-- TODO(Hagay): expand the KAI Scheduler request flow here, or link to KAI docs. -->

Runnable examples live in [`operator/config/samples/`](operator/config/samples/). <!-- TODO: add samples (separate task in D). -->

## `GpuSharingConfig` reference

A single cluster-scoped CR configures the whole stack. Field docs are authoritative in [`api/v1alpha1/gpusharingconfig_types.go`](api/v1alpha1/gpusharingconfig_types.go).

| Field | Description |
|-------|-------------|
| `spec.nodeSelector` *(required)* | Which nodes the sharingd/mpsd DaemonSets target. **Do not change after creation** (a validating webhook to enforce this is planned). |
| `spec.sharingAgent` | sharingd options (annotation prefix, log level, fail-open, retroactive enforcement). |
| `spec.metricsAgent` | metricsd options (`enabled`, `runtimeClassName`, metric-name overrides). |
| `spec.mpsDaemon` | mpsd supervisor options (e.g. `gracefulStopDelay`). |

Status is surfaced as conditions on the CR:
- **`Ready`** — aggregate health of the managed daemons.
- **`DriverUpgradeInProgress`** — `True` while a targeted GPU node is undergoing an NVIDIA driver upgrade; the daemons are automatically drained from that node (so MPS shuts down cleanly before the driver unloads) and rescheduled when it completes.

## Observability

metricsd exports per-pod GPU metrics (Prometheus, plain HTTP). Built-in metric names (labels: `namespace`, `pod`, `pod_uuid`, `gpu_uuid`, `gpu`):

- `gpu_sharing_gpu_memory_used_bytes`
- `gpu_sharing_gpu_sm_utilization_percent`
- `gpu_sharing_gpu_sm_utilization_percent_normalized` — SM utilization divided by the pod's GPU fraction, capped at 100.

The controller also exports operational metrics (`gpu_sharing_daemon_ready_nodes`, `gpu_sharing_daemon_desired_nodes`, `gpu_sharing_nodes_ready`, `gpu_sharing_nodes_degraded`) plus the standard controller-runtime `controller_runtime_reconcile_*` series.

With the Prometheus Operator installed, set `prometheus.enabled=true` to have the chart create a `ServiceMonitor` (controller) and a `PodMonitor` (metricsd, one scrape target per GPU node). Both scrape over plain HTTP; restrict access with a NetworkPolicy if needed. Metric names are overridable via `metricsAgent.metricNames` / `spec.metricsAgent.metricNames`.

## Troubleshooting

See [`docs/troubleshooting.md`](docs/troubleshooting.md) for common issues (mpsd unschedulable without the `nvidia` RuntimeClass, NRI not enabled, CUDA OOM on limit exceed, driver-upgrade drains, reading CR/node conditions and metrics). <!-- TODO: add docs/troubleshooting.md (separate task in D). -->

## Repository structure

```
├── api/               # GpuSharingConfig CRD types (v1alpha1)
├── operator/          # Controller (reconciler) + Helm chart (operator/charts/)
├── sharing-manager/   # Node-level components
│   ├── mpsd/          #   MPS control daemon supervisor
│   ├── sharingd/      #   NRI plugin (memory-limit injection)
│   ├── metricsd/      #   per-pod GPU metrics sidecar
│   └── common/        #   shared packages
├── pkg/               # Shared libraries
├── hack/              # Build and dev scripts
└── test/              # Cross-component / e2e tests
```

## Development

```sh
make build      # build all binaries
make test       # run unit tests
make validate   # format, vet, and lint
```

Full dev/build/e2e setup will live in [`DEVELOPMENT.md`](DEVELOPMENT.md) <!-- TODO: add (separate task in D) -->; see [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE).
