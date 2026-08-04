package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSetDaemonHealth(t *testing.T) {
	SetDaemonHealth("fractiond", 2, 3)

	if got := testutil.ToFloat64(daemonReadyNodes.WithLabelValues("fractiond")); got != 2 {
		t.Errorf("daemon_ready_nodes{fractiond} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(daemonDesiredNodes.WithLabelValues("fractiond")); got != 3 {
		t.Errorf("daemon_desired_nodes{fractiond} = %v, want 3", got)
	}
}

func TestSetNodeHealth(t *testing.T) {
	SetNodeHealth(4, 1)

	if got := testutil.ToFloat64(nodesReady); got != 4 {
		t.Errorf("nodes_ready = %v, want 4", got)
	}
	if got := testutil.ToFloat64(nodesDegraded); got != 1 {
		t.Errorf("nodes_degraded = %v, want 1", got)
	}
}
