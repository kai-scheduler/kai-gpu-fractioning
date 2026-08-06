# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- CNCF project-repository requirements, ahead of making the repository public in
  the `kai-scheduler` organization: `GOVERNANCE.md` (deferring to the KAI
  Scheduler project governance), `MAINTAINERS.md`, `ADOPTERS.md`, `SUPPORT.md`,
  `ROADMAP.md`, and `LICENSE-docs` (CC-BY-4.0) alongside the Apache-2.0
  `LICENSE`, per CNCF Charter section 11. `CODE_OF_CONDUCT.md` now explicitly
  adopts the CNCF Code of Conduct and names <conduct@cncf.io> as an escalation
  path, and the README references it, carries the CNCF footer and the LF
  Projects copyright/trademark notice, and states the dual code/docs licensing.
- Open-source compliance plumbing ahead of the public release: `CLA.md`
  (Developer Certificate of Origin 1.1), `CODE_OF_CONDUCT.md`, a generated
  `THIRD-PARTY.txt` covering the 92 third-party Go modules linked into the
  shipped binaries (also shipped in every image at `/THIRD-PARTY.txt`), and the
  Apache-2.0 SPDX header on every authored file. `make license-check` and
  `make third-party-check` enforce both in CI.
- metricsd metric names are now configurable via Helm (`metricsAgent.metricNames.{gpuMemoryUsedBytes,gpuSmUtilizationPercent,gpuSmUtilizationPercentNormalized}`), and the per-pod metric labels are `namespace, pod, pod_uuid, gpu_uuid, gpu` (renamed from `pod_uid`/`gpu_index`) to integrate with external metric consumers.

### Changed
- All four images now build from an NVIDIA-approved base container. `operator`,
  `fractiond` and `metricsd` move from `gcr.io/distroless/*` to
  `nvcr.io/nvidia/distroless/go:v4.0.8`; `mpsd` stays on the approved public
  `nvidia/cuda` base. No OS packages are added on top of any base. The operator
  container now runs as uid **1000** (the base image's `nvs` user) instead of
  65532, and `fractiond` and `metricsd` set `USER 0:0` explicitly because the
  new base defaults to a non-root user and both need root (the NRI socket and
  NVML respectively).
- The minimum supported NVIDIA GPU Operator version is now **v26.7.1** (was v26.7.0). A cluster running v26.7.0 is reported as unsupported on the `GpuFractioningConfig` `Ready` condition and the node-level daemons are not rolled out. Note that a ClusterPolicy labelled only `26.7` normalizes to `v26.7.0` and is therefore also rejected; label it with the full patch version.

### Fixed
- fractiond now defaults a missing GPU-memory request or limit from the other (so `request == limit`). A container that annotates only `.request` now also gets `NVIDIA_GPU_MEMORY_LIMITS` injected (enforced at the requested size instead of being unbounded), and a container that annotates only `.limit` gets `NVIDIA_GPU_MEMORY_REQUESTS` populated. The retroactive-enforcement audit applies the same defaulting.
