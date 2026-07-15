# Code Review Findings

## [HIGH] Concurrent Audit goroutines can double-stop the same containers
**File:** `sharing-manager/sharingd/internal/audit/sentinel.go:62`
**Verdict:** CONFIRMED
**Summary:** Each `Audit()` call independently spawns a goroutine over the same violator set; two overlapping calls (rapid NRI reconnects) both attempt to stop the same container IDs.
**Failure scenario:** NRI disconnects and reconnects twice in quick succession; two `Audit()` goroutines each build a violator list from the same Synchronize snapshot. Goroutine 1 stops container C; goroutine 2 then tries to stop C again, gets a CRI error ("container not running"), and logs it as a remediation failure — operators see a false enforcement failure on a container that was correctly stopped.

---

## [MEDIUM] CONTAINER_CREATED state passes enforcement gate but CRI StopContainer may reject it
**File:** `sharing-manager/sharingd/internal/audit/detector.go:76`
**Verdict:** PLAUSIBLE
**Summary:** `check()` passes containers in `CONTAINER_CREATED` state to the violators list, but containerd's CRI `StopContainer` may return an error for a container that has been created but not yet started.
**Failure scenario:** A GPU-sharing container that was injected during `CreateContainer` but has not yet transitioned to `RUNNING` appears in the Synchronize snapshot as `CONTAINER_CREATED` and passes the state gate. `CRIStopper.Stop()` issues `StopContainer` on it; containerd returns `"container not in running state"`. The remediator logs the error and moves on, leaving the container in a limbo state. The container later starts without the expected injection.

---

## [LOW] gRPC client not reset after transport failure — stopper stays broken across CRI socket restarts
**File:** `sharing-manager/sharingd/internal/audit/cri_stopper.go:119`
**Verdict:** PLAUSIBLE
**Summary:** `runtimeClient()` caches the gRPC client after the first successful dial and never evicts it; after a CRI socket restart the stale connection returns permanent transport errors until the process restarts.
**Failure scenario:** The CRI socket (`/run/containerd/containerd.sock`) is restarted (containerd upgrade, crash). The cached `*grpc.ClientConn` returns a transport error on the next `StopContainer` RPC. `runtimeClient()` returns the same cached client on all subsequent calls (`s.client != nil`), so every future stop attempt fails. gRPC auto-reconnect may mitigate this, but no reset path exists short of calling `Close()` and recreating the stopper.

---

## [LOW] patchNodeConditions labels filter spans all managed components — mpsd failure marks sharingd-healthy nodes not-ready
**File:** `operator/internal/controller/gpusharingconfig_controller.go:251`
**Verdict:** PLAUSIBLE
**Summary:** `managedByLabels` matches pods by `LabelManagedBy` only, not by component, so an unhealthy mpsd pod marks a node `Ready=False` even when sharingd is healthy.
**Failure scenario:** mpsd crashes on node X; sharingd is healthy and injecting normally. `patchNodeConditions` lists all managed pods (sharingd + mpsd), finds the failing mpsd pod, and patches node X to `Ready=False`. Users and alerting rules see node X as not GPU-sharing-ready and may block workloads — even though injection (sharingd) is working correctly.

---

## [CLEANUP] Dead nil-guard in `detector.logger()` and `remediator.logger()` duplicated across both structs
**File:** `sharing-manager/sharingd/internal/audit/detector.go:143`
**Verdict:** CONFIRMED
**Summary:** Both `logger()` methods contain an identical nil-fallback that can never execute because `NewSentinel` always sets a non-nil `log` before assigning it to both sub-structs.
**Failure scenario:** `NewSentinel` (sentinel.go:31-33) defaults `log` to `slog.Default()` when nil, then assigns the non-nil value to both `detector.log` and `remediator.log`. The nil branches in both `logger()` methods are unreachable dead code that will mislead anyone adding a second constructor.

---

## [CLEANUP] listOpts rebuilt from scratch on every pagination page in RemoveNodeConditions
**File:** `operator/internal/common/daemonmgr/conditions.go:178`
**Verdict:** CONFIRMED
**Summary:** The full `[]client.ListOption` slice (including `MatchingLabels` and `Limit`) is reconstructed on every continuation page instead of appending only the `Continue` token to a base slice.
**Failure scenario:** A future change adds a filter to the initial `listOpts` but forgets to mirror it in the loop's reconstruction; pages 2+ silently drop the filter. A simpler form is to build the base slice once before the loop and append `client.Continue(token)` each iteration.

---

## [CLEANUP] `quantityToDecimalMB` is a single-use private wrapper with no isolation value
**File:** `sharing-manager/sharingd/internal/annotations/annotations.go:103`
**Verdict:** CONFIRMED
**Summary:** `quantityToDecimalMB` is only ever called from `parseToDecimalMB`; the extra function adds indirection without testability or reuse benefit.
**Failure scenario:** A reader following the conversion path must jump between two functions. Inlining removes the indirection with no loss of testability since `parseToDecimalMB` is already the tested entry point.
