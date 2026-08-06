# Roadmap

kai-gpu-fractioning is the node-side enforcement half of GPU sharing in
[KAI Scheduler](https://github.com/kai-scheduler/KAI-Scheduler): KAI Scheduler decides which
fraction of which GPU a workload gets, and this project enforces that boundary on the node. Its
roadmap therefore tracks the fractioning items on the
[KAI Scheduler roadmap](https://github.com/kai-scheduler/KAI-Scheduler/blob/main/roadmap.md).

This is a statement of direction, not a commitment to dates. Concrete work is tracked in
[GitHub issues](https://github.com/kai-scheduler/kai-gpu-fractioning/issues); anything here that
is being actively worked on has an issue.

## Near term

- **Graduate the API.** `GpuFractioningConfig` is `v1alpha1`. Stabilize the surface, then move it
  to `v1beta1` with a conversion path, once it has been exercised by adopters.
- **Harden the release.** Publish an SBOM with each release and sign the released images and
  chart, and adopt the OpenSSF Best Practices and Scorecard badges.
- **Broaden the validated matrix.** Publish and test against an explicit matrix of Kubernetes
  versions, container runtimes (containerd and CRI-O, by NRI version) and NVIDIA driver / GPU
  Operator versions, rather than the single supported combination documented today.
- **Documentation.** A troubleshooting guide covering the `gpu-fractioning.nvidia.com/Ready` node
  condition and the common misconfigurations, and worked examples of sharing a GPU across
  workloads end to end with KAI Scheduler.

## Medium term

- **Compute alongside memory.** Today the enforced boundary is GPU memory. Extend enforcement to
  the compute-sharing constraints being defined on the KAI Scheduler side, so a fraction can bound
  SM share as well as memory.
- **DRA.** Follow KAI Scheduler's Dynamic Resource Allocation work through to fractional devices,
  so fractions can be expressed as DRA claims rather than pod annotations.
- **MIG.** Interoperate cleanly with MIG-partitioned GPUs, so a cluster can mix MIG partitioning
  and MPS-based fractioning without the two enforcement paths conflicting.
- **Observability.** Expand the per-pod metrics beyond memory and SM utilization, and surface
  enforcement events (a container terminated for exceeding its limit) as first-class signals.

## Long term

- Support for GPU sharing on architectures and driver configurations beyond the MPS-based path
  used today.
- Multi-tenancy hardening: stronger isolation guarantees between fractions on a shared GPU, and
  the audit trail to prove them.

## How this roadmap is maintained

Maintainers review it at least once per release cycle. Proposals are welcome — open an issue, or
raise it in the `#kai-scheduler` channel on the
[CNCF Slack](https://communityinviter.com/apps/cloud-native/cncf). See
[GOVERNANCE.md](GOVERNANCE.md) for how significant changes are decided.
