# metricsd e2e — debugging snapshot (2026-07-05)

Working notes for the `e2e/metricsd-tests` branch. Captures what's fixed, what
still fails, the confirmed root cause of the open failure, and how to resume.

## TL;DR

Running `make test-e2e` repeatedly surfaced a chain of independent problems.
Most are fixed. The **one open issue** is TC-1 (`TestE2E_SingleFractionalPodAttribution`)
failing with `gpu_sharing_gpu_memory_used_bytes = 0, want 8589934592`.

Root cause of the open issue is **confirmed**: the plugin logs
`NVML unavailable; GPU process metrics will be zero (pod labels still reported)`
— it falls back to the dead-collector, so it emits the correct pod-label series
but a hard-coded `0` memory value. Not a test-code bug; it's the plugin ↔
nvml-mock `.so` loading.

## Test status (last full run)

| Test | Result | Notes |
|------|--------|-------|
| TC-1 `SingleFractionalPodAttribution` | ❌ FAIL | `memory=0` — NVML unavailable (see below) |
| TC-7 `FullGPUPodIsExcludedFromMetrics` | ✅ PASS | |
| TC-8 `MalformedAnnotationIsIgnoredNotFatal` | ✅ PASS | |
| `GPUSharingPluginMetricsEndpointHealthy` | ✅ PASS | |
| TC-9 `PodDeletionMidCollectionDoesNotBreakExporter` | ⚠️ blocked | Same NVML-unavailable path as TC-1; last run also hit a leftover `tc9-delete-mid-collection-pod` from a prior aborted run (deleted manually) |

TC-7/TC-8 pass because they assert *absence*/non-fatality, which the
dead-collector's zeroes satisfy. TC-1/TC-9 need a real non-zero memory value, so
they expose the NVML fallback.

## Changes made (all in the working tree, not committed)

1. **`Makefile`** — busybox pre-import so workload pods never do a live
   (429-rate-limited) Docker Hub pull:
   - Added `E2E_WORKLOAD_IMAGE` (`busybox:1.37`) and
     `E2E_WORKLOAD_IMAGE_PLATFORM` (`linux/$(docker daemon arch)`).
   - Added `e2e-load-workload-image` target, chained into `e2e`. It uses
     `docker save --platform ...` → `k3d image import <tar>` because plain
     `k3d image import <name>` fails on Docker Desktop's containerd image store
     ("content digest ... not found" — multi-arch manifest list).

2. **`test/e2e/workload/workload.go`**
   - `DefaultImage` → `busybox:1.37` (must match the pre-imported tag; the test
     does not read `E2E_WORKLOAD_IMAGE`).
   - Container command → `sh -c "sleep 86400 & wait # <marker>"`.
     **Why:** busybox `ash` exec-optimizes a single simple command, replacing the
     shell with `sleep 86400` and dropping the `#` comment — so no process
     carried the marker and `HostPID`'s `/proc` grep found nothing. `& wait`
     keeps the shell alive with the marker in its argv. Matched PID (the shell)
     is in the pod cgroup, which is all attribution needs.

3. **`test/e2e/nvmlmock/nvmlmock.go`** — `SetProcesses` now calls a new
   `restartNVMLMock` that captures the GPU nodes up front and **re-applies
   `nvidia.com/gpu.present=true` on every poll tick** while waiting for the
   rollout. Refactored `rolloutRestart` into `bumpRestartAnnotation` +
   `daemonSetReady` (plugin restart path unchanged).
   **Why:** nvml-mock's `preStop` `cleanup.sh` runs
   `kubectl label node ... nvidia.com/gpu.present-`, stripping the label its own
   `nodeSelector` requires. The label is bootstrapped only once at cluster
   creation (k3d `--k3s-node-label`) and k3s never re-adds it, so a plain rolling
   restart deadlocks (DESIRED→0, replacement never schedules). This was the
   user-chosen fix ("re-apply label in test") over editing the manifest.

