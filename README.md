# GPU Sharing Operator

A Kubernetes operator that enables **multiple pods to safely share a single GPU with enforced memory boundaries**.

The operator manages the full lifecycle of GPU sharing on a cluster: it deploys an [NRI](https://github.com/containerd/nri) plugin that injects per-container GPU memory limits at container creation time — before the container process starts — and runs [NVIDIA MPS](https://docs.nvidia.com/deploy/mps/index.html) with GPU memory accounting enabled on every shared GPU.

It is designed to run alongside [KAI Scheduler](https://github.com/NVIDIA/KAI-Scheduler): KAI Scheduler decides *which* fraction of *which* GPU a workload gets, and gpu-sharing enforces that memory boundary on the node and exports per-pod metrics for the resulting fractional GPUs.

## How It Works

1. A cluster admin installs the operator and a `GpuSharingConfig` custom resource is created (the Helm chart ships a default one).
2. The **operator** (controller) reconciles the CR and rolls out the node-level components as DaemonSets to the selected GPU nodes.
3. **mpsd** runs an NVIDIA MPS control daemon on each node with per-process GPU memory accounting (`memacct`) enabled and context-share disabled.
4. **sharingd** registers as an NRI plugin with the container runtime. When a pod carrying GPU-memory annotations is created, sharingd injects `NVIDIA_GPU_MEMORY_REQUESTS` / `NVIDIA_GPU_MEMORY_LIMITS` (and the MPS pipe mount) into the container **before it starts**.
5. The NVIDIA driver enforces `NVIDIA_GPU_MEMORY_LIMITS` as a hard cap, so a container cannot allocate beyond its share and impact its neighbors on the same GPU. A container that exceeds its limit is terminated (out-of-memory), the same way a container exceeding its Kubernetes memory limit is.
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
- [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) **v26.7.1 or newer**, which delivers the **NVIDIA driver `r615` or newer / CUDA 13.4** and the `nvidia` [RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/) that mpsd and metricsd run under
- A scheduler that assigns fractional GPUs — designed to run alongside [KAI Scheduler](https://github.com/NVIDIA/KAI-Scheduler)

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
    # limit is optional; omit it and it defaults to the request
    nvidia.com/container.trainer.gpu-memory.limit: 16Gi
spec:
  containers:
    - name: trainer
      image: nvcr.io/nvidia/cuda:12.4.1-base-ubuntu22.04
      command: ["sleep", "infinity"]
```

- The container name in the annotation key selects which container the limits apply to; a pod may carry annotations for several containers.
- **Both `request` and `limit` are optional**, but at least one must be present for the container to be treated as a shared-GPU container. If only one is set, the other defaults to it — so a request-only container is capped at its request rather than left unbounded, and a limit-only container gets its request populated for accounting.
- **limit** is the hard memory cap the driver enforces. **request** is the workload's declared share; the GPU fraction used to normalize SM-utilization metrics is derived from the limit, falling back to the request.
- Values are Kubernetes quantities (`8Gi`, `512Mi`, …) and must resolve to at least 1 MB. A malformed value fails container creation unless sharingd is running fail-open.
- The GPU **device assignment** (`nvidia.com/container.<name>.gpus.devices`) is set by the scheduler (KAI Scheduler); sharingd injects `NVIDIA_VISIBLE_DEVICES` from it.

## `GpuSharingConfig` reference

A single cluster-scoped CR configures the whole stack. Field docs are authoritative in [`api/v1alpha1/gpusharingconfig_types.go`](api/v1alpha1/gpusharingconfig_types.go).

| Field | Description |
|-------|-------------|
| `spec.nodeSelector` *(required)* | Which nodes the sharingd/mpsd DaemonSets target. **Immutable** — set once at creation. |
| `spec.sharingAgent` | sharingd options (annotation prefix, log level, fail-open, retroactive enforcement). |
| `spec.metricsAgent` | metricsd options (`enabled`, `runtimeClassName`, metric-name overrides). |
| `spec.mpsDaemon` | mpsd supervisor options (e.g. `gracefulStopDelay`). |

Status is surfaced as conditions on the CR:
- **`SharingdReady`** / **`MpsdReady`** — per-daemon rollout health (ready vs desired nodes).
- **`Ready`** — aggregate health of the managed daemons. When not ready, it is refined with NVIDIA GPU Operator dependency failures (e.g. the GPU Operator is missing or below the required version) to explain why.
- **`DriverUpgradeInProgress`** — `True` while a targeted GPU node is undergoing an NVIDIA driver upgrade; the daemons are automatically drained from that node (so MPS shuts down cleanly before the driver unloads) and rescheduled when it completes.

Per-node health is also published as a `gpu-sharing.nvidia.com/Ready` **node condition** (refined with the CUDA driver version when a node is unhealthy).

## Observability

metricsd exports per-pod GPU metrics (Prometheus, plain HTTP). Built-in metric names (labels: `namespace`, `pod`, `pod_uuid`, `gpu_uuid`, `gpu`):

- `gpu_sharing_gpu_memory_used_bytes`
- `gpu_sharing_gpu_sm_utilization_percent`
- `gpu_sharing_gpu_sm_utilization_percent_normalized` — SM utilization divided by the pod's GPU fraction, capped at 100.

The controller also exports operational metrics (`gpu_sharing_daemon_ready_nodes`, `gpu_sharing_daemon_desired_nodes`, `gpu_sharing_nodes_ready`, `gpu_sharing_nodes_degraded`) plus the standard controller-runtime `controller_runtime_reconcile_*` series.

With the Prometheus Operator installed, set `prometheus.enabled=true` to have the chart create a `ServiceMonitor` (controller) and a `PodMonitor` (metricsd, one scrape target per GPU node). Both scrape over plain HTTP; restrict access with a NetworkPolicy if needed. Metric names are overridable via `metricsAgent.metricNames` / `spec.metricsAgent.metricNames`.

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

## License

Apache 2.0 — see [LICENSE](LICENSE).
