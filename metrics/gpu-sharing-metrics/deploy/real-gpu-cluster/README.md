# Deploy — real GPU cluster

> **Target environment:** these manifests are written for a Run.ai cluster with the
> NVIDIA GPU Operator. The test workloads use the Run.ai scheduler and the metric
> names in `daemonset.yaml` are overridden to match the Run.ai Prometheus naming
> convention. For a generic cluster, remove the `metricNames` block from the
> ConfigMap to use the default names.

Manifests for a cluster with the NVIDIA GPU Operator and Run.ai scheduler.

## Prerequisites

- NVIDIA GPU Operator installed (driver DaemonSet running, device files at `/run/nvidia/driver/dev/`)
- NRI enabled in the container runtime (`--enable-nri` or equivalent)
- Prometheus Operator installed (for PodMonitor)
- Access to `runai.jfrog.io` registry

## 1. Build and push the image

```bash
# Uses the current git HEAD as the tag by default.
# Override any variable inline:
#   REGISTRY=myregistry.io TAG=v0.2.0 ./deploy/real-gpu-cluster/build-and-push.sh
./deploy/real-gpu-cluster/build-and-push.sh
```

The script runs all tests, logs in to the registry, and pushes a multi-arch image (linux/amd64 by default).

## 2. Deploy the plugin

```bash
kubectl apply -f deploy/real-gpu-cluster/daemonset.yaml
```

This creates:
- `gpu-sharing` namespace
- `gpu-sharing-plugin-config` ConfigMap (edit metric names / intervals here)
- `gpu-sharing-plugin` DaemonSet (runs on every `nvidia.com/gpu.present=true` node)
- `gpu-sharing-plugin-metrics` headless Service (Prometheus scrape target)

Verify the DaemonSet is ready:

```bash
kubectl -n gpu-sharing rollout status daemonset/gpu-sharing-plugin
kubectl -n gpu-sharing logs -l app=gpu-sharing-plugin --tail=40
```

## 3. Install the PodMonitor

```bash
kubectl apply -f deploy/real-gpu-cluster/podmonitor.yaml
```

The PodMonitor is created in the `runai` namespace (where Prometheus Operator watches by default). It scrapes `/metrics` on port `2112` every 15 seconds with `honorLabels: true` so the `pod`/`namespace` labels refer to the GPU workload, not the plugin pod.

## 4. Run test workloads

```bash
kubectl apply -f deploy/real-gpu-cluster/test-fractional-gpu-pods.yaml
```

Check that metrics appear (replace `<prometheus-url>` with your Prometheus address):

```bash
kubectl -n gpu-sharing port-forward svc/gpu-sharing-plugin-metrics 2112:2112 &
curl -s http://localhost:2112/metrics | grep runai_gpu
```

## 5. Tear down

```bash
kubectl delete -f deploy/real-gpu-cluster/test-fractional-gpu-pods.yaml
kubectl delete -f deploy/real-gpu-cluster/podmonitor.yaml
kubectl delete -f deploy/real-gpu-cluster/daemonset.yaml
```

## Configuration

Edit the `config.yaml` key in `daemonset.yaml` before applying, or patch the ConfigMap and restart the DaemonSet:

| Field | Default | Description |
|---|---|---|
| `metrics.interval` | `5s` | NVML sampling cadence (floor: 2s) |
| `metrics.smUtilizationWindow` | `30s` | Sliding-window averaging for SM utilization; set empty to disable |
| `metrics.metricNames.gpuMemoryUsedBytes` | `gpu_sharing_gpu_memory_used_bytes` | Prometheus metric name |
| `metrics.metricNames.gpuSmUtilizationPercent` | `gpu_sharing_gpu_sm_utilization_percent` | Prometheus metric name |

## Troubleshooting

**DaemonSet pod stays Pending** — check node selectors: the pod requires `nvidia.com/gpu.present=true`.

**No metrics / all zero** — confirm `hostPID: true` is set (required for NVML process enumeration on driver 570+) and that the NVIDIA device files exist at `/run/nvidia/driver/dev/`.

**`pod` label shows plugin pod name** — ensure `honorLabels: true` is present in the PodMonitor.

**Containers not tracked** — the plugin only tracks pods with `nvidia.com/container.<name>.gpu-memory.limit` or `nvidia.com/container.<name>.gpu-memory.request` annotations. Run.ai sets these automatically for fractional GPU workloads.
