# gpu-sharing-plugin

`gpu-sharing-plugin` is the containerd NRI plugin component. It handles
container lifecycle events, reads GPU memory annotations, patches annotated
containers with NVIDIA GPU memory environment variables and the MPS pipe
directory mount, and exports pod-level GPU metrics.

## Layout

```text
gpu-sharing-plugin/
  Dockerfile
  Makefile
  cmd/gpu-sharing-plugin/
  internal/metrics/
  internal/plugin/
  internal/store/
  go.mod
  go.sum
```

This directory is an independent Go module.

## Build And Test

From the repository root:

```sh
make -C gpu-sharing-plugin fmt
make -C gpu-sharing-plugin test
make -C gpu-sharing-plugin build
```

The component build writes:

```text
gpu-sharing-plugin/bin/gpu-sharing-plugin
```

To write into the root `bin/` directory instead:

```sh
make -C gpu-sharing-plugin build BIN_DIR=../bin
```

## Image

The component Makefile defaults to
`REGISTRY=runai.jfrog.io/op-containers-lab-virt`, so the default image is
`runai.jfrog.io/op-containers-lab-virt/gpu-sharing-plugin:dev`.

Build the plugin image from the repository root:

```sh
make -C gpu-sharing-plugin docker-build TAG=dev
```

For multi-platform builds:

```sh
make -C gpu-sharing-plugin docker-buildx TAG=dev
```

The image uses `gpu-sharing-plugin/Dockerfile` and runs the plugin from a
distroless base image. The plugin links the NVIDIA Go NVML bindings, so the
component build uses cgo and the runtime image uses the Debian distroless base.

## Runtime Flags

```text
--config          plugin configuration file, default /etc/gpu-sharing-plugin/config.yaml
--socket          NRI socket path, default /var/run/nri/nri.sock
--plugin-name     NRI plugin name, default gpu-sharing-plugin
--plugin-index    NRI plugin ordering index, default 10
--log-level       debug, info, warn, or error
--retry-interval  reconnect delay after NRI exits, default 5s
```

The same settings can be provided with `CONFIG_PATH`, `NRI_SOCKET_PATH`,
`PLUGIN_NAME`, `PLUGIN_INDEX`, `LOG_LEVEL`, and `RETRY_INTERVAL`.

## Configuration

```yaml
logPodEvents: true
metrics:
  enabled: true
  address: :2112
  path: /metrics
  interval: 5s
  procRoot: /proc
```

When either targeted annotation is present on the pod or container for the
container currently being created, the plugin patches:

- `NVIDIA_GPU_MEMORY_REQUESTS`
- `NVIDIA_GPU_MEMORY_LIMITS`
- `CUDA_MPS_PIPE_DIRECTORY=/tmp/nvidia-mps/`
- a bind mount from `/run/nvidia-mps/` on the host to `/tmp/nvidia-mps/` in the
  container

The annotation key includes the target container name:

```yaml
metadata:
  annotations:
    nvidia.com/gpu-memory.container.cuda-vector-add.request: 8Gi
    nvidia.com/gpu-memory.container.cuda-vector-add.limit: 16Gi
```

A pod can include annotations for more than one container by adding additional
`nvidia.com/gpu-memory.container.<name>.{request,limit}` keys. Invalid target
container-name syntax rejects container creation. If only request or limit is
annotated for a container, the missing bound is deduced with the same value.

## GPU Metrics

When `metrics.enabled=true`, the plugin starts a Prometheus exporter on
`metrics.address` and serves `metrics.path`. A background sweeper runs every
`metrics.interval`, queries NVML process metrics, resolves each GPU process PID
through `metrics.procRoot/<pid>/cgroup`, and uses NRI bookkeeping to enrich the
sample with Kubernetes pod labels.

Exported metrics include:

- `gpu_sharing_gpu_memory_used_bytes`
- `gpu_sharing_gpu_sm_utilization_percent`
- `gpu_sharing_gpu_sm_utilization_percent_normalized` — SM utilization divided by
  the pod's requested GPU fraction and capped at 100. The requested fraction is
  read from the pod annotation named by `gpuFractionAnnotation` (default
  `gpu-fraction`, e.g. `"0.5"`); a pod using as much of the GPU as it requested
  reports 100. When the fraction annotation is absent or unparseable it falls
  back to a fraction of 1, so the value equals the raw SM utilization.

Metric names are configurable under `metrics.metricNames`.

NVML returns process-level data. The plugin joins
`nvmlDeviceGetProcessUtilization` with compute and graphics running-process
queries, then aggregates matching processes by pod and GPU. GPU attachment is
inferred from Linux device metadata in the NRI container spec, using NVIDIA
major `195` and GPU minors `0` through `32`. For active annotated pods attached
to a GPU, the exporter keeps publishing zero values when NVML has no process
reading; deleted pods are pruned once their last tracked container is removed.
