// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package metrics defines and registers the operator's custom Prometheus metrics
// on controller-runtime's registry, so they are exposed on the same (secure)
// controller metrics endpoint as the built-in controller_runtime_* series.
//
// Reconcile counters, error counters, and durations are already provided by
// controller-runtime out of the box (controller_runtime_reconcile_total{result},
// _reconcile_errors_total, _reconcile_time_seconds), so they are intentionally
// not duplicated here. What this package adds is the domain-specific health that
// only the operator knows: per-daemon ready/desired node counts and the
// aggregate GPU-node condition counts.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const namespace = "gpu_fractioning"

var (
	daemonReadyNodes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "daemon_ready_nodes",
		Help:      "Number of nodes on which the named managed daemon's pod is Ready.",
	}, []string{"daemon"})

	daemonDesiredNodes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "daemon_desired_nodes",
		Help:      "Number of nodes the named managed daemon's DaemonSet targets (desired scheduled).",
	}, []string{"daemon"})

	nodesReady = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "nodes_ready",
		Help:      "GPU nodes whose gpu-fractioning.nvidia.com/Ready condition was True at the last node-condition evaluation.",
	})

	nodesDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "nodes_degraded",
		Help:      "GPU nodes whose gpu-fractioning.nvidia.com/Ready condition was False at the last node-condition evaluation.",
	})
)

func init() {
	ctrlmetrics.Registry.MustRegister(daemonReadyNodes, daemonDesiredNodes, nodesReady, nodesDegraded)
}

// SetDaemonHealth records a managed daemon's ready/desired node counts, observed
// on each reconcile.
func SetDaemonHealth(daemon string, ready, desired int32) {
	daemonReadyNodes.WithLabelValues(daemon).Set(float64(ready))
	daemonDesiredNodes.WithLabelValues(daemon).Set(float64(desired))
}

// SetNodeHealth records the aggregate GPU-node condition counts observed during a
// node-condition patch pass.
func SetNodeHealth(ready, degraded int) {
	nodesReady.Set(float64(ready))
	nodesDegraded.Set(float64(degraded))
}
