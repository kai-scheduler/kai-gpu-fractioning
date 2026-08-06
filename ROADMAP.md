# Roadmap

This is the roadmap for **kai-gpu-fractioning**: what its maintainers intend to work on, in
roughly the order they intend to work on it. It is a statement of direction, not a commitment to
dates. Concrete work is tracked in
[GitHub issues](https://github.com/kai-scheduler/kai-gpu-fractioning/issues); anything here that
is being actively worked on has an issue.

Proposals are welcome — open an issue. See [GOVERNANCE.md](GOVERNANCE.md) for how significant
changes are decided. Maintainers review this document at least once per release cycle.

## Near term

- **Graduate the API.** `GpuFractioningConfig` is `v1alpha1`. Stabilize the surface, then move it
  to `v1beta1` with a conversion path, once it has been exercised by adopters.
- **Harden the release.** Publish an SBOM with each release, sign the released images and chart,
  and earn the OpenSSF Best Practices and Scorecard badges.
- **Broaden the validated matrix.** Publish and test against an explicit matrix of Kubernetes
  versions, container runtimes (containerd and CRI-O, by NRI version) and NVIDIA driver / GPU
  Operator versions, rather than the single supported combination documented today.
- **Documentation.** A troubleshooting guide covering the `gpu-fractioning.nvidia.com/Ready` node
  condition and the common misconfigurations, and worked end-to-end examples of sharing a GPU
  across workloads.

## Medium term

- **Enforce compute, not only memory.** Today the enforced boundary is GPU memory. Extend
  enforcement so a fraction can bound SM share as well.
- **Dynamic Resource Allocation.** Let a fraction be expressed as a DRA claim rather than as pod
  annotations, so allocation and enforcement share one Kubernetes-native representation.
- **MIG interoperability.** Run cleanly on MIG-partitioned GPUs, so a cluster can mix MIG
  partitioning and MPS-based fractioning without the two enforcement paths conflicting.
- **Observability.** Expand the per-pod metrics beyond memory and SM utilization, and surface
  enforcement events — a container terminated for exceeding its limit — as first-class signals.

## Long term

- GPU sharing on architectures and driver configurations beyond the MPS-based path used today.
- Multi-tenancy hardening: stronger isolation guarantees between fractions on a shared GPU, and
  the audit trail to prove them.

## Relationship to KAI Scheduler

kai-gpu-fractioning enforces on the node the fractions that
[KAI Scheduler](https://github.com/kai-scheduler/KAI-Scheduler) assigns, so items that change the
interface between them — DRA and compute-sharing constraints above — are sequenced against the
[KAI Scheduler roadmap](https://github.com/kai-scheduler/KAI-Scheduler/blob/main/roadmap.md) and
agreed with its maintainers. The rest of this roadmap is independent of it.