Build/vet clean with `-tags e2e` (run from inside `test/e2e`, it's a separate module).

## Resolved issues (history, so we don't re-chase them)

- **`ErrImageNeverPull` (plugin)** — `make test-e2e` alone never imports the
  plugin image; that's `make e2e-load-plugin-image`. Also, a **k3d cluster
  stop/start drops images** imported via `k3d image import` (observed: node
  container `StartedAt` ~5 min ago with `RestartCount: 0`). Re-import before
  running, or use the full `make e2e`.
- **busybox `429 Too Many Requests`** — fixed by pre-import (change #1).
- **busybox tag** — 1.36 vs 1.37/1.38 is irrelevant to the 429; it's an
  anonymous Docker Hub pull-rate limit, not a version issue.
- **nvml-mock rollout deadlock** — fixed by change #3.
- **HostPID marker not found** — fixed by change #2.

## OPEN: TC-1 `memory=0` — confirmed diagnosis

Reproduced manually (pod `repro-frac` in `metricsd-e2e`, PID 13721) and verified
every upstream step is correct:

- Repro pod Running; PID 13721 (`sh -c sleep 86400 & wait # gpumock-...`) alive,
  cgroup `/kubepods/besteffort/pod<uid>/<containerid>` ✅
- nvml-mock configmap, mounted `/etc/nvml-mock/config.yaml`, and on-host
  `/var/lib/nvml-mock/driver/config/config.yaml` all contain
  `pid: 13721, used_gpu_memory: 8589934592` on Device0 ✅
- Plugin registers the container via NRI ("loaded container mapping",
  "completed GPU metrics collect", not "skipped ... no pods using GPUs") ✅
- **Plugin NVML is in dead-collector fallback** → logs
  `NVML unavailable; GPU process metrics will be zero (pod labels still reported)`.
  Every collect shows `podMetrics:0, unmatchedGPUProcesses:0` (NVML returns 0
  processes), so the pod-label series exists but memory is always 0. ❌

`waitForSeries` returns as soon as the series *exists* (even a zeroed
placeholder), so `GaugeValue` reads the 0.

### Leading hypothesis
Startup-ordering race: the plugin initializes NVML (loads the mock
`libnvidia-ml.so` from `/var/lib/nvml-mock/driver` via `LD_LIBRARY_PATH`) **before**
nvml-mock's `setup.sh` finishes installing the driver. nvml-mock's pod reports
Ready before the `.so` is fully written, so a plugin (re)start races it.
Alternative: a path/mount mismatch for the `.so`.

### Next step (read-only) — where I stopped
Read the plugin's NVML init + the daemonset `.so` path / `LD_LIBRARY_PATH`:
```
grep -rn "NVML unavailable\|LD_LIBRARY_PATH\|nvml-mock/driver\|libnvidia-ml\|nvmlInit\|Init()" \
  sharing-manager/metricsd/internal sharing-manager/metricsd/deploy/daemonset.yaml sharing-manager/metricsd/cmd
```
Then confirm on a node whether `libnvidia-ml.so.1` exists under
`/var/lib/nvml-mock/driver/usr/lib64/` at plugin start, and whether the plugin
retries NVML init or fails once and gives up.

### Candidate fixes (once cause is confirmed)
- Plugin: retry `nvmlInit` with backoff instead of a one-shot fallback, OR gate
  startup on the driver `.so` existing.
- Test/setup: ensure nvml-mock's driver is fully installed before the plugin
  (re)starts — e.g. an nvml-mock readiness probe that checks the installed `.so`,
  or have `SetProcesses` wait for the driver file before restarting the plugin.

## Known latent issue (not yet hit in a passing run)
`HostPID` requires **exactly one** `/proc` match for the marker. During repro,
a transient second PID (13858, already gone) briefly matched alongside 13721.
If a transient process ever shares the marker at scan time, `HostPID` errors
with "want exactly one host PID". Consider filtering to processes whose cgroup
is under `/kubepods` / matches the target pod, or picking the live match.

## Environment notes / gotchas
- Cluster: k3d `gpu-sharing-e2e`, 2 agents (arm64, Apple Silicon host).
- `nvidia.com/gpu.present=true` label is fragile: **any** nvml-mock pod
  termination (incl. a manual `kubectl rollout restart ds/nvml-mock`) strips it
  via `cleanup.sh`, killing both nvml-mock and the plugin (shared nodeSelector).
  Re-apply with:
  `kubectl label node k3d-gpu-sharing-e2e-agent-0 k3d-gpu-sharing-e2e-agent-1 nvidia.com/gpu.present=true --overwrite`
- `make e2e-build-plugin-image` is currently **broken**:
  `gcc: error: unrecognized command-line option '-m64'` (CGO cross-compile in
  `sharing-manager/metricsd/Dockerfile`). Unrelated to this work, but it blocks
  rebuilding the plugin image. The host already has a valid `gpu-sharing-plugin:e2e`;
  import it directly with `k3d image import gpu-sharing-plugin:e2e --cluster gpu-sharing-e2e`.
- Plugin metrics port: `2112` (`wget -qO- http://127.0.0.1:2112/metrics` from
  inside a plugin pod).
- Leftover repro artifacts to clean up: pod `metricsd-e2e/repro-frac` and the
  hand-patched `gpu-operator/nvml-mock-config` configmap (has `pid: 13721`).

## Quick cluster health check
```
kubectl get nodes -L nvidia.com/gpu.present            # both agents must show "true"
kubectl get ds -n gpu-operator nvml-mock               # 2/2
kubectl get ds -n gpu-sharing gpu-sharing-plugin       # 2/2
kubectl logs -n gpu-sharing <plugin-pod> | grep -i "NVML unavailable"   # should be absent when healthy
```
