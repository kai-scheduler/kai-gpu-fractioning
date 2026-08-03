# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- metricsd metric names are now configurable via Helm (`metricsAgent.metricNames.{gpuMemoryUsedBytes,gpuSmUtilizationPercent,gpuSmUtilizationPercentNormalized}`), and the per-pod metric labels are `namespace, pod, pod_uuid, gpu_uuid, gpu` (renamed from `pod_uid`/`gpu_index`) to integrate with external metric consumers.

### Changed
- The minimum supported NVIDIA GPU Operator version is now **v26.7.1** (was v26.7.0). A cluster running v26.7.0 is reported as unsupported on the `GpuFractioningConfig` `Ready` condition and the node-level daemons are not rolled out. Note that a ClusterPolicy labelled only `26.7` normalizes to `v26.7.0` and is therefore also rejected; label it with the full patch version.

### Fixed
- fractiond now defaults a missing GPU-memory request or limit from the other (so `request == limit`). A container that annotates only `.request` now also gets `NVIDIA_GPU_MEMORY_LIMITS` injected (enforced at the requested size instead of being unbounded), and a container that annotates only `.limit` gets `NVIDIA_GPU_MEMORY_REQUESTS` populated. The retroactive-enforcement audit applies the same defaulting.
