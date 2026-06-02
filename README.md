# GPU Sharing Operator

A Kubernetes operator that enables **multiple pods to safely share a single GPU with enforced memory boundaries**.

The operator manages the full lifecycle of GPU sharing on a cluster: it configures [NVIDIA MPS](https://docs.nvidia.com/deploy/mps/index.html) for hardware-level memory enforcement and deploys an [NRI](https://github.com/containerd/nri) plugin that injects per-container GPU memory limits at container creation time — before the container process starts.

## How It Works

1. A cluster admin deploys the operator and creates a `GpuSharingConfig` custom resource.
2. The **operator** (controller) reconciles the CR and rolls out two DaemonSets to GPU nodes.
3. **mpsd** starts an NVIDIA MPS control daemon on each node, enabling per-process GPU memory accounting.
4. **sharingd** registers as an NRI plugin with the container runtime. When a pod with GPU memory annotations is scheduled, sharingd intercepts container creation and injects the appropriate `NVIDIA_GPU_MEMORY_LIMITS` environment variable.
5. MPS enforces the memory cap at the hardware level — containers that exceed their allocation receive a CUDA OOM error rather than impacting neighbors.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  Controller (Deployment)                            │
│  Reconciles GpuSharingConfig CR → manages DaemonSets│
└──────────────┬──────────────────────┬───────────────┘
               │                      │
               ▼                      ▼
┌──────────────────────┐  ┌──────────────────────────┐
│  mpsd (DaemonSet)    │  │  sharingd (DaemonSet)    │
│  Per-node MPS daemon │  │  NRI plugin — injects    │
│  GPU memory accounting│  │  GPU memory limits into  │
│                      │  │  containers at creation   │
└──────────────────────┘  └──────────────────────────┘
```

## Components

| Component | Description |
|-----------|-------------|
| **operator** | Kubernetes controller that watches `GpuSharingConfig` CRDs and manages the sharing-manager DaemonSets |
| **mpsd** | MPS daemon — configures and launches NVIDIA MPS on each GPU node |
| **sharingd** | Sharing daemon — NRI plugin that enforces per-container GPU memory limits |

## Repository Structure

```
├── operator/          # Controller (CRD, reconciler)
├── sharing-manager/   # Node-level components
│   ├── mpsd/          # MPS daemon
│   ├── sharingd/      # Sharing daemon (NRI plugin)
│   └── common/        # Shared packages
├── deployments/       # Helm chart
├── hack/              # Build and dev scripts
└── test/              # Cross-component tests
```

## Prerequisites

- Kubernetes 1.28+
- containerd 2.0+ (NRI enabled by default) or CRI-O with NRI support
- NVIDIA GPU driver installed on nodes
- NVIDIA GPUs with Volta+ architecture (compute capability 7.0+)

## Development

```bash
make build      # Build all binaries
make test       # Run unit tests
make validate   # Format, vet, and lint
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
